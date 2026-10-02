package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/install"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type fakeResolver struct {
	latest   Release
	versions map[string]Release
}

func (f fakeResolver) Latest(context.Context) (Release, error) { return f.latest, nil }
func (f fakeResolver) Version(_ context.Context, version string) (Release, error) {
	release, ok := f.versions[version]
	if !ok {
		return Release{}, errors.New("release not found")
	}
	return release, nil
}

type failingResolver struct{}

func (failingResolver) Latest(context.Context) (Release, error) {
	return Release{}, errors.New("unexpected latest resolution")
}
func (failingResolver) Version(context.Context, string) (Release, error) {
	return Release{}, errors.New("unexpected version resolution")
}

type fakeArtifactSource struct {
	binary string
	calls  *int
}

func (f fakeArtifactSource) Download(context.Context, Release) (Artifact, error) {
	(*f.calls)++
	return Artifact{Dir: filepath.Dir(f.binary), Binary: f.binary}, nil
}

func TestUpdaterApplyLatest(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old")
	binary, artifactDir := updateTestBinary(t, "new")
	calls := 0
	updater := Updater{Resolver: fakeResolver{latest: Release{Version: "v1.1.0"}}, Downloader: fakeArtifactSource{binary: binary, calls: &calls}}
	result, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Target != "v1.1.0" || result.Downgrade || calls != 1 {
		t.Fatalf("result = %+v, calls = %d", result, calls)
	}
	version, _, err := install.CurrentVersion(layout)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.1.0" {
		t.Fatalf("current version = %q", version)
	}
	if _, err := os.Stat(filepath.Join(layout.Versions, "v1.0.0")); err != nil {
		t.Fatalf("previous version was removed: %v", err)
	}
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Fatalf("artifact directory was not cleaned up: %v", err)
	}
}

func TestUpdaterResolvePlansUpdateWithoutDownloading(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old")
	calls := 0
	updater := Updater{Resolver: fakeResolver{latest: Release{Version: "v1.1.0"}}, Downloader: fakeArtifactSource{calls: &calls}}
	result, err := updater.Resolve(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Current != "v1.0.0" || result.Target != "v1.1.0" || result.Downgrade || calls != 0 {
		t.Fatalf("result = %+v, calls = %d", result, calls)
	}
}

func TestUpdaterApplyUsesResolvedReleaseWithoutResolvingAgain(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old")
	binary, _ := updateTestBinary(t, "new")
	calls := 0
	release := Release{Version: "v1.1.0"}
	updater := Updater{Resolver: failingResolver{}, Downloader: fakeArtifactSource{binary: binary, calls: &calls}}
	result, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0", ResolvedRelease: &release})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Target != "v1.1.0" || calls != 1 {
		t.Fatalf("result = %+v, calls = %d", result, calls)
	}
}

func TestUpdaterNoopsWhenLatestIsNotNewer(t *testing.T) {
	for _, latest := range []string{"v1.0.0", "v0.9.0"} {
		t.Run(latest, func(t *testing.T) {
			layout := updateTestLayout(t)
			installCurrentVersion(t, layout, "v1.0.0", "current")
			calls := 0
			updater := Updater{Resolver: fakeResolver{latest: Release{Version: latest}}, Downloader: fakeArtifactSource{calls: &calls}}
			result, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Changed || calls != 0 {
				t.Fatalf("result = %+v, calls = %d", result, calls)
			}
		})
	}
}

func TestUpdaterExplicitVersionAllowsDowngrade(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.2.0", "old")
	binary, _ := updateTestBinary(t, "downgrade")
	calls := 0
	resolver := fakeResolver{versions: map[string]Release{"v1.1.0": {Version: "v1.1.0"}}}
	result, err := (Updater{Resolver: resolver, Downloader: fakeArtifactSource{binary: binary, calls: &calls}}).Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.2.0", TargetVersion: "1.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Downgrade || result.Target != "v1.1.0" || calls != 1 {
		t.Fatalf("result = %+v, calls = %d", result, calls)
	}
}

