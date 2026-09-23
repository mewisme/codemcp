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

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestDownloaderDownload(t *testing.T) {
	assetName, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	archive := releaseArchive(t, assetName, []byte("release-binary"))
	archiveHash := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(archiveHash[:]) + "  " + assetName + "\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(archive)
		case "/checksums.txt":
			_, _ = w.Write(checksums)
		case "/checksums.txt.sigstore.json":
			_, _ = w.Write([]byte("test-signature"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	parent := t.TempDir()
	release := testDownloadRelease("v1.2.3", assetName, server.URL)
	verified := false
	artifact, err := (Downloader{HTTPClient: server.Client(), TempDir: parent, SignatureVerifier: func(_ context.Context, checksumPath, signaturePath, version string) error {
		verified = true
		if version != "v1.2.3" || filepath.Base(checksumPath) != ChecksumName || filepath.Base(signaturePath) != ChecksumSignatureName {
			t.Fatalf("unexpected signature inputs: %q %q %q", checksumPath, signaturePath, version)
		}
		return nil
	}}).Download(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("checksum signature was not verified")
	}
	content, err := os.ReadFile(artifact.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "release-binary" {
		t.Fatalf("binary = %q", content)
	}
	if !strings.HasPrefix(artifact.Dir, parent) {
		t.Fatalf("artifact dir = %q", artifact.Dir)
	}
	if err := artifact.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact.Dir); !os.IsNotExist(err) {
		t.Fatalf("artifact directory remains: %v", err)
	}
}

func TestDownloaderRejectsSignatureBeforeDownloadingArchive(t *testing.T) {
	assetName, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	archiveRequested := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + ChecksumName:
			fmt.Fprintf(w, "%064d  %s\n", 0, assetName)
		case "/" + ChecksumSignatureName:
			_, _ = w.Write([]byte("bad-signature"))
		case "/" + assetName:
			archiveRequested = true
			_, _ = w.Write([]byte("untrusted-archive"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	parent := t.TempDir()
	release := testDownloadRelease("v1.2.3", assetName, server.URL)
	_, err = (Downloader{HTTPClient: server.Client(), TempDir: parent, SignatureVerifier: func(context.Context, string, string, string) error {
		return errors.New("invalid signature")
	}}).Download(context.Background(), release)
	if err == nil || !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("error = %v", err)
	}
	if archiveRequested {
		t.Fatal("archive was downloaded before checksum signature verification")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed signature verification left temporary files: %v", entries)
	}
}

func TestDownloaderCleansUpOnChecksumMismatch(t *testing.T) {
	assetName, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	archive := releaseArchive(t, assetName, []byte("release-binary"))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ChecksumSignatureName) {
			_, _ = w.Write([]byte("test-signature"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			fmt.Fprintf(w, "%064d  %s\n", 0, assetName)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	parent := t.TempDir()
	release := testDownloadRelease("v1.2.3", assetName, server.URL)
	if _, err := (Downloader{HTTPClient: server.Client(), TempDir: parent, SignatureVerifier: acceptTestSignature}).Download(context.Background(), release); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed download left temporary files: %v", entries)
	}
}

func TestDownloaderRejectsHTTP(t *testing.T) {
	assetName, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	release := Release{Version: "v1.2.3", ArchiveName: assetName, ArchiveURL: "http://example.test/" + assetName, ChecksumName: ChecksumName, ChecksumURL: "https://example.test/checksums.txt", SignatureName: ChecksumSignatureName, SignatureURL: "https://example.test/checksums.txt.sigstore.json"}
	if _, err := (Downloader{TempDir: t.TempDir()}).Download(context.Background(), release); err == nil {
		t.Fatal("HTTP release URL was accepted")
	}
}

func TestDownloaderRejectsUnexpectedAssetName(t *testing.T) {
	release := Release{Version: "v1.2.3", ArchiveName: "unexpected.tar.gz", ArchiveURL: "https://example.test/unexpected.tar.gz", ChecksumName: ChecksumName, ChecksumURL: "https://example.test/checksums.txt", SignatureName: ChecksumSignatureName, SignatureURL: "https://example.test/checksums.txt.sigstore.json"}
	if _, err := (Downloader{TempDir: t.TempDir()}).Download(context.Background(), release); err == nil {
		t.Fatal("unexpected release asset name was accepted")
	}
}

func TestDownloaderRejectsHTTPRedirect(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("bad")) }))
	defer httpServer.Close()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, httpServer.URL, http.StatusFound) }))
	defer tlsServer.Close()
	destination := filepath.Join(t.TempDir(), "download")
	if err := (Downloader{HTTPClient: tlsServer.Client()}).downloadFile(context.Background(), tlsServer.URL, destination, 1024); err == nil {
		t.Fatal("HTTP redirect was accepted")
	}
}

func TestDownloaderEnforcesBodyLimitWithoutContentLength(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "")
		_, _ = w.Write([]byte("12345"))
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "download")
	if err := (Downloader{HTTPClient: server.Client()}).downloadFile(context.Background(), server.URL, destination, 4); err == nil {
		t.Fatal("oversized body was accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("partial download remains: %v", err)
	}
}

func TestDownloaderEmitsFileChecksumAndExtractionTrace(t *testing.T) {
	assetName, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	archive := releaseArchive(t, assetName, []byte("release-binary"))
	archiveHash := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(archiveHash[:]) + "  " + assetName + "\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(archive)
		case "/checksums.txt":
			_, _ = w.Write(checksums)
		case "/checksums.txt.sigstore.json":
			_, _ = w.Write([]byte("test-signature"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	artifact, err := (Downloader{HTTPClient: server.Client(), TempDir: t.TempDir(), SignatureVerifier: acceptTestSignature}).Download(ctx, testDownloadRelease("v1.2.3", assetName, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Cleanup()
	for _, name := range []string{"update.workspace.create.completed", "update.signature.verify.completed", "update.checksum.verify.completed", "update.archive.extract.completed", "update.artifact.prepare.completed"} {
		if !containsTraceEvent(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	downloads := 0
	responses := 0
	for _, event := range events {
		if event.Name == "update.file.download.completed" {
			downloads++
		}
		if event.Name == "http.request.completed" {
			responses++
		}
	}
	if downloads != 3 || responses != 3 {
		t.Fatalf("downloads=%d responses=%d events=%#v", downloads, responses, events)
	}
}

func releaseArchive(t *testing.T, assetName string, binary []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, assetName)
	if runtime.GOOS == "windows" {
		writeZipArchive(t, path, []zipEntry{{name: "chatgpt-mcp.exe", content: binary}})
	} else {
		writeTarArchive(t, path, []tarEntry{{name: "chatgpt-mcp", content: binary}})
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func testDownloadRelease(version, assetName, baseURL string) Release {
	return Release{
		Version: version, ArchiveName: assetName, ArchiveURL: baseURL + "/" + assetName,
		ChecksumName: ChecksumName, ChecksumURL: baseURL + "/" + ChecksumName,
		SignatureName: ChecksumSignatureName, SignatureURL: baseURL + "/" + ChecksumSignatureName,
	}
}

func acceptTestSignature(context.Context, string, string, string) error { return nil }
