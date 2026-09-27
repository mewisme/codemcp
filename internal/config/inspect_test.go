package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestInspectIsReadOnlyAndScrubsTunnelSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath("") })

	cfg := Default()
	cfg.Tunnel.Enabled = true
	cfg.Tunnel.ID = "tunnel_test"
	cfg.Tunnel.APIKey = "raw-runtime-secret"
	cfg.Tunnel.Admin.Key = "raw-admin-secret"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	before := snapshotConfigTree(t, root)

	inspection, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Exists || !inspection.TunnelRuntimeKeyConfigured || !inspection.TunnelAdminKeyConfigured {
		t.Fatalf("inspection=%#v", inspection)
	}
	if inspection.Config.Tunnel.APIKey != "" || inspection.Config.Tunnel.Admin.Key != "" {
		t.Fatalf("inspection exposed tunnel secret material: %#v", inspection.Config.Tunnel)
	}
	after := snapshotConfigTree(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("config inspection mutated persistent state\nbefore=%#v\nafter=%#v", before, after)
	}
}

func snapshotConfigTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(path[len(root):])] = bytes.Clone(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
