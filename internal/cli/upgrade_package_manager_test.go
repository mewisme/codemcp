package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	updatepkg "go.mewis.me/codemcp/internal/update"
)

func TestRunPackageManagerPhaseUsesRefreshThenApplyCommands(t *testing.T) {
	plan, ok := updatepkg.PackageManagerPlanFor("scoop")
	if !ok {
		t.Fatal("scoop plan unavailable")
	}
	commands := []string{}
	run := func(_ context.Context, command updatepkg.PackageManagerCommand) (string, error) {
		commands = append(commands, command.Name+" "+strings.Join(command.Args, " "))
		return "ok", nil
	}
	cmd := newRootCommand()
	log := commandLogger(cmd)
	if err := runPackageManagerPhase(cmd, log, plan, "refresh", run); err != nil {
		t.Fatal(err)
	}
	if err := runPackageManagerPhase(cmd, log, plan, "apply", run); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(commands, " | "); got != "scoop update | scoop update mew/chatgpt-mcp" {
		t.Fatalf("commands = %q", got)
	}
}

func TestRunPackageManagerPhaseStopsAfterRefreshFailure(t *testing.T) {
	plan, _ := updatepkg.PackageManagerPlanFor("homebrew")
	run := func(context.Context, updatepkg.PackageManagerCommand) (string, error) {
		return "network failed", errors.New("exit status 1")
	}
	cmd := newRootCommand()
	err := runPackageManagerPhase(cmd, commandLogger(cmd), plan, "refresh", run)
	if err == nil || !strings.Contains(err.Error(), "refreshing homebrew metadata") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyPackageManagedVersion(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "cm" {
			return "/bin/cm", nil
		}
		return "", errors.New("not found")
	}
	readName := ""
	readVersion := func(_ context.Context, name string) (string, error) {
		readName = name
		return "cm version v1.2.3 (abc123) 2026-09-13", nil
	}
	binary, installed, err := verifyPackageManagedVersion(context.Background(), "v1.2.3", lookup, readVersion)
	if err != nil {
		t.Fatal(err)
	}
	if binary != "/bin/cm" || installed != "v1.2.3" || readName != "cm" {
		t.Fatalf("binary = %q installed = %q read = %q", binary, installed, readName)
	}
}

func TestRunPackageManagerCommandRejectsUnknownInvocation(t *testing.T) {
	_, err := runPackageManagerCommand(t.Context(), updatepkg.PackageManagerCommand{Name: "scoop", Args: []string{"update", "other/app"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported package manager command") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPackageBinaryVersionRejectsUnknownBinary(t *testing.T) {
	_, err := runPackageBinaryVersion(t.Context(), "/tmp/chatgpt-mcp")
	if err == nil || !strings.Contains(err.Error(), "unsupported package binary") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyPackageManagedVersionRejectsStaleMetadata(t *testing.T) {
	lookup := func(string) (string, error) { return "/bin/cm", nil }
	readVersion := func(context.Context, string) (string, error) { return "cm version v1.2.2", nil }
	_, installed, err := verifyPackageManagedVersion(context.Background(), "v1.2.3", lookup, readVersion)
	if err == nil || installed != "v1.2.2" || !strings.Contains(err.Error(), "package metadata did not install v1.2.3") {
		t.Fatalf("installed = %q error = %v", installed, err)
	}
}

func TestVerifyPackageManagedVersionAcceptsNewerReleaseRace(t *testing.T) {
	lookup := func(string) (string, error) { return "/bin/cm", nil }
	readVersion := func(context.Context, string) (string, error) { return "cm version v1.2.4", nil }
	_, installed, err := verifyPackageManagedVersion(context.Background(), "v1.2.3", lookup, readVersion)
	if err != nil || installed != "v1.2.4" {
		t.Fatalf("installed = %q error = %v", installed, err)
	}
}

func TestPackageVersionFromOutput(t *testing.T) {
	for _, output := range []string{"cm version v0.2.18 (abc) now", "cm version 0.2.18"} {
		version, err := packageVersionFromOutput(output)
		if err != nil || version != "v0.2.18" {
			t.Fatalf("output = %q version = %q error = %v", output, version, err)
		}
	}
}

func TestPreparePackageUpgradeHandoffRejectsForegroundRuntime(t *testing.T) {
	plan, _ := updatepkg.PackageManagerPlanFor("scoop")
	_, err := preparePackageUpgradeHandoff(plan, "v1.2.3", t.TempDir(), updateRuntimeState{Running: true, Status: runtimeStatusResult{PID: 42}}, false)
	if err == nil || !strings.Contains(err.Error(), "foreground runtime") {
		t.Fatalf("error = %v", err)
	}
}

func TestPackageUpgradePowerShellWaitsForParentAndUsesScoopAfterRuntimeStops(t *testing.T) {
	plan, _ := updatepkg.PackageManagerPlanFor("scoop")
	script := packageUpgradePowerShell(packageUpgradeHandoff{ParentPID: 1234, Plan: plan, Target: "v1.2.3", ConfigRoot: `C:\Users\Mew\.chatgpt-mcp`, Runtime: updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true}}, ScriptPath: `C:\Temp\upgrade.ps1`, LogPath: `C:\Temp\upgrade.log`})
	for _, expected := range []string{"Wait-Process -Id $parentPid", "& cm '--config-dir' 'C:\\Users\\Mew\\.chatgpt-mcp' 'down'", "& scoop update", "& scoop update mew/chatgpt-mcp", "$version = (& cm --version | Out-String)", "$restartRuntime = $true", "& cm '--config-dir' 'C:\\Users\\Mew\\.chatgpt-mcp' 'up'"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("script missing %q:\n%s", expected, script)
		}
	}
}

func TestPackageUpgradeShellRestoresSystemRuntime(t *testing.T) {
	plan, _ := updatepkg.PackageManagerPlanFor("homebrew")
	script := packageUpgradeShell(packageUpgradeHandoff{ParentPID: 1234, Plan: plan, Target: "v1.2.3", ConfigRoot: "/etc/chatgpt-mcp", Runtime: updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true, ServiceScope: "system"}}, ScriptPath: "/tmp/upgrade.sh", LogPath: "/tmp/upgrade.log"})
	for _, expected := range []string{"while kill -0 \"$parent_pid\"", "cm '--config-dir' '/etc/chatgpt-mcp' 'down' '--system'", "brew update", "brew upgrade --cask chatgpt-mcp", "cm '--config-dir' '/etc/chatgpt-mcp' 'up' '--system'"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("script missing %q:\n%s", expected, script)
		}
	}
}

func TestPackageUpgradePowerShellHonorsNoRestart(t *testing.T) {
	plan, _ := updatepkg.PackageManagerPlanFor("scoop")
	script := packageUpgradePowerShell(packageUpgradeHandoff{ParentPID: 1234, Plan: plan, Target: "v1.2.3", ConfigRoot: `C:\cfg`, Runtime: updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true}}, NoRestart: true, ScriptPath: `C:\Temp\upgrade.ps1`, LogPath: `C:\Temp\upgrade.log`})
	if !strings.Contains(script, "$stopRuntime = $true") || !strings.Contains(script, "$restartRuntime = $false") {
		t.Fatalf("unexpected no-restart script:\n%s", script)
	}
}
