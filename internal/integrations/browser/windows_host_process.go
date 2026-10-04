package browser

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func windowsHostOwnedProcessFilter(executable, profilePath string) string {
	executableExpr := powershellBase64String(strings.TrimSpace(executable))
	profileExpr := powershellBase64String(strings.TrimSpace(profilePath))
	return "$e=" + executableExpr + ";" +
		"$d=" + profileExpr + ";" +
		"$_.ExecutablePath -and $_.CommandLine -and " +
		"$_.ExecutablePath.Equals($e,[StringComparison]::OrdinalIgnoreCase) -and " +
		"$_.CommandLine.Contains('--user-data-dir') -and $_.CommandLine.Contains($d)"
}

func windowsHostBrowserStopScript(executable, profilePath string) string {
	filter := windowsHostOwnedProcessFilter(executable, profilePath)
	return "$ErrorActionPreference='Stop';" +
		"for($i=0;$i -lt 25;$i++){" +
		"$owned=@(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue|" +
		"Where-Object{" + filter + "});" +
		"if($owned.Count -eq 0){exit 0};" +
		"foreach($p in $owned){Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue};" +
		"Start-Sleep -Milliseconds 100};exit 1"
}

func stopWindowsHostBrowser(ctx context.Context, executable, profilePath string) error {
	executable = strings.TrimSpace(executable)
	profilePath = strings.TrimSpace(profilePath)
	if executable == "" || profilePath == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		return fmt.Errorf("stop windows-host browser requires powershell.exe: %w", err)
	}
	command := exec.CommandContext(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command",
		windowsHostBrowserStopScript(executable, profilePath))
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("stop windows-host browser: %w", err)
	}
	return nil
}
