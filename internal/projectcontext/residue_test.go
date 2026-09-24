package projectcontext

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionProjectContextConstructionStaysCanonical(t *testing.T) {
	root := filepath.Join("..")
	allowed := map[string]bool{
		filepath.Clean(filepath.Join("application", "project_context.go")): true,
		filepath.Clean(filepath.Join("tools", "context_tools.go")):         true,
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		if strings.Contains(text, "projectcontext.New(") {
			t.Fatalf("production file %s directly constructs projectcontext.New", relative)
		}
		if strings.Contains(text, "projectcontext.NewService(") && !allowed[filepath.Clean(relative)] {
			t.Fatalf("production file %s bypasses canonical Project Context construction authority", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
