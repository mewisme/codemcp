package codegraph

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/integrations/managedasset"
)

func TestManagedAssetContractMatchesPinnedRelease(t *testing.T) {
	expected := map[string]string{
		"darwin/amd64":  "cb86a2b62ee676b62a56bf8423600e7d867e752e57f323cdc98c0f6236efd908",
		"darwin/arm64":  "1c73033512d55f67be04717e81532e8beaf7be6fb8531f51a179fa23064ad480",
		"linux/amd64":   "de3391f79ed42622d937e6cd5b7642a7ea8bb7d1473607e80b879ba73ef216b0",
		"linux/arm64":   "6dc935a7b8f1a61e688a578b98ea34680eb2e36d7b91db079d64f4011f1a668f",
		"windows/amd64": "cd76c3c3391f2d40abef12b142151950b6d77abc2d8429e648f89eaa90f5b68a",
		"windows/arm64": "3ca980010bd718a6b5e75be1145806ae6491afb1a59a2cec6cee4bf5c39f1b3a",
	}
	if Version != "v1.6.0" || Repository != "colbymchenry/codegraph" || ChecksumAsset != "SHA256SUMS" {
		t.Fatalf("provenance version=%q repository=%q checksum=%q", Version, Repository, ChecksumAsset)
	}
	for key, sha := range expected {
		goos, goarch, _ := strings.Cut(key, "/")
		asset, ok := AssetFor(goos, goarch)
		if !ok || asset.SHA256 != sha || !strings.Contains(asset.URL, "/releases/download/"+Version+"/") || len(asset.Required) < 3 {
			t.Fatalf("%s asset=%#v ok=%t", key, asset, ok)
		}
	}
	if _, ok := AssetFor("freebsd", "amd64"); ok {
		t.Fatal("unsupported managed asset unexpectedly available")
	}
}

func TestDefaultManagedRootUsesCMConfigRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	if got, want := DefaultManagedRoot(), filepath.Join(root, "managed-assets"); got != want {
		t.Fatalf("managed root=%q want=%q", got, want)
	}
}

func TestResolutionPriorityConfiguredSystemManaged(t *testing.T) {
	runtime, managedPath := installTestManagedCodeGraph(t)
	systemPath := testExecutable(t, "system-codegraph")
	configuredPath := testExecutable(t, "configured-codegraph")

	runtime.lookPath = func(string) (string, error) { return systemPath, nil }
	resolution, err := runtime.Resolve()
	if err != nil || resolution.Source != ExecutableSystem || resolution.Path != systemPath || !resolution.Verified {
		t.Fatalf("system resolution=%#v err=%v", resolution, err)
	}

	runtime.configuredPath = configuredPath
	resolution, err = runtime.Resolve()
	if err != nil || resolution.Source != ExecutableConfigured || resolution.Path != configuredPath || !resolution.Verified {
		t.Fatalf("configured resolution=%#v err=%v", resolution, err)
	}

	runtime.configuredPath = ""
	runtime.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	resolution, err = runtime.Resolve()
	if err != nil || resolution.Source != ExecutableManaged || resolution.Path != managedPath || !resolution.Verified {
		t.Fatalf("managed resolution=%#v err=%v", resolution, err)
	}
}

func TestExternalResolutionCanonicalizesSymlinksWithoutChangingSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available on Windows test hosts")
	}
	target := testExecutable(t, "codegraph-target")
	link := filepath.Join(t.TempDir(), "codegraph")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	value := New(Options{Enabled: true, ManagedRoot: t.TempDir()})
	value.lookPath = func(string) (string, error) { return link, nil }
	resolution, err := value.Resolve()
	if err != nil || resolution.Source != ExecutableSystem || resolution.Path != target || !resolution.Verified {
		t.Fatalf("system symlink resolution=%#v err=%v", resolution, err)
	}

	value.configuredPath = link
	resolution, err = value.Resolve()
	if err != nil || resolution.Source != ExecutableConfigured || resolution.Path != target || !resolution.Verified {
		t.Fatalf("configured symlink resolution=%#v err=%v", resolution, err)
	}
}

func TestSystemResolutionRejectsDanglingAndUnsafeSymlinkTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available on Windows test hosts")
	}
	for _, test := range []struct {
		name   string
		target func(*testing.T) string
	}{
		{name: "dangling", target: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing-codegraph") }},
		{name: "directory", target: func(t *testing.T) string { return t.TempDir() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := test.target(t)
			link := filepath.Join(t.TempDir(), "codegraph")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			value := New(Options{Enabled: true, ManagedRoot: t.TempDir()})
			value.lookPath = func(string) (string, error) { return link, nil }
			resolution, err := value.Resolve()
			if err != nil || resolution.Source != ExecutableUnavailable || resolution.Path != "" || resolution.Verified {
				t.Fatalf("unsafe system symlink resolution=%#v err=%v", resolution, err)
			}
		})
	}
}

