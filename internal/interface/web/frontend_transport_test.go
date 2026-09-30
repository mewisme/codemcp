package web

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBrowserProductionFetchesStayBehindCanonicalTransportOwner(t *testing.T) {
	root := webRepositoryRoot(t)
	frontend := filepath.Join(root, "frontend", "src")
	err := filepath.WalkDir(frontend, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".ts") && !strings.HasSuffix(entry.Name(), ".tsx")) ||
			strings.Contains(entry.Name(), ".test.") {
			return nil
		}
		relative, err := filepath.Rel(frontend, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		if !strings.Contains(text, "fetch(") {
			return nil
		}
		switch relative {
		case "lib/api.ts":
			return nil
		case "mini-app/api.ts":
			if strings.Count(text, `method: "POST"`) != 1 || !strings.Contains(text, `request("/api/auth"`) ||
				strings.Contains(text, `method: "DELETE"`) || strings.Contains(text, `method: "PUT"`) ||
				strings.Contains(text, `method: "PATCH"`) {
				t.Errorf("%s adds a Mini App mutation path; only session-auth POST plus read projections are allowed", relative)
			}
			return nil
		}
		if !strings.Contains(text, "adminRequestHeaders(") {
			t.Errorf("%s uses direct fetch without canonical browser operation headers", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func webRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve web interface test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}
