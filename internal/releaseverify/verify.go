package releaseverify

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"

	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	updatepkg "go.mewis.me/codemcp/internal/update"
)

const (
	ExpectedModulePath        = "go.mewis.me/codemcp"
	ExpectedGitHubRepository  = "mewisme/codemcp"
	ExpectedVanityURL         = "https://go.mewis.me/codemcp?go-get=1"
	ExpectedGitRemote         = "https://github.com/mewisme/codemcp"
	packageMaintainerTemplate = `{{ index .Env "PACKAGE_MAINTAINER" }}`
)

var retiredExecutableIdentityPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9_])(chatgpt-mcp(?:\.exe)?|cgm(?:\.exe|\.cmd)?|cmcp(?:\.exe|\.cmd)?)([^a-z0-9_]|$)`)

func VerifyTelemetryEndpoint(raw string) error {
	metadata, err := producttelemetry.ParseEndpoint(raw)
	if err != nil || !metadata.Available || metadata.Product != "codemcp" {
		return errors.New("release telemetry endpoint metadata is missing or invalid")
	}
	return nil
}

type TelemetryExpectation string

const (
	TelemetryUnchecked TelemetryExpectation = ""
	TelemetryAbsent    TelemetryExpectation = "absent"
	TelemetryPresent   TelemetryExpectation = "present"
)

func VerifyRepository(root, observedRepository string) error {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if observedRepository = strings.TrimSpace(observedRepository); observedRepository != "" && observedRepository != ExpectedGitHubRepository {
		return fmt.Errorf("release repository = %q, want %q", observedRepository, ExpectedGitHubRepository)
	}
	if err := verifyModulePath(root); err != nil {
		return err
	}
	if updatepkg.DefaultOwner+"/"+updatepkg.DefaultRepo != ExpectedGitHubRepository || updatepkg.PackageName != "codemcp" {
		return errors.New("updater release metadata does not target the canonical CodeMCP repository")
	}
	if err := verifyGoReleaser(root); err != nil {
		return err
	}
	if err := verifyReleaseWorkflows(root); err != nil {
		return err
	}
	if err := verifyMigrationNotes(root); err != nil {
		return err
	}
	return verifyNoLegacyRepositoryURLs(root)
}

func verifyModulePath(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[0] != "module" || fields[1] != ExpectedModulePath {
		return fmt.Errorf("module declaration = %q, want module %s", line, ExpectedModulePath)
	}
	return nil
}

func verifyGoReleaser(root string) error {
	data, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		return fmt.Errorf("read goreleaser config: %w", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("decode goreleaser config: %w", err)
	}
	if stringValue(cfg["project_name"]) != updatepkg.PackageName {
		return fmt.Errorf("goreleaser project_name = %q, want %q", stringValue(cfg["project_name"]), updatepkg.PackageName)
	}
	before := mapValue(cfg["before"])
	for _, hook := range stringSlice(before["hooks"]) {
		fields := strings.Fields(hook)
		if len(fields) < 2 || fields[0] != "node" || !strings.HasPrefix(filepath.ToSlash(fields[1]), "scripts/") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(fields[1]))); err != nil {
			return fmt.Errorf("goreleaser before hook references missing script %q", fields[1])
		}
	}
	release := mapValue(cfg["release"])
	github := mapValue(release["github"])
	if stringValue(github["owner"])+"/"+stringValue(github["name"]) != ExpectedGitHubRepository {
		return errors.New("goreleaser release target is not the canonical CodeMCP repository")
	}
	checksum := mapValue(cfg["checksum"])
	if stringValue(checksum["name_template"]) != updatepkg.ChecksumName {
		return errors.New("goreleaser checksum asset name drifted from the updater contract")
	}
	if len(stringSlice(checksum["ids"])) != 0 {
		return errors.New("goreleaser checksum must cover the complete canonical artifact set")
	}
	setupExtraFiles := []string{
		"./dist/codemcp_windows_amd64_setup.exe",
		"./dist/codemcp_windows_arm64_setup.exe",
	}
	if !sameStrings(extraFileGlobs(checksum["extra_files"]), setupExtraFiles) {
		return errors.New("goreleaser checksum extra files do not cover both canonical Windows setup artifacts")
	}
	if !sameStrings(extraFileGlobs(release["extra_files"]), setupExtraFiles) {
		return errors.New("goreleaser release extra files do not publish both canonical Windows setup artifacts")
	}
	replaceDraft, _ := release["replace_existing_draft"].(bool)
	if !replaceDraft {
		return errors.New("goreleaser release must replace an existing draft on safe workflow retries")
	}
	signs := sliceValue(cfg["signs"])
	if len(signs) != 1 {
		return errors.New("expected one canonical checksum signature definition")
	}
	sign := mapValue(signs[0])
	if stringValue(sign["id"]) != "checksums" ||
		stringValue(sign["cmd"]) != "cosign" ||
		stringValue(sign["signature"]) != "${artifact}.sigstore.json" ||
		stringValue(sign["artifacts"]) != "checksum" {
		return errors.New("goreleaser checksum signature contract drifted")
	}
	signArgs := stringSlice(sign["args"])
	for _, required := range []string{"sign-blob", "--bundle=${signature}", "${artifact}", "--yes"} {
		if !containsExact(signArgs, required) {
			return fmt.Errorf("goreleaser checksum signature is missing argument %q", required)
		}
	}
	archives := sliceValue(cfg["archives"])
	if len(archives) != 1 || stringValue(mapValue(archives[0])["name_template"]) != "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}" {
		return errors.New("goreleaser archive naming drifted from the stable release artifact contract")
	}
	nfpms := sliceValue(cfg["nfpms"])
	if len(nfpms) != 1 {
		return errors.New("expected one canonical Linux package definition")
	}
	nfpm := mapValue(nfpms[0])
	if stringValue(nfpm["id"]) != updatepkg.PackageName ||
		stringValue(nfpm["package_name"]) != updatepkg.PackageName ||
		stringValue(nfpm["file_name_template"]) != "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}.{{ .Format }}" ||
		!sameStrings(stringSlice(nfpm["ids"]), []string{updatepkg.PackageName}) ||
		!sameStrings(stringSlice(nfpm["formats"]), []string{"deb", "rpm"}) ||
		stringValue(nfpm["vendor"]) != linuxPackageVendor ||
		stringValue(nfpm["homepage"]) != ExpectedGitRemote ||
		stringValue(nfpm["maintainer"]) != packageMaintainerTemplate ||
		stringValue(nfpm["description"]) != linuxPackageDescription ||
		stringValue(nfpm["license"]) != "MIT" ||
		stringValue(nfpm["bindir"]) != "/usr/bin" ||
		!sameStrings(stringSlice(nfpm["goamd64"]), []string{"v1"}) {
		return errors.New("linux package generation drifted from the canonical package contract")
	}
	if len(sliceValue(nfpm["contents"])) != 0 || len(mapValue(nfpm["scripts"])) != 0 {
		return errors.New("linux package definition must not add extra contents or maintainer scripts")
	}
	deb := mapValue(nfpm["deb"])
	rpm := mapValue(nfpm["rpm"])
	if stringValue(deb["compression"]) != "gzip" ||
		stringValue(rpm["compression"]) != "gzip" ||
		stringValue(rpm["summary"]) != linuxPackageDescription ||
		len(mapValue(deb["scripts"])) != 0 ||
		len(mapValue(rpm["scripts"])) != 0 {
		return errors.New("linux package format settings drifted from the canonical package contract")
	}
	buildFound := false
	for _, item := range sliceValue(cfg["builds"]) {
		build := mapValue(item)
		if stringValue(build["id"]) != updatepkg.PackageName {
			continue
		}
		buildFound = true
		if stringValue(build["binary"]) != "cm" {
			return errors.New("release build does not emit the canonical cm binary")
		}
		if !sameStrings(stringSlice(build["goos"]), []string{"linux", "windows", "darwin"}) ||
			!sameStrings(stringSlice(build["goarch"]), []string{"amd64", "arm64"}) {
			return errors.New("release platform matrix drifted from the canonical updater contract")
		}
		wantLDFlag := "-X " + ExpectedModulePath + "/internal/telemetry/product.Endpoint={{ index .Env \"TELEMETRY_ENDPOINT\" }}"
		if !containsExact(stringSlice(build["ldflags"]), wantLDFlag) {
			return errors.New("release build does not inject product telemetry through the canonical ldflag")
		}
		hooks := mapValue(build["hooks"])
		post := sliceValue(hooks["post"])
		if len(post) != 1 {
			return errors.New("release build must define exactly one canonical post-build setup hook")
		}
		setupHook := mapValue(post[0])
		output, _ := setupHook["output"].(bool)
		if stringValue(setupHook["cmd"]) != "sh scripts/release/build-windows-setup.sh \"{{ .Path }}\" \"{{ .Target }}\" dist" || !output {
			return errors.New("release build Windows setup hook drifted from the canonical OSS wrapper")
		}
	}
	if !buildFound {
		return errors.New("canonical CodeMCP release build is missing")
	}
	if err := verifyWindowsSetupBootstrap(root); err != nil {
		return err
	}
	scoops := sliceValue(cfg["scoops"])
	if len(scoops) != 1 {
		return errors.New("expected one canonical Scoop manifest")
	}
	scoop := mapValue(scoops[0])
	if stringValue(scoop["name"]) != updatepkg.PackageName ||
		stringValue(scoop["homepage"]) != ExpectedGitRemote ||
		stringValue(scoop["url_template"]) != ExpectedGitRemote+"/releases/download/{{ .Tag }}/{{ .ArtifactName }}" {
		return errors.New("scoop generation does not target the canonical CodeMCP release")
	}
	casks := sliceValue(cfg["homebrew_casks"])
	if len(casks) != 1 {
		return errors.New("expected one canonical Homebrew cask")
	}
	cask := mapValue(casks[0])
	if stringValue(cask["name"]) != updatepkg.PackageName ||
		stringValue(cask["homepage"]) != ExpectedGitRemote ||
		!sameStrings(stringSlice(cask["binaries"]), []string{"cm"}) {
		return errors.New("homebrew generation does not install the canonical cm binary")
	}
	caskURL := mapValue(cask["url"])
	if stringValue(caskURL["template"]) != ExpectedGitRemote+"/releases/download/{{ .Tag }}/{{ .ArtifactName }}" {
		return errors.New("homebrew generation does not use tag-pinned stable artifact URLs")
	}
	return nil
}

func verifyWindowsSetupBootstrap(root string) error {
	wrapperPath := filepath.Join(root, "scripts", "release", "build-windows-setup.sh")
	wrapperData, err := os.ReadFile(wrapperPath)
	if err != nil {
		return fmt.Errorf("read Windows setup wrapper: %w", err)
	}
	wrapper := string(wrapperData)
	for _, required := range []string{
		"windows_amd64|windows_amd64_*",
		"windows_arm64|windows_arm64_*",
		"codemcp_windows_${arch}_setup.exe",
		"if [ \"$(basename \"$binary\")\" != \"cm.exe\" ]",
		"if [ ! -f \"$binary\" ] || [ -L \"$binary\" ]",
		"makensis=${MAKENSIS:-makensis}",
	} {
		if !strings.Contains(wrapper, required) {
			return fmt.Errorf("windows setup wrapper is missing required contract %q", required)
		}
	}
	for _, forbidden := range []string{"Program Files", "WriteUninstaller"} {
		if strings.Contains(wrapper, forbidden) {
			return fmt.Errorf("windows setup wrapper contains forbidden installer ownership %q", forbidden)
		}
	}
	if retiredExecutableIdentityPattern.MatchString(wrapper) {
		return errors.New("windows setup wrapper contains a retired executable identity")
	}

	templatePath := filepath.Join(root, "installer", "windows", "codemcp.nsi")
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read Windows setup template: %w", err)
	}
	template := string(templateData)
	for _, required := range []string{
		"RequestExecutionLevel user",
		"SetOutPath \"$PLUGINSDIR\"",
		"File /oname=cm.exe \"${BINARY_PATH}\"",
		"ExecWait '\"$PLUGINSDIR\\cm.exe\" install'",
		"ReadEnvStr $InstallRoot \"CM_INSTALL_DIR\"",
		"StrCpy $InstallRoot \"$PROFILE\\.cm\"",
		"ReadRegStr $0 HKCU \"Environment\" \"Path\"",
		"WriteRegExpandStr HKCU \"Environment\" \"Path\"",
		"WM_SETTINGCHANGE",
	} {
		if !strings.Contains(template, required) {
			return fmt.Errorf("windows setup template is missing required contract %q", required)
		}
	}
	for _, forbidden := range []string{"WriteUninstaller", "$PROGRAMFILES", "$PROGRAMFILES64"} {
		if strings.Contains(template, forbidden) {
			return fmt.Errorf("windows setup template contains forbidden installer ownership %q", forbidden)
		}
	}
	if retiredExecutableIdentityPattern.MatchString(template) {
		return errors.New("windows setup template contains a retired executable identity")
	}
	if strings.Count(template, "File /oname=cm.exe \"${BINARY_PATH}\"") != 1 {
		return errors.New("windows setup template must embed exactly one canonical cm.exe payload")
	}
	return nil
}

func verifyReleaseWorkflows(root string) error {
	releaseData, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		return fmt.Errorf("read release workflow: %w", err)
	}
	release := string(releaseData)
	repositoryExpr := "$" + "{{ github.repository }}"
	repositoryOwnerExpr := "$" + "{{ github.repository_owner }}"
	repositoryOwnerIDExpr := "$" + "{{ github.repository_owner_id }}"
	telemetryExpr := "$" + "{{ vars.TELEMETRY_ENDPOINT }}"
	for _, required := range []string{
		"RELEASE_REPOSITORY: " + repositoryExpr,
		`PACKAGE_MAINTAINER: "` + repositoryOwnerExpr + " <" + repositoryOwnerIDExpr + "+" + repositoryOwnerExpr + `@users.noreply.github.com>"`,
		"runs-on: ubuntu-24.04",
		"--github-repository",
		"--vanity-url",
		ExpectedVanityURL,
		"TELEMETRY_ENDPOINT: " + telemetryExpr,
		"--telemetry-endpoint",
		"args: build --snapshot --clean --single-target",
		"TELEMETRY_ENDPOINT: ''",
		"--expect-telemetry absent",
		"nsis=3.09-4ubuntu1",
		"7zip=23.01+dfsg-11",
		"distribution: goreleaser",
		"version: 'v2.18.0'",
		"args: release --clean --draft",
		"--dist dist --expect-telemetry present",
		"scripts/release/verify-windows-setup-payload.sh dist",
		"cosign verify-blob",
		"dist/scoop/codemcp.json",
		"dist/homebrew/Casks/codemcp.rb",
		`gh release edit "${GITHUB_REF_NAME}" --draft=false --latest`,
	} {
		if !strings.Contains(release, required) {
			return fmt.Errorf("release workflow is missing required cutover contract %q", required)
		}
	}
	for _, script := range []string{
		filepath.Join("scripts", "release", "verify", "main.go"),
		filepath.Join("scripts", "release", "verify-windows-setup-payload.sh"),
	} {
		if _, err := os.Stat(filepath.Join(root, script)); err != nil {
			return fmt.Errorf("release workflow helper %s is unavailable: %w", filepath.ToSlash(script), err)
		}
	}
	if strings.Contains(release, "goreleaser-pro") {
		return errors.New("release workflow must use GoReleaser OSS")
	}
	draftIndex := strings.Index(release, "args: release --clean --draft")
	verifyIndex := strings.Index(release, "--dist dist --expect-telemetry present")
	payloadIndex := strings.Index(release, "scripts/release/verify-windows-setup-payload.sh dist")
	signatureIndex := strings.Index(release, "cosign verify-blob")
	manifestIndex := strings.Index(release, "gh release upload")
	publishIndex := strings.Index(release, `gh release edit "${GITHUB_REF_NAME}" --draft=false --latest`)
	if draftIndex < 0 || verifyIndex <= draftIndex || payloadIndex <= verifyIndex ||
		signatureIndex <= payloadIndex || manifestIndex <= signatureIndex || publishIndex <= manifestIndex {
		return errors.New("release workflow must verify the signed draft and attach package manifests before publishing it")
	}

	ciData, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		return fmt.Errorf("read CI workflow: %w", err)
	}
	ci := string(ciData)
	if !strings.Contains(ci, "--expect-telemetry absent") || !strings.Contains(ci, "dist-smoke/") {
		return errors.New("ci workflow does not verify endpoint-less source build telemetry boundary")
	}
	for _, required := range []string{
		"scripts/installer/test-windows-setup.sh",
		"scripts/release/verify-windows-setup-payload.sh",
		"choco install nsis -y --no-progress",
		"scripts/installer/test-windows-setup.ps1",
	} {
		if !strings.Contains(ci, required) {
			return fmt.Errorf("ci workflow is missing Windows setup smoke contract %q", required)
		}
	}
	return nil
}

func verifyMigrationNotes(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "docs", "migration-from-0.2.24.md"))
	if err != nil {
		return fmt.Errorf("read released migration notes: %w", err)
	}
	text := string(data)
	for _, required := range []string{"chatgpt-mcp", "cgm", "executable aliases", "cm upgrade", "update", "upg", "command aliases"} {
		if !strings.Contains(text, required) {
			return fmt.Errorf("released migration notes are missing %q", required)
		}
	}
	return nil
}

func verifyNoLegacyRepositoryURLs(root string) error {
	forbidden := []string{
		"github.com/mewisme/" + "chatgpt-mcp",
		"api.github.com/repos/mewisme/" + "chatgpt-mcp",
		"go.mewis.me/" + "chatgpt-mcp",
	}
	paths := []string{
		"README.md", "install.sh", "install.ps1", ".goreleaser.yaml",
		filepath.Join(".github", "workflows"), "docs",
	}
	for _, relative := range paths {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if err := rejectLegacyURLs(path, relative, forbidden); err != nil {
				return err
			}
			continue
		}
		err = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			display := filepath.ToSlash(strings.TrimPrefix(current, root+string(filepath.Separator)))
			return rejectLegacyURLs(current, display, forbidden)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func rejectLegacyURLs(path, display string, forbidden []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, value := range forbidden {
		if strings.Contains(string(data), value) {
			return fmt.Errorf("%s contains retired repository URL identity %q", display, value)
		}
	}
	return nil
}

func VerifyVanity(ctx context.Context, client *http.Client, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		rawURL = ExpectedVanityURL
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "CodeMCP release verifier")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("resolve Go vanity metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("go vanity endpoint returned HTTP %d", response.StatusCode)
	}
	document, err := html.Parse(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("parse Go vanity metadata: %w", err)
	}
	want := ExpectedModulePath + " git " + ExpectedGitRemote
	found := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found || node == nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "meta" {
			name, content := "", ""
			for _, attribute := range node.Attr {
				switch strings.ToLower(attribute.Key) {
				case "name":
					name = attribute.Val
				case "content":
					content = attribute.Val
				}
			}
			if name == "go-import" && strings.Join(strings.Fields(content), " ") == want {
				found = true
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	if !found {
		return fmt.Errorf("go vanity metadata does not resolve %s to %s", ExpectedModulePath, ExpectedGitRemote)
	}
	return nil
}

func VerifyBinaryTelemetry(ctx context.Context, binary string, expectation TelemetryExpectation) error {
	if expectation != TelemetryAbsent && expectation != TelemetryPresent {
		return fmt.Errorf("unsupported telemetry expectation %q", expectation)
	}
	binary = filepath.Clean(strings.TrimSpace(binary))
	info, err := os.Stat(binary)
	if err != nil {
		return fmt.Errorf("inspect release binary: %w", err)
	}
	if info.IsDir() {
		return errors.New("release binary path is a directory")
	}
	root, err := os.MkdirTemp("", "cm-release-telemetry-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	command := exec.CommandContext(ctx, binary, "--config-dir", filepath.Join(root, "config"), "telemetry", "show", "--json")
	command.Env = replaceEnv(os.Environ(), map[string]string{
		"CM_TELEMETRY":       "0",
		"TELEMETRY_ENDPOINT": "https://runtime-override.invalid/v1/products/codemcp/events",
		"HOME":               root,
		"USERPROFILE":        root,
	})
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("query release telemetry metadata: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var status map[string]any
	if err := json.Unmarshal(output, &status); err != nil {
		return fmt.Errorf("decode release telemetry metadata: %w", err)
	}
	available, _ := status["endpoint_available"].(bool)
	host := stringValue(status["endpoint_host"])
	product := stringValue(status["product"])
	if strings.Contains(string(output), "/v1/products/codemcp/events") || strings.Contains(string(output), "runtime-override.invalid") {
		return errors.New("release telemetry status exposed a runtime-configurable raw endpoint")
	}
	switch expectation {
	case TelemetryAbsent:
		if available || host != "" || product != "" {
			return fmt.Errorf("endpoint-less build reported telemetry metadata: %s", strings.TrimSpace(string(output)))
		}
	case TelemetryPresent:
		if !available || host == "" || product != "codemcp" {
			return fmt.Errorf("release build telemetry metadata is incomplete: %s", strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func VerifyDist(ctx context.Context, distRoot string, expectation TelemetryExpectation) error {
	distRoot = filepath.Clean(strings.TrimSpace(distRoot))
	if distRoot == "" {
		return errors.New("release dist root is required")
	}
	if err := verifyPublishedArtifactMatrix(distRoot); err != nil {
		return err
	}
	archives, err := releaseArchives(distRoot)
	if err != nil {
		return err
	}
	wantPlatforms := updatepkg.PrimaryReleaseLayout().Platforms
	if len(archives) != len(wantPlatforms) {
		return fmt.Errorf("release archive count = %d, want %d", len(archives), len(wantPlatforms))
	}
	seen := map[string]bool{}
	var nativeBinary []byte
	for _, archive := range archives {
		key := archive.platform.OS + "/" + archive.platform.Arch
		if seen[key] {
			return fmt.Errorf("duplicate release archive for %s", key)
		}
		seen[key] = true
		content, err := verifyArchive(archive.path, archive.platform)
		if err != nil {
			return err
		}
		if archive.platform.OS == runtime.GOOS && archive.platform.Arch == runtime.GOARCH {
			nativeBinary = content
		}
	}
	for _, platform := range wantPlatforms {
		if !seen[platform.OS+"/"+platform.Arch] {
			return fmt.Errorf("release archive missing for %s/%s", platform.OS, platform.Arch)
		}
	}
	if err := verifyLinuxPackages(ctx, distRoot); err != nil {
		return err
	}
	if err := verifyWindowsSetups(distRoot); err != nil {
		return err
	}
	if err := verifyReleaseChecksums(distRoot); err != nil {
		return err
	}
	if err := verifyChecksumSignature(distRoot); err != nil {
		return err
	}
	if err := verifyPackageManifests(distRoot); err != nil {
		return err
	}
	if expectation != TelemetryUnchecked {
		if len(nativeBinary) == 0 {
			return fmt.Errorf("release dist has no verifier-host binary for %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		dir, err := os.MkdirTemp("", "cm-release-native-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		name, err := updatepkg.BinaryName(runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nativeBinary, 0755); err != nil {
			return err
		}
		if err := VerifyBinaryTelemetry(ctx, path, expectation); err != nil {
			return err
		}
	}
	return nil
}

func verifyPublishedArtifactMatrix(root string) error {
	expected := map[string]struct{}{}
	for _, artifact := range updatepkg.PrimaryReleaseLayout().Artifacts {
		name, err := updatepkg.ArtifactName(artifact.Kind, artifact.OS, artifact.Arch)
		if err != nil {
			return err
		}
		expected[name] = struct{}{}
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("canonical release artifact %s is unavailable: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 {
			return fmt.Errorf("canonical release artifact %s is not a non-empty regular file", name)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !publishedArtifactLike(entry.Name()) {
			continue
		}
		if _, ok := expected[entry.Name()]; !ok {
			return fmt.Errorf("unexpected published release artifact %q", entry.Name())
		}
	}
	return nil
}

func publishedArtifactLike(name string) bool {
	if !strings.HasPrefix(name, updatepkg.PackageName+"_") {
		return false
	}
	for _, suffix := range []string{".tar.gz", ".zip", ".deb", ".rpm", "_setup.exe"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func verifyWindowsSetups(root string) error {
	for _, arch := range []string{"amd64", "arm64"} {
		name, err := updatepkg.ArtifactName(updatepkg.ArtifactSetup, "windows", arch)
		if err != nil {
			return err
		}
		path := filepath.Join(root, name)
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open windows setup %s: %w", name, err)
		}
		header := make([]byte, 2)
		_, readErr := io.ReadFull(file, header)
		closeErr := file.Close()
		if readErr != nil {
			return fmt.Errorf("read windows setup %s: %w", name, readErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if string(header) != "MZ" {
			return fmt.Errorf("windows setup %s is not a PE executable", name)
		}
	}
	return nil
}

func verifyReleaseChecksums(root string) error {
	path := filepath.Join(root, updatepkg.ChecksumName)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect release checksum manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 1<<20 {
		return errors.New("release checksum manifest must be a bounded non-empty regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read release checksum manifest: %w", err)
	}
	expected := map[string]string{}
	for _, artifact := range updatepkg.PrimaryReleaseLayout().Artifacts {
		name, err := updatepkg.ArtifactName(artifact.Kind, artifact.OS, artifact.Arch)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("read checksummed artifact %s: %w", name, err)
		}
		expected[name] = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	seen := map[string]struct{}{}
	checksumPattern := regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || !checksumPattern.MatchString(fields[0]) {
			return fmt.Errorf("checksum manifest line %d is invalid", lineNumber+1)
		}
		name := fields[1]
		want, ok := expected[name]
		if !ok {
			return fmt.Errorf("checksum manifest contains unexpected artifact %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("checksum manifest contains duplicate artifact %q", name)
		}
		if !strings.EqualFold(fields[0], want) {
			return fmt.Errorf("checksum manifest digest mismatch for %s", name)
		}
		seen[name] = struct{}{}
	}
	for name := range expected {
		if _, ok := seen[name]; !ok {
			return fmt.Errorf("checksum manifest is missing canonical artifact %q", name)
		}
	}
	return nil
}

func verifyChecksumSignature(root string) error {
	path := filepath.Join(root, updatepkg.ChecksumSignatureName)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect checksum signature bundle: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 4<<20 {
		return errors.New("checksum signature bundle must be a bounded non-empty regular file")
	}
	return nil
}

type releaseArchive struct {
	path     string
	platform updatepkg.ReleasePlatform
}

func releaseArchives(root string) ([]releaseArchive, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	canonical := make(map[string]updatepkg.ReleasePlatform, len(updatepkg.PrimaryReleaseLayout().Platforms))
	for _, platform := range updatepkg.PrimaryReleaseLayout().Platforms {
		name, err := updatepkg.ArchiveName(platform.OS, platform.Arch)
		if err != nil {
			return nil, err
		}
		canonical[name] = platform
	}
	result := make([]releaseArchive, 0, len(canonical))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if platform, ok := canonical[entry.Name()]; ok {
			result = append(result, releaseArchive{path: filepath.Join(root, entry.Name()), platform: platform})
			continue
		}
		if publishedArchiveLike(entry.Name()) {
			return nil, fmt.Errorf("unexpected published release archive %q; expected stable canonical filename", entry.Name())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].path < result[j].path })
	return result, err
}

func publishedArchiveLike(name string) bool {
	return strings.HasPrefix(name, updatepkg.PackageName+"_") &&
		(strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip"))
}

func verifyArchive(path string, platform updatepkg.ReleasePlatform) ([]byte, error) {
	if strings.HasSuffix(path, ".zip") {
		return verifyZipArchive(path, platform)
	}
	return verifyTarArchive(path, platform)
}

func verifyTarArchive(path string, platform updatepkg.ReleasePlatform) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var binary []byte
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if retiredExecutable(filepath.Base(filepath.ToSlash(header.Name))) {
			return nil, fmt.Errorf("%s contains retired executable alias %q", filepath.Base(path), header.Name)
		}
		if filepath.ToSlash(header.Name) != platform.BinaryName {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != 0 {
			return nil, fmt.Errorf("%s canonical binary is not regular", filepath.Base(path))
		}
		if binary != nil {
			return nil, fmt.Errorf("%s contains duplicate %s", filepath.Base(path), platform.BinaryName)
		}
		binary, err = io.ReadAll(io.LimitReader(reader, 512<<20))
		if err != nil {
			return nil, err
		}
	}
	if len(binary) == 0 {
		return nil, fmt.Errorf("%s is missing non-empty %s", filepath.Base(path), platform.BinaryName)
	}
	return binary, nil
}

func verifyZipArchive(path string, platform updatepkg.ReleasePlatform) ([]byte, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var binary []byte
	for _, entry := range reader.File {
		if retiredExecutable(filepath.Base(filepath.ToSlash(entry.Name))) {
			return nil, fmt.Errorf("%s contains retired executable alias %q", filepath.Base(path), entry.Name)
		}
		if filepath.ToSlash(entry.Name) != platform.BinaryName {
			continue
		}
		if !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("%s canonical binary is not regular", filepath.Base(path))
		}
		if binary != nil {
			return nil, fmt.Errorf("%s contains duplicate %s", filepath.Base(path), platform.BinaryName)
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		binary, err = io.ReadAll(io.LimitReader(stream, 512<<20))
		closeErr := stream.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if len(binary) == 0 {
		return nil, fmt.Errorf("%s is missing non-empty %s", filepath.Base(path), platform.BinaryName)
	}
	return binary, nil
}

func retiredExecutable(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chatgpt-mcp", "chatgpt-mcp.exe", "cgm", "cgm.exe", "cgm.cmd", "cmcp", "cmcp.exe", "cmcp.cmd":
		return true
	default:
		return false
	}
}

func verifyPackageManifests(root string) error {
	scoopData, err := os.ReadFile(filepath.Join(root, "scoop", "codemcp.json"))
	if err != nil {
		return fmt.Errorf("read Scoop manifest: %w", err)
	}
	var scoop map[string]any
	if err := json.Unmarshal(scoopData, &scoop); err != nil {
		return fmt.Errorf("decode Scoop manifest: %w", err)
	}
	if !manifestBinContains(scoop, "cm.exe") {
		return errors.New("scoop manifest does not install cm.exe")
	}
	if !strings.Contains(string(scoopData), ExpectedGitRemote+"/releases/download/") {
		return errors.New("scoop manifest does not use the canonical CodeMCP release repository")
	}
	if strings.Contains(string(scoopData), "/releases/latest/download/") {
		return errors.New("scoop manifest must remain pinned to an exact release tag")
	}
	if regexp.MustCompile(`codemcp_[0-9]`).Match(scoopData) {
		return errors.New("scoop manifest contains a version-coupled artifact filename")
	}
	if retiredExecutableIdentityPattern.Match(scoopData) {
		return errors.New("scoop manifest contains a retired executable identity")
	}

	caskData, err := os.ReadFile(filepath.Join(root, "homebrew", "Casks", "codemcp.rb"))
	if err != nil {
		return fmt.Errorf("read Homebrew cask: %w", err)
	}
	cask := string(caskData)
	if !regexp.MustCompile(`(?m)^\s*binary\s+["'](?:#\{staged_path\}/)?cm["']`).MatchString(cask) {
		return errors.New("homebrew cask does not install cm")
	}
	if !strings.Contains(cask, ExpectedGitRemote) {
		return errors.New("homebrew cask does not use the canonical CodeMCP repository")
	}
	if strings.Contains(cask, "/releases/latest/download/") {
		return errors.New("homebrew cask must remain pinned to an exact release tag")
	}
	if regexp.MustCompile(`codemcp_[0-9]`).MatchString(cask) {
		return errors.New("homebrew cask contains a version-coupled artifact filename")
	}
	if retiredExecutableIdentityPattern.MatchString(cask) {
		return errors.New("homebrew cask contains a retired executable identity")
	}
	return nil
}

