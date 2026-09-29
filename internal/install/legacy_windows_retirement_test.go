//go:build windows

package install

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveLegacyWindowsAliasAndExecutable(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, historicalBinaryName())
	copyWindowsTestExecutable(t, binary)
	aliasPath := filepath.Join(dir, historicalAliasName())
	if err := os.WriteFile(aliasPath, []byte("@echo off\r\n\"%~dp0chatgpt-mcp.exe\" %*\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	alias, err := InspectLegacyAlias(aliasPath)
	if err != nil {
		t.Fatal(err)
	}
	if !alias.Verified || !alias.Removable || alias.PackageManaged {
		t.Fatalf("legacy Windows alias=%#v", alias)
	}
	if removed, err := RemoveLegacyAlias(alias); err != nil || !removed {
		t.Fatalf("remove Windows alias=%t err=%v", removed, err)
	}
	if _, err := os.Stat(aliasPath); !os.IsNotExist(err) {
		t.Fatalf("legacy Windows alias remained: %v", err)
	}

	layout, err := NewLayout(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	installation, err := InspectLegacyInstallation(layout, binary, binary)
	if err != nil {
		t.Fatal(err)
	}
	if !installation.Verified || !installation.Removable || installation.PackageManaged {
		t.Fatalf("legacy Windows executable=%#v", installation)
	}
	if removed, err := RemoveLegacyInstallation(installation); err != nil || !removed {
		t.Fatalf("remove Windows executable=%t err=%v", removed, err)
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("legacy Windows executable remained: %v", err)
	}
}

func copyWindowsTestExecutable(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if !verifyChatGPTMCPBinary(destination) {
		t.Fatalf("test executable was not recognized as historical CodeMCP binary: %s", destination)
	}
}
