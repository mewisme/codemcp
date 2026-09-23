package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "asset.tar.gz")
	checksums := filepath.Join(dir, "codemcp_checksums.txt")
	content := []byte("archive")
	if err := os.WriteFile(archive, content, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(content)
	if err := os.WriteFile(checksums, []byte(hex.EncodeToString(hash[:])+"  asset.tar.gz\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum(archive, checksums, "asset.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum(archive, checksums, "asset.tar.gz"); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyChecksumRequiresExactAsset(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "asset.tar.gz")
	checksums := filepath.Join(dir, "codemcp_checksums.txt")
	if err := os.WriteFile(archive, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checksums, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  other.tar.gz\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum(archive, checksums, "asset.tar.gz"); err == nil {
		t.Fatal("missing exact asset checksum was accepted")
	}
}