func TestResolveGlobalOnlyDiscoversUserInstalledExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux external executable fixture requires POSIX mode semantics")
	}
	value := New(Options{Enabled: true, ManagedRoot: t.TempDir()})
	value.goos, value.goarch = "linux", "amd64"
	value.lookPath = func(name string) (string, error) {
		if name != "codegraph" {
			t.Fatalf("lookPath(%q) want codegraph", name)
		}
		return "", errors.New("missing")
	}
	value.run = func(context.Context, string, []string, int) (commandResult, error) {
		t.Fatal("global discovery must never run an installer or command")
		return commandResult{}, nil
	}
	result, err := value.ResolveGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if result.Available || !result.ManagedRecommended || result.Path != "" {
		t.Fatalf("missing global result=%#v", result)
	}

	path := testExecutable(t, "codegraph-global")
	value.lookPath = func(string) (string, error) { return path, nil }
	result, err = value.ResolveGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || result.Path != path || result.ManagedRecommended {
		t.Fatalf("global result=%#v", result)
	}
}

func TestInvalidExternalCodeGraphTargetFailsClosed(t *testing.T) {
	value := New(Options{Enabled: true, ConfiguredPath: t.TempDir(), ManagedRoot: t.TempDir()})
	resolution, err := value.Resolve()
	if err == nil || resolution.Source != ExecutableUnavailable || resolution.Path != "" || resolution.Verified {
		t.Fatalf("invalid configured resolution=%#v err=%v", resolution, err)
	}
}

func TestDisabledAndUnsupportedCodeGraphDegradeCleanly(t *testing.T) {
	disabled := New(Options{Enabled: false})
	status, err := disabled.Status()
	if err != nil || status.Enabled || status.Resolution.Source != ExecutableDisabled {
		t.Fatalf("disabled status=%#v err=%v", status, err)
	}
	if _, err := disabled.Probe(context.Background()); err == nil {
		t.Fatal("disabled probe unexpectedly succeeded")
	}

	unsupported := New(Options{Enabled: true, ManagedRoot: t.TempDir()})
	unsupported.goos, unsupported.goarch = "freebsd", "amd64"
	unsupported.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	status, err = unsupported.Status()
	if err != nil || status.ManagedSupported || status.Resolution.Source != ExecutableUnavailable {
		t.Fatalf("unsupported status=%#v err=%v", status, err)
	}
	if _, err := unsupported.Install(context.Background()); err == nil {
		t.Fatal("unsupported managed install unexpectedly succeeded")
	}
}

func TestManagedIntegrityFailureIsNeverResolvableOrExecutable(t *testing.T) {
	value, path := installTestManagedCodeGraph(t)
	value.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if err := os.WriteFile(path, []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	resolution, err := value.Resolve()
	if err != nil || resolution.Source != ExecutableUnavailable || resolution.Path != "" || resolution.Verified {
		t.Fatalf("tampered resolution=%#v err=%v", resolution, err)
	}
	if _, err := value.Probe(context.Background()); err == nil {
		t.Fatal("tampered managed asset probe unexpectedly succeeded")
	}
}

func TestManagedAuxiliaryFileTamperInvalidatesWholeTree(t *testing.T) {
	value, path := installTestManagedCodeGraph(t)
	value.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	root := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	auxiliary := filepath.Join(root, "codegraph-linux-x64", "lib", "dist", "bin", "codegraph.js")
	if err := os.WriteFile(auxiliary, []byte("tampered-js"), 0644); err != nil {
		t.Fatal(err)
	}
	resolution, err := value.Resolve()
	if err != nil || resolution.Source != ExecutableUnavailable || resolution.Verified {
		t.Fatalf("auxiliary tamper resolution=%#v err=%v", resolution, err)
	}
}

func TestManagedChecksumMismatchNeverActivates(t *testing.T) {
	archive := codegraphTestZip(t)
	spec, asset := codegraphTestSpec(archive)
	spec.SHA256 = strings.Repeat("0", 64)
	runtime := &Runtime{
		enabled: true, goos: "linux", goarch: "amd64", managedRoot: t.TempDir(),
		httpClient: bytesClient(archive), lookPath: func(string) (string, error) { return "", errors.New("missing") },
		run: runCommand, specFor: func(string, string) (managedasset.TreeSpec, Asset, bool) { return spec, asset, true },
	}
	if _, err := runtime.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("install err=%v", err)
	}
	resolution, err := runtime.Resolve()
	if err != nil || resolution.Source != ExecutableUnavailable {
		t.Fatalf("post-failure resolution=%#v err=%v", resolution, err)
	}
}