func TestUpdaterExplicitVersionRejectsResolverMismatch(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "current")
	resolver := fakeResolver{versions: map[string]Release{"v1.1.0": {Version: "v1.2.0"}}}
	if _, err := (Updater{Resolver: resolver}).Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0"}); err == nil {
		t.Fatal("resolver version mismatch was accepted")
	}
}

func TestUpdaterRefusesDevelopmentBuild(t *testing.T) {
	_, err := (Updater{Resolver: fakeResolver{}}).Apply(context.Background(), ApplyOptions{CurrentVersion: "dev"})
	if !errors.Is(err, ErrDevelopmentUpdate) {
		t.Fatalf("error = %v", err)
	}
}

func TestUpdaterRefusesStaleRunningVersion(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.1.0", "current")
	_, err := (Updater{Resolver: fakeResolver{}}).Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"})
	if !errors.Is(err, ErrCurrentVersionMismatch) {
		t.Fatalf("error = %v", err)
	}
}

func TestUpdaterDoesNotCreateHistoricalAlias(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old")
	binary, _ := updateTestBinary(t, "new")
	calls := 0
	updater := Updater{Resolver: fakeResolver{latest: Release{Version: "v1.1.0"}}, Downloader: fakeArtifactSource{binary: binary, calls: &calls}}
	if _, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(layout.BinDir, "cgm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("historical alias unexpectedly created: %v", err)
	}
}

func TestUpdaterDoesNotOverwriteUnrelatedCanonicalExecutable(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old")
	if err := os.Remove(layout.CanonicalBinary); err != nil {
		t.Fatal(err)
	}
	const unrelated = "unrelated-same-name-executable"
	if err := os.WriteFile(layout.CanonicalBinary, []byte(unrelated), 0755); err != nil {
		t.Fatal(err)
	}
	binary, _ := updateTestBinary(t, "new")
	calls := 0
	updater := Updater{Resolver: fakeResolver{latest: Release{Version: "v1.1.0"}}, Downloader: fakeArtifactSource{binary: binary, calls: &calls}}
	if _, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"}); err == nil {
		t.Fatal("direct updater accepted unrelated canonical executable")
	}
	content, err := os.ReadFile(layout.CanonicalBinary)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != unrelated {
		t.Fatalf("unrelated canonical executable changed: %q", content)
	}
}

func TestUpdaterVerifiedReleaseDownloadActivates(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old-release")
	server, release := updateReleaseFixture(t, "v1.1.0", []byte("new-release"), true)
	defer server.Close()
	updater := Updater{Resolver: fakeResolver{latest: release}, Downloader: Downloader{HTTPClient: server.Client(), TempDir: t.TempDir(), SignatureVerifier: acceptTestSignature}}
	result, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Target != "v1.1.0" {
		t.Fatalf("result = %+v", result)
	}
	version, _, err := install.CurrentVersion(layout)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.1.0" {
		t.Fatalf("current version = %q", version)
	}
	content, err := os.ReadFile(layout.CurrentBinary)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new-release" {
		t.Fatalf("current binary = %q", content)
	}
	if _, err := os.Stat(filepath.Join(layout.Versions, "v1.0.0")); err != nil {
		t.Fatalf("previous version was removed: %v", err)
	}
}

func TestUpdaterOptionalSignatureFailureStillActivatesAfterChecksum(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old-release")
	server, release := updateReleaseFixture(t, "v1.1.0", []byte("new-release"), true)
	defer server.Close()
	updater := Updater{
		Resolver: fakeResolver{latest: release},
		Downloader: Downloader{
			HTTPClient: server.Client(), TempDir: t.TempDir(),
			SignatureVerifier: func(context.Context, string, string, string) error { return errors.New("sigstore unavailable") },
		},
	}
	result, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "SHA-256 checksum verified") {
		t.Fatalf("result = %#v", result)
	}
	version, _, err := install.CurrentVersion(layout)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.1.0" {
		t.Fatalf("current version = %q", version)
	}
}

