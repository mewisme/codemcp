package version

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var forbiddenRepositoryIdentities = [][]byte{
	[]byte("go.mewis.me/" + "chatgpt-mcp"),
	[]byte("github.com/mewisme/" + "chatgpt-mcp"),
}

func TestRepositoryIdentityHasNoLegacyLinksOutsideMigrationFixtures(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}

	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		relative := filepath.ToSlash(string(raw))
		if isLegacyIdentityAllowed(relative) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read tracked file %s: %v", relative, err)
		}
		for _, forbidden := range forbiddenRepositoryIdentities {
			if bytes.Contains(data, forbidden) {
				t.Errorf("%s contains legacy repository identity %q", relative, forbidden)
			}
		}
	}
}

func TestLegacyIdentityExceptionIsNarrow(t *testing.T) {
	for _, allowed := range []string{
		"internal/install/legacy.go",
		"internal/version/testdata/migration/released-v0.2.24/identity.txt",
	} {
		if !isLegacyIdentityAllowed(allowed) {
			t.Fatalf("expected migration-only identity path to be allowed: %s", allowed)
		}
	}
	for _, current := range []string{
		"internal/version/version.go",
		"internal/migration/legacy_identity.go",
		"docs/migration.md",
		"testdata/not-migration/identity.txt",
	} {
		if isLegacyIdentityAllowed(current) {
			t.Fatalf("current identity path unexpectedly allowed: %s", current)
		}
	}
}

func isLegacyIdentityAllowed(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "internal/install/legacy.go" {
		return true
	}
	return strings.Contains(path, "/testdata/migration/") || strings.HasPrefix(path, "testdata/migration/")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
