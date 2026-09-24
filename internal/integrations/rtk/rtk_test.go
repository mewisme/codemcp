package rtk

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestPlatformMetadataMatchesPinnedRelease(t *testing.T) {
	cases := map[string]string{
		"linux/amd64":   "7278231dfd7e6a730a4ab7f847b195bcf02289c2d57622b0dab75a6411100c8f",
		"linux/arm64":   "c8ea4b6560841e73157c134fd4a3293914c6ede42e786ee985cf491fde691ba7",
		"darwin/amd64":  "d297388f4a8a786e79abe5f55b80451725bfe8c5835b4736c05d7cff4d68f627",
		"darwin/arm64":  "bbbfebabb22686993a80da731aa4d5d35116fb8ae24abb00608efa028e13ae01",
		"windows/amd64": "cb971046598f0e8bd51f6c27780fcdd2c39a4c459a811bd95b0d77ba8c0d7c9f",
	}
	if Version != "v0.49.0" {
		t.Fatalf("version=%q", Version)
	}
	for key, sha := range cases {
		parts := strings.Split(key, "/")
		platform, ok := PlatformFor(parts[0], parts[1])
		if !ok {
			t.Fatalf("platform %s unavailable", key)
		}
		if platform.Portable.SHA256 != sha || !strings.Contains(platform.Portable.URL, "/v0.49.0/") || platform.Portable.Entrypoint == "" || len(platform.Install) == 0 {
			t.Fatalf("platform %s=%#v", key, platform)
		}
		if err := validatePortable(platform.Portable); err != nil {
			t.Fatalf("platform %s invalid: %v", key, err)
		}
	}
	if _, ok := PlatformFor("plan9", "amd64"); ok {
		t.Fatal("unsupported platform accepted")
	}
}

func TestResolutionPriorityConfiguredSystemManaged(t *testing.T) {
	manager, managedPath := installTestManagedRTK(t)
	systemPath := testExecutable(t, "system-rtk")
	configuredPath := testExecutable(t, "configured-rtk")

	manager.lookPath = func(string) (string, error) { return systemPath, nil }
	resolution, err := manager.Resolve()
	if err != nil || resolution.Source != SourceSystem || resolution.Path != systemPath || !resolution.Verified {
		t.Fatalf("system resolution=%#v err=%v", resolution, err)
	}

	manager.configuredPath = configuredPath
	resolution, err = manager.Resolve()
	if err != nil || resolution.Source != SourceConfigured || resolution.Path != configuredPath || !resolution.Verified {
		t.Fatalf("configured resolution=%#v err=%v", resolution, err)
	}

	manager.configuredPath = ""
	manager.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	resolution, err = manager.Resolve()
	if err != nil || resolution.Source != SourceManaged || resolution.Path != managedPath || !resolution.Verified {
		t.Fatalf("managed resolution=%#v err=%v", resolution, err)
	}
}

func TestStatusUsesSameModelForSystemAndManaged(t *testing.T) {
	manager, managedPath := installTestManagedRTK(t)
	manager.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	managed, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	if managed.Source != SourceManaged || managed.Path != managedPath || !managed.ManagedInstalled || !managed.Verified || managed.Version != Version {
		t.Fatalf("managed status=%#v", managed)
	}

	systemPath := testExecutable(t, "system-rtk")
	manager.lookPath = func(string) (string, error) { return systemPath, nil }
	system, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	if system.Source != SourceSystem || system.Path != systemPath || !system.ManagedInstalled || !system.Verified || system.Version != managed.Version || system.Platform != managed.Platform {
		t.Fatalf("system status=%#v managed=%#v", system, managed)
	}
}

