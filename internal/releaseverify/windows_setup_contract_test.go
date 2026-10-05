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

func TestWindowsSetupBootstrapRejectsInnoOwnershipDrift(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	fixture := copyWindowsSetupBootstrapFixture(t, root)
	path := filepath.Join(fixture, "installer", "windows", "codemcp.iss")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(data), "CreateAppDir=no", "CreateAppDir=yes", 1)
	if mutated == string(data) {
		t.Fatal("fixture did not contain canonical CreateAppDir contract")
	}
	if err := os.WriteFile(path, []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsSetupBootstrap(fixture); err == nil || !strings.Contains(err.Error(), "CreateAppDir=no") {
		t.Fatalf("error = %v", err)
	}
}

func TestWindowsSetupBootstrapRejectsRetiredCompilerDependency(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	fixture := copyWindowsSetupBootstrapFixture(t, root)
	path := filepath.Join(fixture, "scripts", "release", "install-inno-setup.ps1")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n# makensis must never return\n")...)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsSetupBootstrap(fixture); err == nil || !strings.Contains(err.Error(), "retired Windows setup tooling") {
		t.Fatalf("error = %v", err)
	}
}

func TestWindowsSetupBootstrapRejectsFailurePathIsolationDrift(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	fixture := copyWindowsSetupBootstrapFixture(t, root)
	path := filepath.Join(fixture, "scripts", "installer", "test-windows-setup.ps1")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(data), "$pathBeforeFailure = Get-UserPathState", "$pathBeforeFailure = $userPathBefore", 1)
	if mutated == string(data) {
		t.Fatal("fixture did not contain failure PATH isolation baseline")
	}
	if err := os.WriteFile(path, []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsSetupBootstrap(fixture); err == nil || !strings.Contains(err.Error(), "$pathBeforeFailure = Get-UserPathState") {
		t.Fatalf("error = %v", err)
	}
}

func TestWindowsSetupBootstrapRejectsUserPathTypeRollbackDrift(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	fixture := copyWindowsSetupBootstrapFixture(t, root)
	path := filepath.Join(fixture, "scripts", "installer", "test-windows-setup.ps1")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(
		string(data),
		"$key.SetValue('Path', $State.Value, $State.Kind)",
		"$key.SetValue('Path', $State.Value)",
		1,
	)
	if mutated == string(data) {
		t.Fatal("fixture did not contain typed HKCU PATH restoration")
	}
	if err := os.WriteFile(path, []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsSetupBootstrap(fixture); err == nil || !strings.Contains(err.Error(), "$key.SetValue('Path', $State.Value, $State.Kind)") {
		t.Fatalf("error = %v", err)
	}
}

func TestReleaseWorkflowRejectsRetiredWindowsSetupDependency(t *testing.T) {
	root := windowsSetupRepositoryRoot(t)
	fixture := t.TempDir()
	for _, relative := range []string{
		filepath.FromSlash(windowsSetupContractPath),
		filepath.Join(".github", "workflows", "release.yml"),
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("scripts", "release", "install-inno-setup.ps1"),
		filepath.Join("scripts", "release", "verify-windows-setup-payload.sh"),
		filepath.Join("scripts", "release", "verify", "main.go"),
	} {
		copyWindowsSetupFixtureFile(t, root, fixture, relative)
	}
	ciPath := filepath.Join(fixture, ".github", "workflows", "ci.yml")
	data, err := os.ReadFile(ciPath)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n# choco install nsis -y --no-progress\n")...)
	if err := os.WriteFile(ciPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyReleaseWorkflows(fixture); err == nil || !strings.Contains(err.Error(), "retired Windows setup tooling") {
		t.Fatalf("error = %v", err)
	}
}

func copyWindowsSetupBootstrapFixture(t *testing.T, root string) string {
	t.Helper()
	fixture := t.TempDir()
	for _, relative := range []string{
		filepath.FromSlash(windowsSetupContractPath),
		filepath.Join("installer", "windows", "codemcp.iss"),
		filepath.Join("scripts", "release", "build-windows-setup.ps1"),
		filepath.Join("scripts", "release", "install-inno-setup.ps1"),
		filepath.Join("scripts", "installer", "test-windows-setup.ps1"),
	} {
		copyWindowsSetupFixtureFile(t, root, fixture, relative)
	}
	return fixture
}

func copyWindowsSetupFixtureFile(t *testing.T, root, fixture, relative string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(fixture, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0644); err != nil {
		t.Fatal(err)
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