func TestUpdaterBadChecksumNeverActivates(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old-release")
	server, release := updateReleaseFixture(t, "v1.1.0", []byte("new-release"), false)
	defer server.Close()
	updater := Updater{Resolver: fakeResolver{latest: release}, Downloader: Downloader{HTTPClient: server.Client(), TempDir: t.TempDir(), SignatureVerifier: acceptTestSignature}}
	_, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v", err)
	}
	version, _, currentErr := install.CurrentVersion(layout)
	if currentErr != nil {
		t.Fatal(currentErr)
	}
	if version != "v1.0.0" {
		t.Fatalf("current version changed after checksum failure: %q", version)
	}
	content, readErr := os.ReadFile(layout.CurrentBinary)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "old-release" {
		t.Fatalf("current binary changed after checksum failure: %q", content)
	}
	if _, statErr := os.Stat(filepath.Join(layout.Versions, "v1.1.0")); !os.IsNotExist(statErr) {
		t.Fatalf("failed update staged target version: %v", statErr)
	}
}

func TestUpdaterInstallFailurePreservesCurrentInstallation(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old-release")
	binary, _ := updateTestBinary(t, "new-release")
	calls := 0
	updater := Updater{
		Resolver:   fakeResolver{latest: Release{Version: "v1.1.0"}},
		Downloader: fakeArtifactSource{binary: binary, calls: &calls},
		Install: func(options install.Options) (install.Result, error) {
			version, _, err := install.CurrentVersion(options.Layout)
			if err != nil {
				t.Fatal(err)
			}
			if version != "v1.0.0" {
				t.Fatalf("current version changed before install transaction: %q", version)
			}
			return install.Result{}, errors.New("injected install failure")
		},
	}
	if _, err := updater.Apply(context.Background(), ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"}); err == nil || !strings.Contains(err.Error(), "injected install failure") {
		t.Fatalf("error = %v", err)
	}
	version, _, err := install.CurrentVersion(layout)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.0.0" {
		t.Fatalf("current version changed after install failure: %q", version)
	}
	content, err := os.ReadFile(layout.CurrentBinary)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old-release" {
		t.Fatalf("current binary changed after install failure: %q", content)
	}
}

func TestUpdaterApplyEmitsDeepTrace(t *testing.T) {
	layout := updateTestLayout(t)
	installCurrentVersion(t, layout, "v1.0.0", "old")
	binary, _ := updateTestBinary(t, "new")
	calls := 0
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	updater := Updater{Resolver: fakeResolver{latest: Release{Version: "v1.1.0", ArchiveName: "fixture"}}, Downloader: fakeArtifactSource{binary: binary, calls: &calls}}
	if _, err := updater.Apply(ctx, ApplyOptions{Layout: layout, CurrentVersion: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"update.apply.started", "update.current.read.completed", "update.target.resolve.completed", "update.artifact.download.completed", "update.install.completed", "update.apply.completed", "update.artifact.cleanup.completed"} {
		if !containsTraceEvent(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
}

func containsTraceEvent(events []tracepkg.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func updateReleaseFixture(t *testing.T, version string, binary []byte, validChecksum bool) (*httptest.Server, Release) {
	t.Helper()
	assetName, err := CurrentArchiveName()
	if err != nil {
		t.Fatal(err)
	}
	archive := releaseArchive(t, assetName, binary)
	hash := sha256.Sum256(archive)
	checksum := hex.EncodeToString(hash[:])
	if !validChecksum {
		checksum = fmt.Sprintf("%064d", 0)
	}
	checksums := []byte(checksum + "  " + assetName + "\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(archive)
		case "/codemcp_checksums.txt":
			_, _ = w.Write(checksums)
		case "/codemcp_checksums.txt.sigstore.json":
			_, _ = w.Write([]byte("test-signature"))
		default:
			http.NotFound(w, r)
		}
	}))
	return server, testDownloadRelease(version, assetName, server.URL)
}

func updateTestLayout(t *testing.T) install.Layout {
	t.Helper()
	root := filepath.Join(t.TempDir(), "install")
	binDir := filepath.Join(root, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(root, "current")
	}
	layout, err := install.NewLayout(root, binDir)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func updateTestBinary(t *testing.T, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cm")
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

func installCurrentVersion(t *testing.T, layout install.Layout, version, content string) {
	t.Helper()
	binary, _ := updateTestBinary(t, content)
	if _, err := install.Install(install.Options{Layout: layout, Version: version, Source: binary}); err != nil {
		t.Fatal(err)
	}
}