func TestTamperedManagedAssetIsNeverResolved(t *testing.T) {
	manager, path := installTestManagedRTK(t)
	manager.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if err := os.WriteFile(path, []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	resolution, err := manager.Resolve()
	if err == nil || resolution.Source != SourceUnavailable || resolution.Path != "" || resolution.Verified {
		t.Fatalf("tampered resolution=%#v err=%v", resolution, err)
	}
	manager.run = func(context.Context, string, ...string) (runResult, error) {
		t.Fatal("tampered managed RTK was executed")
		return runResult{}, nil
	}
	if _, err := manager.Probe(context.Background()); err == nil {
		t.Fatal("tampered managed RTK probe unexpectedly succeeded")
	}
}

func TestSignatureMetadataMustVerifyBeforeActivation(t *testing.T) {
	key := runtime.GOOS + "/" + runtime.GOARCH
	original, ok := platforms[key]
	if !ok {
		t.Skipf("unsupported platform %s", key)
	}
	archive := testArchive(t, original.Portable.Archive, original.Portable.Entrypoint)
	sum := sha256.Sum256(archive)
	modified := original
	modified.Portable.URL = "https://example.com/rtk-test"
	modified.Portable.SHA256 = hex.EncodeToString(sum[:])
	modified.Portable.Signature = &Signature{Kind: "test", URL: "https://example.com/rtk-test.sig", Identity: "release"}
	platforms[key] = modified
	defer func() { platforms[key] = original }()

	root := t.TempDir()
	manager := New(Options{Enabled: true, ManagedRoot: root, HTTPClient: bytesClient(archive)})
	if _, err := manager.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "no signature verifier") {
		t.Fatalf("missing verifier err=%v", err)
	}
	if _, err := os.Stat(manager.managedPath(modified)); !os.IsNotExist(err) {
		t.Fatalf("unsigned asset activated: %v", err)
	}

	manager.signatureVerifier = func(context.Context, string, Signature, *http.Client) error {
		return errors.New("signature rejected")
	}
	if _, err := manager.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "signature rejected") {
		t.Fatalf("rejected signature err=%v", err)
	}
	if _, err := os.Stat(manager.managedPath(modified)); !os.IsNotExist(err) {
		t.Fatalf("signature-rejected asset activated: %v", err)
	}

	called := false
	manager.signatureVerifier = func(_ context.Context, path string, signature Signature, _ *http.Client) error {
		called = true
		if signature.Kind != "test" || signature.Identity != "release" {
			t.Fatalf("signature=%#v", signature)
		}
		_, err := os.Stat(path)
		return err
	}
	result, err := manager.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !called || !result.Installed {
		t.Fatalf("result=%#v called=%t", result, called)
	}
}

func TestChecksumMismatchNeverActivatesManagedAsset(t *testing.T) {
	key := runtime.GOOS + "/" + runtime.GOARCH
	original, ok := platforms[key]
	if !ok {
		t.Skipf("unsupported platform %s", key)
	}
	archive := testArchive(t, original.Portable.Archive, original.Portable.Entrypoint)
	modified := original
	modified.Portable.URL = "https://example.com/rtk-test"
	modified.Portable.SHA256 = strings.Repeat("0", 64)
	platforms[key] = modified
	defer func() { platforms[key] = original }()

	manager := New(Options{Enabled: true, ManagedRoot: t.TempDir(), HTTPClient: bytesClient(archive)})
	if _, err := manager.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum err=%v", err)
	}
	if _, err := os.Stat(manager.managedPath(modified)); !os.IsNotExist(err) {
		t.Fatalf("checksum-failed asset activated: %v", err)
	}
}

func TestConfiguredPathFailureDoesNotSilentlyFallBack(t *testing.T) {
	systemPath := testExecutable(t, "system-rtk")
	manager := New(Options{Enabled: true, ConfiguredPath: filepath.Join(t.TempDir(), "missing")})
	manager.lookPath = func(string) (string, error) { return systemPath, nil }
	resolution, err := manager.Resolve()
	if err == nil || resolution.Source != SourceUnavailable {
		t.Fatalf("resolution=%#v err=%v", resolution, err)
	}
}

func TestRewriteUsesResolvedRTKAndPreservesRequestedCommand(t *testing.T) {
	path := testExecutable(t, "rtk")
	manager := New(Options{Enabled: true, ConfiguredPath: path})
	manager.run = func(_ context.Context, got string, args ...string) (runResult, error) {
		if got != path || len(args) != 2 || args[0] != "rewrite" || args[1] != "git status --short" {
			t.Fatalf("run path=%q args=%v", got, args)
		}
		return runResult{Stdout: "rtk git status --short\n"}, nil
	}
	result, err := manager.Rewrite(context.Background(), " git status --short ")
	if err != nil {
		t.Fatal(err)
	}
	if result.Requested != "git status --short" || result.Effective != "rtk git status --short" || result.Executable != path || !result.Rewritten {
		t.Fatalf("rewrite=%#v", result)
	}
}

func TestRewriteDeclineLeavesCommandUnchanged(t *testing.T) {
	path := testExecutable(t, "rtk")
	for _, exitCode := range []int{1, 2} {
		manager := New(Options{Enabled: true, ConfiguredPath: path})
		manager.run = func(context.Context, string, ...string) (runResult, error) {
			return runResult{ExitCode: exitCode}, nil
		}
		result, err := manager.Rewrite(context.Background(), "printf unchanged")
		if err != nil {
			t.Fatalf("exit=%d err=%v", exitCode, err)
		}
		if result.Rewritten || result.Requested != "printf unchanged" || result.Effective != "printf unchanged" || result.Executable != "" {
			t.Fatalf("exit=%d rewrite=%#v", exitCode, result)
		}
	}
}

