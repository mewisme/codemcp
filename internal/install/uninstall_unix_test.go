//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallOwnedPreservesSharedConfigAndUserState(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cm")
	binDir := filepath.Join(t.TempDir(), "bin")
	layout, err := NewLayout(root, binDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Current, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Versions, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.State, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.BinDir, 0755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		layout.CurrentBinary:                        "binary",
		filepath.Join(layout.Versions, "keep"):      "installer version tree",
		filepath.Join(root, "config.json"):          "{}",
		filepath.Join(root, "workspaces.json"):      "{}",
		filepath.Join(root, "user-note.txt"):        "keep me",
		filepath.Join(layout.State, "runtime.json"): "{}",
		layout.UpdateCache:                          "{}",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(layout.CurrentBinary, layout.CanonicalBinary); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(layout.Metadata, Metadata{Schema: MetadataSchema, Method: MethodDirect, Version: "v1.2.3", InstallDir: root, BinDir: binDir}); err != nil {
		t.Fatal(err)
	}
	result, err := uninstallOwnedWithDependencies(UninstallOptions{Layout: layout}, uninstallDependencies{
		FindLegacyAliases:       func() ([]LegacyAlias, error) { return nil, nil },
		FindLegacyInstallations: func(Layout, string) ([]LegacyInstallation, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DirectRemoved || !result.CanonicalRemoved || !result.ConfigRootPreserved {
		t.Fatalf("result=%#v", result)
	}
	for _, removed := range []string{layout.Current, layout.Versions, layout.Metadata, layout.UpdateCache, layout.CanonicalBinary} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("installer-owned path still exists %s err=%v", removed, err)
		}
	}
	for _, preserved := range []string{
		filepath.Join(root, "config.json"),
		filepath.Join(root, "workspaces.json"),
		filepath.Join(root, "user-note.txt"),
		filepath.Join(layout.State, "runtime.json"),
	} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("shared state removed %s: %v", preserved, err)
		}
	}
}

func TestUninstallOwnedRefusesAmbiguousCanonicalBinary(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cm")
	binDir := filepath.Join(t.TempDir(), "bin")
	layout, err := NewLayout(root, binDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.BinDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.CanonicalBinary, []byte("unrelated"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(layout.Metadata, Metadata{Schema: MetadataSchema, Method: MethodDirect, Version: "v1.2.3", InstallDir: root, BinDir: binDir}); err != nil {
		t.Fatal(err)
	}
	_, err = uninstallOwnedWithDependencies(UninstallOptions{Layout: layout}, uninstallDependencies{
		FindLegacyAliases:       func() ([]LegacyAlias, error) { return nil, nil },
		FindLegacyInstallations: func(Layout, string) ([]LegacyInstallation, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("ambiguous canonical binary was removed")
	}
	if _, statErr := os.Stat(layout.CanonicalBinary); statErr != nil {
		t.Fatalf("ambiguous canonical binary was not preserved: %v", statErr)
	}
}

func TestUninstallOwnedExternalCleanupPreservesActiveBinaryTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cm")
	binDir := filepath.Join(t.TempDir(), "bin")
	layout, err := NewLayout(root, binDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Current, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(layout.Versions, "v1.2.3"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.BinDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.CurrentBinary, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.Versions, "v1.2.3", layout.BinaryName), []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(layout.CurrentBinary, layout.CanonicalBinary); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(layout.Metadata, Metadata{Schema: MetadataSchema, Method: MethodDirect, Version: "v1.2.3", InstallDir: root, BinDir: binDir}); err != nil {
		t.Fatal(err)
	}

	result, err := uninstallOwnedWithDependencies(UninstallOptions{Layout: layout, PreserveBinaryTree: true}, uninstallDependencies{
		FindLegacyAliases:       func() ([]LegacyAlias, error) { return nil, nil },
		FindLegacyInstallations: func(Layout, string) ([]LegacyInstallation, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ExternalCleanupRequired || result.DirectRemoved || result.CanonicalRemoved {
		t.Fatalf("result=%#v", result)
	}
	for _, preserved := range []string{layout.CurrentBinary, layout.Versions, layout.CanonicalBinary, layout.Metadata} {
		if _, err := os.Lstat(preserved); err != nil {
			t.Fatalf("external cleanup prematurely removed %s: %v", preserved, err)
		}
	}
}
