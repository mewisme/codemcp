package version

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var forbiddenRepositoryIdentities = [][]byte{
	[]byte("go.mewis.me/" + "chatgpt-mcp"),
	[]byte("github.com/mewisme/" + "chatgpt-mcp"),
}

var legacyUserFacingIdentity = regexp.MustCompile(`(?i)(chatgpt-mcp|chatgpt_mcp|(^|[^a-z0-9_])(cgm|cmcp)([^a-z0-9_]|$))`)

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

func TestCurrentUserFacingSurfacesHaveNoLegacyProductIdentity(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range trackedRepositoryFiles(t, root) {
		if !isCurrentUserFacingIdentitySurface(relative) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read tracked user-facing file %s: %v", relative, err)
		}
		if match := legacyUserFacingIdentity.Find(data); len(match) > 0 {
			t.Errorf("%s contains legacy user-facing product identity %q", relative, match)
		}
	}
}

func TestFrontendSourceRootIsCanonicalAndUnique(t *testing.T) {
	root := repositoryRoot(t)
	packageRoots := []string{}
	for _, relative := range trackedRepositoryFiles(t, root) {
		if filepath.Base(relative) == "package.json" {
			packageRoots = append(packageRoots, relative)
		}
		first, _, _ := strings.Cut(relative, "/")
		switch first {
		case "web", "ui", "client", "dashboard", "admin-ui":
			t.Errorf("alternate frontend source root is tracked: %s", relative)
		}
	}
	if len(packageRoots) != 1 || packageRoots[0] != "frontend/package.json" {
		t.Fatalf("frontend package roots = %#v, want only frontend/package.json", packageRoots)
	}
}

func isLegacyIdentityAllowed(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "internal/install/legacy.go" {
		return true
	}
	return strings.Contains(path, "/testdata/migration/") || strings.HasPrefix(path, "testdata/migration/")
}

func isCurrentUserFacingIdentitySurface(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	switch path {
	case "README.md", "install.sh", "install.ps1", ".goreleaser.yaml":
		return true
	case "docs/migration-from-0.2.24.md":
		return false
	}
	if strings.HasPrefix(path, "docs/") || strings.HasPrefix(path, ".github/") ||
		strings.HasPrefix(path, "frontend/") || strings.HasPrefix(path, "installer/") {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".html", ".css", ".js", ".jsx", ".ts", ".tsx", ".json", ".yaml", ".yml", ".toml", ".sh", ".ps1", ".nsi":
			return true
		}
	}
	return false
}

func trackedRepositoryFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := []string{}
	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		files = append(files, filepath.ToSlash(string(raw)))
	}
	return files
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
