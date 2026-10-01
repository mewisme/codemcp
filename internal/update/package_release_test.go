package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func currentLinuxPackageFixture(t *testing.T, kind ArtifactKind) string {
	t.Helper()
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("native Linux package contract is only defined for linux/amd64 and linux/arm64")
	}
	name, err := ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestClientPackageVersionResolvesExactTaggedPackage(t *testing.T) {
	packageName := currentLinuxPackageFixture(t, ArtifactDebian)
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprintf(w, `{"tag_name":"v1.2.3","draft":false,"assets":[{"name":%q,"browser_download_url":"https://example.test/package"},{"name":"%s","browser_download_url":"https://example.test/checksums"},{"name":"%s","browser_download_url":"https://example.test/signature"}]}`, packageName, ChecksumName, ChecksumSignatureName)
	}))
	defer server.Close()

	release, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).PackageVersion(context.Background(), "1.2.3", ArtifactDebian)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/mewisme/codemcp/releases/tags/v1.2.3" {
		t.Fatalf("request path = %q", gotPath)
	}
	if release.Version != "v1.2.3" || release.Kind != ArtifactDebian || release.PackageName != packageName ||
		release.PackageURL != "https://example.test/package" || release.ChecksumURL != "https://example.test/checksums" ||
		release.SignatureURL != "https://example.test/signature" {
		t.Fatalf("release = %#v", release)
	}
}

func TestClientPackageVersionRejectsTagMismatchAndMissingAssets(t *testing.T) {
	packageName := currentLinuxPackageFixture(t, ArtifactRPM)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("case") {
		default:
			fmt.Fprintf(w, `{"tag_name":"v1.2.4","assets":[{"name":%q,"browser_download_url":"https://example.test/package"},{"name":"%s","browser_download_url":"https://example.test/checksums"}]}`, packageName, ChecksumName)
		}
	}))
	defer server.Close()
	if _, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).PackageVersion(context.Background(), "v1.2.3", ArtifactRPM); err == nil || !strings.Contains(err.Error(), "tag mismatch") {
		t.Fatalf("tag mismatch error = %v", err)
	}

	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.2.3","assets":[]}`)
	}))
	defer missing.Close()
	if _, err := (Client{BaseURL: missing.URL, HTTPClient: missing.Client()}).PackageVersion(context.Background(), "v1.2.3", ArtifactRPM); err == nil || !strings.Contains(err.Error(), packageName) {
		t.Fatalf("missing package error = %v", err)
	}

	missingChecksum := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":%q,"browser_download_url":"https://example.test/package"}]}`, packageName)
	}))
	defer missingChecksum.Close()
	if _, err := (Client{BaseURL: missingChecksum.URL, HTTPClient: missingChecksum.Client()}).PackageVersion(context.Background(), "v1.2.3", ArtifactRPM); err == nil || !strings.Contains(err.Error(), ChecksumName) {
		t.Fatalf("missing checksum error = %v", err)
	}
}

func TestDownloaderDownloadPackageUsesMandatoryChecksumBeforeOptionalSignature(t *testing.T) {
	packageName := currentLinuxPackageFixture(t, ArtifactDebian)
	payload := []byte("native-package")
	hash := sha256.Sum256(payload)
	checksums := []byte(hex.EncodeToString(hash[:]) + "  " + packageName + "\n")
	requests := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/" + ChecksumName:
			_, _ = w.Write(checksums)
		case "/" + packageName:
			_, _ = w.Write(payload)
		case "/" + ChecksumSignatureName:
			_, _ = w.Write([]byte("signature"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	release := PackageRelease{
		Version: "v1.2.3", Kind: ArtifactDebian, PackageName: packageName, PackageURL: server.URL + "/" + packageName,
		ChecksumName: ChecksumName, ChecksumURL: server.URL + "/" + ChecksumName,
		SignatureName: ChecksumSignatureName, SignatureURL: server.URL + "/" + ChecksumSignatureName,
	}
	verified := false
	artifact, err := (Downloader{HTTPClient: server.Client(), TempDir: t.TempDir(), SignatureVerifier: func(_ context.Context, checksumPath, signaturePath, version string) error {
		verified = true
		if version != release.Version || filepath.Base(checksumPath) != ChecksumName || filepath.Base(signaturePath) != ChecksumSignatureName {
			t.Fatalf("signature inputs = %q %q %q", checksumPath, signaturePath, version)
		}
		return nil
	}}).DownloadPackage(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Cleanup()
	if !verified {
		t.Fatal("optional signature verifier was not called after checksum")
	}
	got, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("package payload = %q", got)
	}
	wantOrder := []string{"/" + ChecksumName, "/" + packageName, "/" + ChecksumSignatureName}
	if strings.Join(requests, "|") != strings.Join(wantOrder, "|") {
		t.Fatalf("request order = %#v, want %#v", requests, wantOrder)
	}
}

func TestDownloaderDownloadPackageChecksumFailurePreventsSignatureAndCleansUp(t *testing.T) {
	packageName := currentLinuxPackageFixture(t, ArtifactRPM)
	parent := t.TempDir()
	signatureRequested := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + ChecksumName:
			fmt.Fprintf(w, "%064d  %s\n", 0, packageName)
		case "/" + packageName:
			_, _ = w.Write([]byte("native-package"))
		case "/" + ChecksumSignatureName:
			signatureRequested = true
			_, _ = w.Write([]byte("signature"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	release := PackageRelease{
		Version: "v1.2.3", Kind: ArtifactRPM, PackageName: packageName, PackageURL: server.URL + "/" + packageName,
		ChecksumName: ChecksumName, ChecksumURL: server.URL + "/" + ChecksumName,
		SignatureName: ChecksumSignatureName, SignatureURL: server.URL + "/" + ChecksumSignatureName,
	}
	verifierCalled := false
	if _, err := (Downloader{HTTPClient: server.Client(), TempDir: parent, SignatureVerifier: func(context.Context, string, string, string) error {
		verifierCalled = true
		return nil
	}}).DownloadPackage(context.Background(), release); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	if signatureRequested || verifierCalled {
		t.Fatal("optional signature verification ran before mandatory checksum succeeded")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed package download left temporary files: %v", entries)
	}
}

func TestDownloaderDownloadPackageMissingSignatureKeepsChecksumMandatory(t *testing.T) {
	packageName := currentLinuxPackageFixture(t, ArtifactDebian)
	payload := []byte("native-package")
	hash := sha256.Sum256(payload)
	checksums := []byte(hex.EncodeToString(hash[:]) + "  " + packageName + "\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + ChecksumName:
			_, _ = w.Write(checksums)
		case "/" + packageName:
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	release := PackageRelease{
		Version: "v1.2.3", Kind: ArtifactDebian, PackageName: packageName, PackageURL: server.URL + "/" + packageName,
		ChecksumName: ChecksumName, ChecksumURL: server.URL + "/" + ChecksumName,
		SignatureName: ChecksumSignatureName,
	}
	artifact, err := (Downloader{HTTPClient: server.Client(), TempDir: t.TempDir()}).DownloadPackage(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Cleanup()
	if len(artifact.Warnings) != 1 || !strings.Contains(artifact.Warnings[0], "not published") || !strings.Contains(artifact.Warnings[0], "SHA-256 checksum verified") {
		t.Fatalf("warnings = %#v", artifact.Warnings)
	}
}