func TestRewriteFailureFailsClosed(t *testing.T) {
	path := testExecutable(t, "rtk")
	manager := New(Options{Enabled: true, ConfiguredPath: path})
	manager.run = func(context.Context, string, ...string) (runResult, error) {
		return runResult{ExitCode: 9, Stderr: "rewrite failed"}, nil
	}
	if _, err := manager.Rewrite(context.Background(), "git status"); err == nil || !strings.Contains(err.Error(), "rewrite failed") {
		t.Fatalf("err=%v", err)
	}

	manager.run = func(context.Context, string, ...string) (runResult, error) {
		return runResult{Stdout: "git status --short\n"}, nil
	}
	if _, err := manager.Rewrite(context.Background(), "git status"); err == nil || !strings.Contains(err.Error(), "not routed through rtk") {
		t.Fatalf("malformed rewrite err=%v", err)
	}
}

func TestSafeEnvironmentDoesNotForwardUnrelatedSecrets(t *testing.T) {
	t.Setenv("CODEMCP_RTK_SECRET_TEST", "do-not-forward")
	t.Setenv("HOME", t.TempDir())
	values := safeEnvironment()
	for _, value := range values {
		if strings.HasPrefix(value, "CODEMCP_RTK_SECRET_TEST=") {
			t.Fatalf("secret-like environment forwarded: %q", value)
		}
	}
	foundHome := false
	for _, value := range values {
		if strings.HasPrefix(strings.ToUpper(value), "HOME=") {
			foundHome = true
		}
	}
	if !foundHome {
		t.Fatalf("safe environment=%v", values)
	}
}

func TestProbeReturnsTypedResult(t *testing.T) {
	path := testExecutable(t, "rtk")
	manager := New(Options{Enabled: true, ConfiguredPath: path})
	manager.run = func(_ context.Context, got string, args ...string) (runResult, error) {
		if got != path || len(args) != 1 || args[0] != "--version" {
			t.Fatalf("run path=%q args=%v", got, args)
		}
		return runResult{Stdout: "rtk 0.49.0\n"}, nil
	}
	result, err := manager.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != path || result.Version != "rtk 0.49.0" || result.Status.Source != SourceConfigured || !result.Status.Verified {
		t.Fatalf("probe=%#v", result)
	}
}

func TestDefaultManagedRootUsesCMConfigRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	if got, want := DefaultManagedRoot(), filepath.Join(root, "managed-assets"); got != want {
		t.Fatalf("root=%q want=%q", got, want)
	}
}

func installTestManagedRTK(t *testing.T) (*Manager, string) {
	t.Helper()
	key := runtime.GOOS + "/" + runtime.GOARCH
	original, ok := platforms[key]
	if !ok {
		t.Skipf("unsupported platform %s", key)
	}
	archive := testArchive(t, original.Portable.Archive, original.Portable.Entrypoint)
	sum := sha256.Sum256(archive)
	modified := original
	modified.Portable.URL = "https://example.com/rtk-test"
	modified.Portable.SHA256 = hex.EncodeToString(sum[:])
	modified.Portable.Signature = nil
	platforms[key] = modified
	t.Cleanup(func() { platforms[key] = original })

	manager := New(Options{Enabled: true, ManagedRoot: t.TempDir(), HTTPClient: bytesClient(archive)})
	result, err := manager.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Installed || result.Path == "" {
		t.Fatalf("install=%#v", result)
	}
	return manager, result.Path
}

func testExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("rtk-test-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func bytesClient(data []byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Body:          io.NopCloser(bytes.NewReader(data)),
			ContentLength: int64(len(data)),
			Header:        make(http.Header),
		}, nil
	})}
}

func testArchive(t *testing.T, archiveType, entrypoint string) []byte {
	t.Helper()
	content := []byte("rtk-test-binary")
	var buffer bytes.Buffer
	switch archiveType {
	case "zip":
		writer := zip.NewWriter(&buffer)
		header := &zip.FileHeader{Name: entrypoint, Method: zip.Deflate}
		header.SetMode(0755)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	case "tar.gz":
		gzipWriter := gzip.NewWriter(&buffer)
		writer := tar.NewWriter(gzipWriter)
		if err := writer.WriteHeader(&tar.Header{Name: entrypoint, Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gzipWriter.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("archive=%q", archiveType)
	}
	return buffer.Bytes()
}
