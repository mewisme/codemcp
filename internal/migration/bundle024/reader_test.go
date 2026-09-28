package bundle024

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectReleasedPortableBundleIsBoundedAndSecretSafe(t *testing.T) {
	secret := "bundle-secret-value"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	value := bundle{
		Version: Version, CreatedAt: created, Source: Platform{OS: "linux", Arch: "amd64", Home: "/home/legacy"},
		Files: []file{
			{Path: "config.json", Data: []byte(`{"server":{}}`)},
			{Path: "workspaces/ws_one/MEMORY.md", Data: []byte("memory")},
		},
		Secrets: map[string]string{"tunnel/runtime-key": secret},
	}
	encoded := encodeFixture(t, value)
	path := filepath.Join(t.TempDir(), "released.cgm")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	inspection, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SourceRelease != SourceRelease || inspection.Version != Version || inspection.CreatedAt != created ||
		inspection.SourceOS != "linux" || inspection.SourceArch != "amd64" || inspection.FileCount != 2 ||
		inspection.SecretCount != 1 || inspection.FilesBytes == 0 {
		t.Fatalf("inspection=%#v", inspection)
	}
	encodedInspection, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedInspection), secret) || strings.Contains(string(encodedInspection), "/home/legacy") {
		t.Fatalf("bundle inspection leaked source secret/home: %s", encodedInspection)
	}
}

func TestInspectReleasedPortableBundleRejectsUnsafePaths(t *testing.T) {
	for _, unsafe := range []string{"../escape", "/absolute", `C:\\escape`} {
		value := bundle{
			Version: Version, CreatedAt: time.Now().UTC(), Source: Platform{OS: "linux"},
			Files: []file{{Path: unsafe, Data: []byte("x")}},
		}
		path := filepath.Join(t.TempDir(), "unsafe.cgm")
		if err := os.WriteFile(path, encodeFixture(t, value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Inspect(path); err == nil || !strings.Contains(err.Error(), "unsafe path") {
			t.Fatalf("unsafe bundle path=%q err=%v", unsafe, err)
		}
	}
}

func encodeFixture(t *testing.T, value bundle) []byte {
	t.Helper()
	plain, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	if _, err := zipper.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	aead, err := bundleAEAD()
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	sealed := aead.Seal(nil, nonce, compressed.Bytes(), []byte(magic))
	result := append([]byte(magic), nonce...)
	return append(result, sealed...)
}
