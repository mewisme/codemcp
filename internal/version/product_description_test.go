package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const canonicalProjectDescription = "CodeMCP is a workspace-aware execution, context, and agent orchestration server for AI clients."

func TestCanonicalProjectDescriptionDoesNotDrift(t *testing.T) {
	root := repositoryRoot(t)
	cases := []struct {
		path  string
		count int
	}{
		{path: "README.md", count: 1},
		{path: ".goreleaser.yaml", count: 4},
		{path: "installer/windows/codemcp.iss", count: 2},
		{path: "internal/cli/root.go", count: 1},
		{path: "internal/releaseverify/linux_packages.go", count: 1},
		{path: "frontend/package.json", count: 1},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.path)))
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if got := strings.Count(string(data), canonicalProjectDescription); got != tc.count {
			t.Errorf("%s canonical description count=%d want=%d", tc.path, got, tc.count)
		}
	}
}
