package releaseverify

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	updatepkg "go.mewis.me/codemcp/internal/update"
)

func TestRepositoryReleaseContracts(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve release verifier source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if err := VerifyRepository(root, ExpectedGitHubRepository); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRepository(root, "mewisme/other"); err == nil {
		t.Fatal("mismatched GitHub repository was accepted")
	}
}

func TestReleaseWorkflowTelemetryContractRejectsDrift(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve release verifier source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	fixture := t.TempDir()
	for _, relative := range []string{
		filepath.Join(".github", "workflows", "release.yml"),
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("scripts", "verify-release-telemetry.go"),
		filepath.Join("scripts", "release-cutover", "main.go"),
	} {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(relative, "release.yml") {
			needle := "TELEMETRY_ENDPOINT: $" + "{{ vars.TELEMETRY_ENDPOINT }}"
			data = []byte(strings.ReplaceAll(string(data), needle, "TELEMETRY_ENDPOINT: ''"))
		}
		target := filepath.Join(fixture, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyReleaseWorkflows(fixture); err == nil || !strings.Contains(err.Error(), "release workflow") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyVanity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<html><head><meta name=\"go-import\" content=\"%s git %s\"></head></html>", ExpectedModulePath, ExpectedGitRemote)
	}))
	defer server.Close()
	if err := VerifyVanity(context.Background(), server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyVanityRejectsWrongRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<meta name=\"go-import\" content=\"%s git https://github.com/mewisme/other\">", ExpectedModulePath)
	}))
	defer server.Close()
	if err := VerifyVanity(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("wrong vanity repository was accepted")
	}
}

func TestVerifyDistAcceptsCanonicalArtifacts(t *testing.T) {
	root := buildDistFixture(t, false)
	if err := VerifyDist(context.Background(), root, TelemetryUnchecked); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyDistRejectsRetiredExecutableAlias(t *testing.T) {
	root := buildDistFixture(t, true)
	err := VerifyDist(context.Background(), root, TelemetryUnchecked)
	if err == nil || !strings.Contains(err.Error(), "retired executable alias") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyDistRejectsVersionedPublishedArchive(t *testing.T) {
	root := buildDistFixture(t, false)
	if err := os.WriteFile(filepath.Join(root, "codemcp_9.9.9_linux_amd64.tar.gz"), []byte("stray"), 0644); err != nil {
		t.Fatal(err)
	}
	err := VerifyDist(context.Background(), root, TelemetryUnchecked)
	if err == nil || !strings.Contains(err.Error(), "unexpected published release archive") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyDistIgnoresInternalBuildArchiveNames(t *testing.T) {
	root := buildDistFixture(t, false)
	internal := filepath.Join(root, "codemcp_linux_amd64_v1")
	if err := os.MkdirAll(internal, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(internal, "codemcp_9.9.9_linux_amd64.tar.gz"), []byte("internal"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDist(context.Background(), root, TelemetryUnchecked); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyDistRejectsMissingLinuxPackage(t *testing.T) {
	root := buildDistFixture(t, false)
	if err := os.Remove(filepath.Join(root, "codemcp_linux_arm64.rpm")); err != nil {
		t.Fatal(err)
	}
	err := VerifyDist(context.Background(), root, TelemetryUnchecked)
	if err == nil || !strings.Contains(err.Error(), "codemcp_linux_arm64.rpm") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyDistRejectsVersionedLinuxPackage(t *testing.T) {
	root := buildDistFixture(t, false)
	if err := os.WriteFile(filepath.Join(root, "codemcp_9.9.9_linux_amd64.deb"), buildDebFixture(t, false), 0644); err != nil {
		t.Fatal(err)
	}
	err := VerifyDist(context.Background(), root, TelemetryUnchecked)
	if err == nil || !strings.Contains(err.Error(), "unexpected published linux package") {
		t.Fatalf("error = %v", err)
	}
}

func buildDistFixture(t *testing.T, includeRetired bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "metadata.json"), []byte("{\"project_name\":\"codemcp\",\"tag\":\"v9.9.9\",\"version\":\"1.2.3-next\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, platform := range updatepkg.PrimaryReleaseLayout().Platforms {
		asset, err := updatepkg.ArchiveName(platform.OS, platform.Arch)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, asset)
		retired := includeRetired && platform.OS == "linux" && platform.Arch == "amd64"
		if platform.ArchiveExtension == ".zip" {
			writeZipFixture(t, path, platform.BinaryName, retired)
		} else {
			writeTarFixture(t, path, platform.BinaryName, retired)
		}
	}
	fixtureBinary := []byte("#!/bin/sh\nprintf 'cm version 9.9.9 (fixture) fixture\\n'\n")
	for _, arch := range []string{"amd64", "arm64"} {
		debName, err := updatepkg.ArtifactName(updatepkg.ArtifactDebian, "linux", arch)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, debName), buildDebFixtureForArch(t, arch, false, fixtureBinary), 0644); err != nil {
			t.Fatal(err)
		}
		rpmArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[arch]
		rpmName, err := updatepkg.ArtifactName(updatepkg.ArtifactRPM, "linux", arch)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rpmName), buildRPMFixtureForArch(t, rpmArch, false, fixtureBinary), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "scoop"), 0755); err != nil {
		t.Fatal(err)
	}
	scoop := "{\"version\":\"9.9.9\",\"bin\":\"cm.exe\",\"url\":\"https://github.com/mewisme/codemcp/releases/download/v9.9.9/codemcp_windows_amd64.zip\"}"
	if err := os.WriteFile(filepath.Join(root, "scoop", "codemcp.json"), []byte(scoop), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "homebrew", "Casks"), 0755); err != nil {
		t.Fatal(err)
	}
	cask := "cask \"codemcp\" do\n  version \"9.9.9\"\n  url \"https://github.com/mewisme/codemcp/releases/download/v9.9.9/codemcp_darwin_amd64.tar.gz\"\n  homepage \"https://github.com/mewisme/codemcp\"\n  binary \"cm\"\nend\n"
	if err := os.WriteFile(filepath.Join(root, "homebrew", "Casks", "codemcp.rb"), []byte(cask), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeTarFixture(t *testing.T, path, binaryName string, includeRetired bool) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	writeEntry := func(name string) {
		content := []byte("fixture-binary")
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0755, Typeflag: tar.TypeReg, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	writeEntry(binaryName)
	if includeRetired {
		writeEntry("cgm")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZipFixture(t *testing.T, path, binaryName string, includeRetired bool) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	writeEntry := func(name string) {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0755)
		stream, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write([]byte("fixture-binary")); err != nil {
			t.Fatal(err)
		}
	}
	writeEntry(binaryName)
	if includeRetired {
		writeEntry("cgm.exe")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
