package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestCurrentConfigSourceUsesOnlyConfigJSON(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"config.yaml", "config.yml", "config.toml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("legacy"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source, err := SourceAt(root)
	if err != nil {
		t.Fatal(err)
	}
	if source.Exists || source.Format != configformat.JSON || source.Ext != ".json" || source.Path != filepath.Join(root, "config.json") {
		t.Fatalf("alternate config file affected current source: %#v", source)
	}

	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err = SourceAt(root)
	if err != nil {
		t.Fatal(err)
	}
	if !source.Exists || source.Format != configformat.JSON || source.Ext != ".json" || Path() != filepath.Join(root, "config.json") {
		t.Fatalf("JSON config source=%#v path=%q", source, Path())
	}
}
