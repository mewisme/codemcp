package releaseverify

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsSetupContractMatchesCanonicalReleaseLayout(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	if err := verifyWindowsSetupContract(root); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSetupContractRejectsToolchainDrift(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(windowsSetupContractPath)))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(data), `"version": "7.1.0"`, `"version": "7.1.1"`, 1)
	if mutated == string(data) {
		t.Fatal("fixture did not contain pinned Inno Setup version")
	}
	fixture := t.TempDir()
	target := filepath.Join(fixture, filepath.FromSlash(windowsSetupContractPath))
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsSetupContract(fixture); err == nil || !strings.Contains(err.Error(), "toolchain contract drifted") {
		t.Fatalf("error = %v", err)
	}
}

func TestWindowsSetupContractRejectsUnknownFields(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(windowsSetupContractPath)))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(data), `"schema": 1,`, `"schema": 1, "unexpected": true,`, 1)
	fixture := t.TempDir()
	target := filepath.Join(fixture, filepath.FromSlash(windowsSetupContractPath))
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsSetupContract(fixture); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v", err)
	}
}

func windowsSetupRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Windows setup contract test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
