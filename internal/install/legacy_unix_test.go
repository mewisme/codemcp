//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindLegacyInstallationsUsesHistoricalExecutableIdentity(t *testing.T) {
	layout := testLayout(t)
	legacyDir := t.TempDir()
	legacyBinary := filepath.Join(legacyDir, historicalBinaryName())
	copyTestExecutable(t, legacyBinary)
	if err := os.WriteFile(filepath.Join(legacyDir, layout.BinaryName), []byte("current-name-decoy"), 0755); err != nil {
		t.Fatal(err)
	}
	setLegacyTestEnvironment(t, legacyDir)
	items, err := FindLegacyInstallations(layout, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !samePath(items[0].Path, legacyBinary) || !items[0].Verified || !items[0].Removable {
		t.Fatalf("legacy items = %+v", items)
	}
}

func TestHistoricalAliasVerificationTargetsHistoricalExecutable(t *testing.T) {
	dir := t.TempDir()
	legacyBinary := filepath.Join(dir, historicalBinaryName())
	copyTestExecutable(t, legacyBinary)
	aliasPath := filepath.Join(dir, historicalAliasName())
	if err := os.Symlink(historicalBinaryName(), aliasPath); err != nil {
		t.Fatal(err)
	}
	target, ok, err := legacyAliasTargetPlatform(aliasPath, historicalBinaryName())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !samePath(target, legacyBinary) {
		t.Fatalf("alias target = %q, ok=%v", target, ok)
	}
	if _, ok, err := legacyAliasTargetPlatform(aliasPath, "cm"); err != nil || ok {
		t.Fatalf("historical alias matched current cm identity: ok=%v err=%v", ok, err)
	}
}

func TestFindLegacyInstallationsPreservesPackageManagerClassification(t *testing.T) {
	layout := testLayout(t)
	home := t.TempDir()
	homebrewDir := filepath.Join(t.TempDir(), "Cellar", "chatgpt-mcp", "1.0.0", "bin")
	goDir := filepath.Join(home, "go", "bin")
	unknownDir := t.TempDir()
	for _, dir := range []string{homebrewDir, goDir, unknownDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	copyTestExecutable(t, filepath.Join(homebrewDir, historicalBinaryName()))
	copyTestExecutable(t, filepath.Join(goDir, historicalBinaryName()))
	if err := os.WriteFile(filepath.Join(unknownDir, historicalBinaryName()), []byte("not chatgpt-mcp"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	t.Setenv("SCOOP", "")
	t.Setenv("PATH", homebrewDir+string(os.PathListSeparator)+goDir+string(os.PathListSeparator)+unknownDir)
	items, err := FindLegacyInstallations(layout, "")
	if err != nil {
		t.Fatal(err)
	}
	methods := map[Method]bool{}
	for _, item := range items {
		methods[item.Method] = true
		switch item.Method {
		case MethodHomebrew:
			if !item.PackageManaged || item.Removable {
				t.Fatalf("homebrew legacy item ownership = %+v", item)
			}
		case MethodGo:
			if item.PackageManaged || item.Removable {
				t.Fatalf("go legacy item ownership = %+v", item)
			}
		case MethodUnknown:
			if item.Verified || item.Removable {
				t.Fatalf("unrelated same-name item ownership = %+v", item)
			}
		}
	}
	if len(items) != 3 || !methods[MethodHomebrew] || !methods[MethodGo] || !methods[MethodUnknown] {
		t.Fatalf("legacy items = %+v", items)
	}
}

func TestInstallPreservesHistoricalExecutableAndAlias(t *testing.T) {
	layout := testLayout(t)
	legacyDir := t.TempDir()
	legacyBinary := filepath.Join(legacyDir, historicalBinaryName())
	legacyAlias := filepath.Join(legacyDir, historicalAliasName())
	copyTestExecutable(t, legacyBinary)
	if err := os.Symlink(historicalBinaryName(), legacyAlias); err != nil {
		t.Fatal(err)
	}
	setLegacyTestEnvironment(t, legacyDir+string(os.PathListSeparator)+layout.BinDir)
	if _, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: testBinary(t, "release")}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{legacyBinary, legacyAlias} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("historical launcher was mutated %s: %v", path, err)
		}
	}
	if _, err := os.Stat(layout.CanonicalBinary); err != nil {
		t.Fatalf("canonical cm missing: %v", err)
	}
}

func TestSamePathResolvesSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}
	realPath := filepath.Join(realDir, historicalBinaryName())
	if err := os.WriteFile(realPath, []byte("test"), 0755); err != nil {
		t.Fatal(err)
	}
	if !samePath(filepath.Join(aliasDir, historicalBinaryName()), realPath) {
		t.Fatalf("symlinked path was not canonicalized: alias=%q real=%q", aliasDir, realDir)
	}
}

func copyTestExecutable(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyBinary(source, destination); err != nil {
		t.Fatal(err)
	}
	if !verifyChatGPTMCPBinary(destination) {
		t.Fatalf("test executable was not recognized as chatgpt-mcp: %s", destination)
	}
}

func setLegacyTestEnvironment(t *testing.T, path string) {
	t.Helper()
	t.Setenv("PATH", path)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))
	t.Setenv("SCOOP", "")
}
