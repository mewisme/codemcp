package upstream

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestPathUsesCanonicalUpstreamsJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	if got, want := Path(), filepath.Join(root, "upstreams.json"); got != want {
		t.Fatalf("Path()=%q want=%q", got, want)
	}
}

func TestDefaultStoreDoesNotFallbackToLegacyUpstreamFilename(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	legacyPath := filepath.Join(root, "upstream.json")
	legacy := []byte(`{"version":1,"servers":[{"id":"legacy","name":"Legacy","transport":"stdio","enabled":true,"command":"node"}]}`)
	if err := os.WriteFile(legacyPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	servers, err := NewStore(Path()).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 0 {
		t.Fatalf("legacy filename was used as runtime fallback: %#v", servers)
	}
	if _, err := os.Stat(filepath.Join(root, "upstreams.json")); !os.IsNotExist(err) {
		t.Fatalf("load unexpectedly created canonical store: %v", err)
	}
	got, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(legacy) {
		t.Fatal("legacy file changed during runtime load")
	}
}

func TestStoreRejectsLegacyServersSchemaAtCanonicalPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "upstreams.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"servers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).Load(); err == nil {
		t.Fatal("legacy servers schema was accepted at canonical upstream path")
	}
}
