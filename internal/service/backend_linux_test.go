//go:build linux

package service

import (
	"strings"
	"testing"
)

func TestLinuxUserUnitUsesExplicitConfigAndUserTarget(t *testing.T) {
	spec := Spec{ID: "chatgpt-mcp-user-test", Scope: ScopeUser, ConfigRoot: "/home/mew/.config/chatgpt-mcp", Binary: "/home/mew/.local/bin/cm", EnvironmentHash: "environment-test", Account: Account{Username: "mew", UID: "1000", GID: "1000", HomeDir: "/home/mew"}}
	unit := LinuxUnit(spec)
	for _, expected := range []string{`ExecStart="/home/mew/.local/bin/cm" "--config-dir" "/home/mew/.config/chatgpt-mcp" "_service" "run"`, "NoNewPrivileges=true", "WantedBy=default.target", "Restart=always", "StartLimitIntervalSec=0"} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
	if strings.Contains(unit, "Restart=on-failure") || strings.Contains(unit, "StartLimitBurst=") {
		t.Fatalf("unit keeps bounded failure-only restart policy:\n%s", unit)
	}
	if !strings.Contains(unit, `"--service-environment-hash" "environment-test"`) {
		t.Fatalf("unit missing environment hash:\n%s", unit)
	}
	if strings.Contains(unit, "User=") {
		t.Fatalf("user unit should inherit user manager identity:\n%s", unit)
	}
}

func TestLinuxSystemUnitRunsAsInvokingUser(t *testing.T) {
	spec := Spec{ID: "chatgpt-mcp-system-test", Scope: ScopeSystem, ConfigRoot: "/home/mew/.config/chatgpt-mcp", Binary: "/usr/local/bin/cm", Account: Account{Username: "mew", UID: "1000", GID: "1000", HomeDir: "/home/mew"}}
	unit := LinuxUnit(spec)
	for _, expected := range []string{"User=mew", "Group=1000", "NoNewPrivileges=true", "WantedBy=multi-user.target"} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("system unit missing %q:\n%s", expected, unit)
		}
	}
	if strings.Contains(unit, "User=root") {
		t.Fatalf("system unit unexpectedly runs MCP as root:\n%s", unit)
	}
}

func TestLinuxUnitChangesWhenEnvironmentChanges(t *testing.T) {
	spec := Spec{ID: "chatgpt-mcp-user-test", Scope: ScopeUser, ConfigRoot: "/home/mew/.config/chatgpt-mcp", Binary: "/home/mew/.local/bin/cm", EnvironmentHash: "first", Account: Account{Username: "mew", HomeDir: "/home/mew"}}
	first := LinuxUnit(spec)
	spec.EnvironmentHash = "second"
	second := LinuxUnit(spec)
	if first == second || !strings.Contains(second, `"--service-environment-hash" "second"`) {
		t.Fatalf("environment change did not update service definition:\n%s", second)
	}
}