func TestProbeIsBoundedAndTyped(t *testing.T) {
	path := testExecutable(t, "codegraph")
	runtime := New(Options{Enabled: true, ConfiguredPath: path})
	runtime.run = func(_ context.Context, got string, args []string, limit int) (commandResult, error) {
		if got != path || !reflect.DeepEqual(args, []string{"--version"}) || limit != ProbeOutputLimit {
			t.Fatalf("run path=%q args=%v limit=%d", got, args, limit)
		}
		return commandResult{Stdout: "1.6.0\n"}, nil
	}
	result, err := runtime.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != path || result.Version != "1.6.0" || result.Status.Resolution.Source != ExecutableConfigured {
		t.Fatalf("probe=%#v", result)
	}
}

func TestExecuteRequiresExplicitBoundedWorkspaceDirectory(t *testing.T) {
	path := testExecutable(t, "codegraph")
	value := New(Options{Enabled: true, ConfiguredPath: path})
	for _, test := range []struct {
		name    string
		dir     string
		timeout time.Duration
		limit   int
	}{
		{name: "empty directory", dir: "", timeout: time.Second, limit: 1024},
		{name: "relative directory", dir: ".", timeout: time.Second, limit: 1024},
		{name: "zero timeout", dir: t.TempDir(), timeout: 0, limit: 1024},
		{name: "unbounded output", dir: t.TempDir(), timeout: time.Second, limit: MaxOutputBytes + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := value.ExecuteInDir(context.Background(), test.dir, []string{"status"}, test.timeout, test.limit); err == nil {
				t.Fatal("unsafe execution unexpectedly accepted")
			}
		})
	}
}

func TestSafeEnvironmentDoesNotForwardUnrelatedSecrets(t *testing.T) {
	t.Setenv("CODEMCP_CODEGRAPH_SECRET_TEST", "do-not-forward")
	t.Setenv("HOME", t.TempDir())
	for _, entry := range safeEnvironment() {
		if strings.HasPrefix(entry, "CODEMCP_CODEGRAPH_SECRET_TEST=") {
			t.Fatalf("secret-like environment forwarded: %q", entry)
		}
	}
}

func installTestManagedCodeGraph(t *testing.T) (*Runtime, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Linux managed CodeGraph fixture requires POSIX mode semantics")
	}
	archive := codegraphTestZip(t)
	spec, asset := codegraphTestSpec(archive)
	value := &Runtime{
		enabled: true, goos: "linux", goarch: "amd64", managedRoot: t.TempDir(),
		httpClient: bytesClient(archive), lookPath: func(string) (string, error) { return "", errors.New("missing") },
		run: runCommand, specFor: func(string, string) (managedasset.TreeSpec, Asset, bool) { return spec, asset, true },
	}
	result, err := value.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Installed || result.Path == "" {
		t.Fatalf("install=%#v", result)
	}
	return value, result.Path
}

func codegraphTestSpec(archive []byte) (managedasset.TreeSpec, Asset) {
	sum := sha256.Sum256(archive)
	spec := managedasset.TreeSpec{
		Name: "codegraph", Version: "vtest", Platform: "linux-amd64", URL: "https://example.com/codegraph.zip",
		SHA256: hex.EncodeToString(sum[:]), Archive: "zip", Entrypoint: "codegraph-linux-x64/bin/codegraph",
		Required: []string{"codegraph-linux-x64/node", "codegraph-linux-x64/lib/dist/bin/codegraph.js", "codegraph-linux-x64/bin/codegraph"},
	}
	return spec, Asset{Target: "linux-x64", Entrypoint: spec.Entrypoint, Required: append([]string(nil), spec.Required...)}
}

func codegraphTestZip(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for path, content := range map[string]string{
		"codegraph-linux-x64/node":                      "node",
		"codegraph-linux-x64/lib/dist/bin/codegraph.js": "app",
		"codegraph-linux-x64/bin/codegraph":             "#!/bin/sh",
	} {
		header := &zip.FileHeader{Name: path, Method: zip.Store}
		header.SetMode(0755)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func bytesClient(data []byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
	})}
}

func testExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("codegraph-test"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