func manifestBinContains(value any, expected string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "bin" && containsManifestString(child, expected) {
				return true
			}
			if manifestBinContains(child, expected) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if manifestBinContains(child, expected) {
				return true
			}
		}
	}
	return false
}

func containsManifestString(value any, expected string) bool {
	switch typed := value.(type) {
	case string:
		return filepath.Base(filepath.ToSlash(typed)) == expected
	case []any:
		for _, child := range typed {
			if containsManifestString(child, expected) {
				return true
			}
		}
	}
	return false
}

func replaceEnv(current []string, replacements map[string]string) []string {
	normalized := make(map[string]string, len(replacements))
	for key, value := range replacements {
		normalized[strings.ToUpper(key)] = value
	}
	result := make([]string, 0, len(current)+len(normalized))
	for _, item := range current {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replace := normalized[strings.ToUpper(key)]; replace {
				continue
			}
		}
		result = append(result, item)
	}
	for key, value := range normalized {
		result = append(result, key+"="+value)
	}
	return result
}

func sameStrings(actual, expected []string) bool {
	left, right := append([]string(nil), actual...), append([]string(nil), expected...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsExact(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func mapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func sliceValue(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func stringValue(value any) string {
	if typed, ok := value.(string); ok {
		return strings.TrimSpace(typed)
	}
	return ""
}

func stringSlice(value any) []string {
	items := sliceValue(value)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value := stringValue(item); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func extraFileGlobs(value any) []string {
	items := sliceValue(value)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if glob := stringValue(mapValue(item)["glob"]); glob != "" {
			result = append(result, glob)
		}
	}
	return result
}
