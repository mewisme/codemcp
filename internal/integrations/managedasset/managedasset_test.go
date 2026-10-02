package managedasset

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallVerifiesArchiveAndActivatedBinary(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	payload := []byte("#!/bin/sh\necho managed\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "tool", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()

	spec := Spec{
		Name: "tool", Version: "v1", Platform: "test-platform",
		URL: server.URL + "/tool.tar.gz", SHA256: hex.EncodeToString(sum[:]),
		Archive: "tar.gz", Entrypoint: "tool",
	}
	manager := Manager{Root: t.TempDir(), HTTPClient: server.Client()}
	path, err := manager.Install(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("installed payload=%q err=%v", data, err)
	}
	if _, err := manager.Validate(spec); err != nil {
		t.Fatalf("activated asset failed validation: %v", err)
	}
	again, err := manager.Install(t.Context(), spec)
	if err != nil || again != path {
		t.Fatalf("idempotent install path=%q err=%v want=%q", again, err, path)
	}
	manifestPath := filepath.Join(filepath.Dir(path), "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestSpecValidationRejectsUnsafeIdentityAndTransport(t *testing.T) {
	base := Spec{
		Name: "tool", Version: "v1", Platform: "linux-amd64",
		URL:     "https://example.com/tool.tar.gz",
		SHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Archive: "tar.gz", Entrypoint: "tool",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	unsafe := base
	unsafe.Name = "../tool"
	if err := unsafe.Validate(); err == nil {
		t.Fatal("unsafe managed-asset name was accepted")
	}
	insecure := base
	insecure.URL = "http://example.com/tool.tar.gz"
	if err := insecure.Validate(); err == nil {
		t.Fatal("insecure managed-asset URL was accepted")
	}
}
