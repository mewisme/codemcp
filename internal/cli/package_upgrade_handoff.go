package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	updatepkg "go.mewis.me/codemcp/internal/update"
)

type packageUpgradeHandoff struct {
	ParentPID  int
	Plan       updatepkg.PackageManagerPlan
	Target     string
	ConfigRoot string
	Runtime    updateRuntimeState
	NoRestart  bool
	ScriptPath string
	LogPath    string
}

func preparePackageUpgradeHandoff(plan updatepkg.PackageManagerPlan, target, configRoot string, state updateRuntimeState, noRestart bool) (packageUpgradeHandoff, error) {
	if state.Running && !state.Status.Managed {
		return packageUpgradeHandoff{}, fmt.Errorf("foreground runtime is using the package-managed binary (pid %d); stop it before upgrading", state.Status.PID)
	}
	ext := ".sh"
	if runtime.GOOS == "windows" {
		ext = ".ps1"
	}
	file, err := os.CreateTemp("", "chatgpt-mcp-upgrade-*"+ext)
	if err != nil {
		return packageUpgradeHandoff{}, err
	}
	path := file.Name()
	_ = file.Close()
	logPath := filepath.Join(os.TempDir(), "chatgpt-mcp-upgrade-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".log")
	handoff := packageUpgradeHandoff{ParentPID: os.Getpid(), Plan: plan, Target: target, ConfigRoot: configRoot, Runtime: state, NoRestart: noRestart, ScriptPath: path, LogPath: logPath}
	content, err := packageUpgradeScript(handoff)
	if err != nil {
		_ = os.Remove(path)
		return packageUpgradeHandoff{}, err
	}
	mode := os.FileMode(0o600)
	if runtime.GOOS != "windows" {
		mode = 0o700
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		_ = os.Remove(path)
		return packageUpgradeHandoff{}, err
	}
	return handoff, nil
}

func packageUpgradeScript(handoff packageUpgradeHandoff) (string, error) {
	switch handoff.Plan.Method {
	case "scoop":
		return packageUpgradePowerShell(handoff), nil
	case "homebrew":
		return packageUpgradeShell(handoff), nil
	default:
		return "", fmt.Errorf("unsupported package manager handoff: %s", handoff.Plan.Method)
	}
}

func packageUpgradePowerShell(h packageUpgradeHandoff) string {
	stop := h.Runtime.Running
	restart := h.Runtime.Running && !h.NoRestart
	stopArgs := []string{"--config-dir", h.ConfigRoot, "down"}
	startArgs := []string{"--config-dir", h.ConfigRoot, "up"}
	return strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$parentPid = " + strconv.Itoa(h.ParentPID),
		"$target = " + psQuote(h.Target),
		"$logPath = " + psQuote(h.LogPath),
		"$scriptPath = $MyInvocation.MyCommand.Path",
		"$stopRuntime = " + psBool(stop),
		"$restartRuntime = " + psBool(restart),
		"$exitCode = 0",
		"function Invoke-Step([scriptblock]$Action, [string]$Name) {",
		"    & $Action *>> $logPath",
		"    if ($LASTEXITCODE -ne 0) { throw \"$Name failed with exit code $LASTEXITCODE\" }",
		"}",
		"try {",
		"    Wait-Process -Id $parentPid -ErrorAction SilentlyContinue",
		"    if ($stopRuntime) { Invoke-Step { & cm " + psArgs(stopArgs) + " } 'Stopping managed runtime' }",
		"    Invoke-Step { & scoop update } 'Scoop metadata refresh'",
		"    Invoke-Step { & scoop update mew/chatgpt-mcp } 'Scoop package update'",
		"    $version = (& cm --version | Out-String)",
		"    $version *>> $logPath",
		"    if (-not $version.Contains($target)) { throw \"updated version mismatch: expected $target, got $($version.Trim())\" }",
		"} catch {",
		"    ($_ | Out-String) *>> $logPath",
		"    $exitCode = 1",
		"} finally {",
		"    if ($restartRuntime) {",
		"        try { Invoke-Step { & cm " + psArgs(startArgs) + " } 'Restoring managed runtime' } catch { ($_ | Out-String) *>> $logPath; $exitCode = 1 }",
		"    }",
		"    Remove-Item -LiteralPath $scriptPath -Force -ErrorAction SilentlyContinue",
		"}",
		"exit $exitCode",
	}, "\r\n") + "\r\n"
}

func packageUpgradeShell(h packageUpgradeHandoff) string {
	stop := h.Runtime.Running
	restart := h.Runtime.Running && !h.NoRestart
	stopArgs := []string{"--config-dir", h.ConfigRoot, "down"}
	startArgs := []string{"--config-dir", h.ConfigRoot, "up"}
	if h.Runtime.Status.ServiceScope == "system" {
		stopArgs = append(stopArgs, "--system")
		startArgs = append(startArgs, "--system")
	}
	return strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		"parent_pid=" + strconv.Itoa(h.ParentPID),
		"target=" + shQuote(h.Target),
		"log_path=" + shQuote(h.LogPath),
		"script_path=" + shQuote(h.ScriptPath),
		"restart_runtime=" + boolDigit(restart),
		"exec >>\"$log_path\" 2>&1",
		"cleanup() {",
		"  status=$?",
		"  trap - EXIT",
		"  if [ \"$restart_runtime\" = 1 ]; then cm " + shArgs(startArgs) + " || status=$?; fi",
		"  rm -f -- \"$script_path\"",
		"  exit \"$status\"",
		"}",
		"trap cleanup EXIT",
		"while kill -0 \"$parent_pid\" 2>/dev/null; do sleep 0.1; done",
		conditionalShell(stop, "cm "+shArgs(stopArgs)),
		"brew update",
		"brew upgrade --cask chatgpt-mcp",
		"version=$(cm --version)",
		"printf '%s\\n' \"$version\"",
		"case \"$version\" in *\"$target\"*) ;; *) printf 'updated version mismatch: expected %s, got %s\\n' \"$target\" \"$version\" >&2; exit 1 ;; esac",
	}, "\n") + "\n"
}

func psQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func psArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = psQuote(arg)
	}
	return strings.Join(quoted, " ")
}
func psBool(value bool) string {
	if value {
		return "$true"
	}
	return "$false"
}
func shQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func shArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shQuote(arg)
	}
	return strings.Join(quoted, " ")
}
func boolDigit(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
func conditionalShell(enabled bool, command string) string {
	if enabled {
		return command
	}
	return ":"
}
