package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseWorkflowIdentity(t *testing.T) {
	want := "https://github.com/mewisme/codemcp/.github/workflows/release.yml@refs/tags/v1.2.3"
	if got := releaseWorkflowIdentity("v1.2.3"); got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
}

func TestVerifyChecksumSignatureRejectsInvalidBundleBeforeTrustLookup(t *testing.T) {
	dir := t.TempDir()
	checksum := filepath.Join(dir, ChecksumName)
	signature := filepath.Join(dir, ChecksumSignatureName)
	if err := os.WriteFile(checksum, []byte("checksum"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signature, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksumSignature(context.Background(), checksum, signature, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "load Sigstore bundle") {
		t.Fatalf("error = %v", err)
	}
}
