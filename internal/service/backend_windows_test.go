//go:build windows

package service

import (
	"strings"
	"testing"
)

func TestWindowsTaskIsPerUserLeastPrivilege(t *testing.T) {
	spec := Spec{ID: "cm-user-test", Scope: ScopeUser, ConfigRoot: `C:\\Users\\Mew\\.cm`, Binary: `C:\\Users\\Mew\\.cm\\current\\cm.exe`, Account: Account{Username: `PC\Mew`, HomeDir: `C:\Users\Mew`}}
	xml, err := WindowsTaskXML(spec)
	if err != nil {
		t.Fatal(err)
	}
	command, err := windowsTaskCommand()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<LogonType>InteractiveToken</LogonType>", "<RunLevel>LeastPrivilege</RunLevel>", "<RestartOnFailure>", "<Command>" + xmlText(command) + "</Command>", "//B", "//NoLogo", xmlText(windowsLauncherPath(spec)), "<Hidden>true</Hidden>", "<Interval>PT1M</Interval>"} {
		if !strings.Contains(xml, expected) {
			t.Fatalf("task XML missing %q:\n%s", expected, xml)
		}
	}
	for _, forbidden := range []string{"SYSTEM", "HighestAvailable", "Password", "encoding=", "<Interval>PT3S</Interval>", "powershell.exe", " -Command ", "-EncodedCommand"} {
		if strings.Contains(xml, forbidden) {
			t.Fatalf("task XML contains forbidden %q:\n%s", forbidden, xml)
		}
	}
}

func TestWindowsTaskLaunchesManagedRuntimeWithoutConsoleWindow(t *testing.T) {
	spec := Spec{ID: "cm-user-test", Scope: ScopeUser, ConfigRoot: `C:\\Users\\Mew\\.cm`, Binary: `C:\\Program Files\\CodeMCP\\cm.exe`, EnvironmentHash: "env_test", Account: Account{Username: `PC\Mew`, HomeDir: `C:\Users\Mew`}}
	script := windowsLauncherScript(spec)
	for _, expected := range []string{`CreateObject("WScript.Shell")`, "shell.CurrentDirectory", "shell.Run(", ", 0, True)", "WScript.Quit exitCode", spec.Binary, "--config-dir", spec.ConfigRoot, "--service-environment-hash", spec.EnvironmentHash} {
		if !strings.Contains(script, expected) {
			t.Fatalf("hidden launcher script missing %q:\n%s", expected, script)
		}
	}
	if strings.Contains(strings.ToLower(script), "powershell") {
		t.Fatalf("hidden launcher should not invoke PowerShell:\n%s", script)
	}
	bytes := windowsLauncherBytes(spec)
	if len(bytes) < 2 || bytes[0] != 0xff || bytes[1] != 0xfe {
		t.Fatalf("launcher must be UTF-16LE with BOM: %x", bytes[:min(len(bytes), 8)])
	}
}
