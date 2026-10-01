package controlplane

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestReadOnlyCommandPolicy(t *testing.T) {
	for _, args := range [][]string{
		{"status"}, {"config", "list"}, {"auth", "status"},
		{"workspace", "access", "list", "ws_test"}, {"upstream", "server", "show", "server"}, {"tunnel", "status"}, {"upgrade", "check"}, {"update", "check"},
		{"request", "list"}, {"request", "view", "req_test"}, {"req", "ls"}, {"req", "show", "req_test"}, {"req", "info", "req_test"},
		{"st"}, {"cfg", "ls"}, {"ws", "access", "ls", "ws_test"}, {"upstream", "server", "st", "server"}, {"tunnel", "st"}, {"completion", "bash"},
		{"--config-dir", "/tmp/config", "config", "get", "http.exposure"}, {"--verbose", "status"}, {"--help"},
	} {
		if !IsReadOnlyArgs(args) {
			t.Fatalf("read-only command denied: %#v -> %q", args, PathFromArgs(args))
		}
	}
	for _, args := range [][]string{
		{"config", "set", "permissions.allow_dirs", "/tmp"}, {"config", "export", "backup.json"}, {"config", "import", "backup.json"},
		{"cfg", "set", "permissions.allow_dirs", "/tmp"}, {"ws", "register", "."},
		{"auth", "mcp", "create"}, {"workspace", "register", "."}, {"workspace", "access", "add", "ws_test", "/tmp"},
		{"request", "approve", "req_test"}, {"request", "deny", "req_test"}, {"request", "grant", "revoke", "req_test"}, {"req", "accept", "req_test"}, {"req", "allow", "req_test"}, {"req", "reject", "req_test"},
		{"upstream", "server", "add", "server"}, {"tunnel", "enable"}, {"upgrade"}, {"update"}, {"serve"}, {},
	} {
		if IsReadOnlyArgs(args) {
			t.Fatalf("mutating command allowed: %#v -> %q", args, PathFromArgs(args))
		}
	}
}

func TestApprovalEligibleCommandPolicy(t *testing.T) {
	for _, args := range [][]string{
		{"upgrade"}, {"update"}, {"install"}, {"config", "set", "http.mcp.port", "41001"}, {"workspace", "access", "add", "ws_test", "/tmp"},
	} {
		if !ApprovalEligibleArgs(args) {
			t.Fatalf("approval-eligible command denied: %#v -> %q", args, PathFromArgs(args))
		}
	}
	for _, args := range [][]string{
		{"status"}, {"upgrade", "check"}, {"update", "check"}, {"request", "approve", "req_test"}, {"request", "deny", "req_test"}, {"req", "accept", "req_test"}, {"req", "reject", "req_test"}, {"request", "list"}, {"request", "view", "req_test"}, {"_service", "run"}, {},
	} {
		if ApprovalEligibleArgs(args) {
			t.Fatalf("hard-denied/read-only command became approval eligible: %#v -> %q", args, PathFromArgs(args))
		}
	}
}

func TestUpdateAliasCanonicalizesToUpgrade(t *testing.T) {
	if got := PathFromArgs([]string{"update", "check"}); got != "upgrade check" {
		t.Fatalf("update alias path=%q want upgrade check", got)
	}
	if got := PathFromArgs([]string{"update"}); got != "upgrade" {
		t.Fatalf("update alias path=%q want upgrade", got)
	}
}

func TestAliasSecurityPolicyMatchesCanonicalCommands(t *testing.T) {
	tests := []struct {
		name      string
		canonical []string
		alias     []string
	}{
		{name: "config mutation", canonical: []string{"config", "set", "http.mcp.port", "41001"}, alias: []string{"cfg", "set", "http.mcp.port", "41001"}},
		{name: "upstream remove", canonical: []string{"upstream", "server", "remove", "github"}, alias: []string{"ups", "server", "rm", "github"}},
		{name: "workspace container delete", canonical: []string{"workspace", "container", "delete", "wsc_test"}, alias: []string{"ws", "ctr", "rm", "wsc_test"}},
		{name: "telegram token remove", canonical: []string{"telegram", "token", "remove"}, alias: []string{"tg", "token", "rm"}},
		{name: "telemetry status", canonical: []string{"telemetry", "status"}, alias: []string{"tel", "st"}},
		{name: "review decision", canonical: []string{"request", "approve", "req_test"}, alias: []string{"req", "allow", "req_test"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			canonicalPath := PathFromArgs(test.canonical)
			aliasPath := PathFromArgs(test.alias)
			if aliasPath != canonicalPath {
				t.Fatalf("alias path=%q canonical path=%q", aliasPath, canonicalPath)
			}
			if IsReadOnlyArgs(test.alias) != IsReadOnlyArgs(test.canonical) {
				t.Fatalf("read-only policy drifted: alias=%v canonical=%v", test.alias, test.canonical)
			}
			if ApprovalEligibleArgs(test.alias) != ApprovalEligibleArgs(test.canonical) {
				t.Fatalf("approval policy drifted: alias=%v canonical=%v", test.alias, test.canonical)
			}
		})
	}
}

func TestAliasAfterSecurityBoundaryFailsClosed(t *testing.T) {
	args := []string{"ws", "--unknown", "ls"}
	if got := PathFromArgs(args); got != "workspace --unknown" {
		t.Fatalf("path=%q want fail-closed workspace --unknown", got)
	}
	if IsReadOnlyArgs(args) {
		t.Fatalf("unresolved alias after flag was treated as read-only: %#v", args)
	}
	if ApprovalEligibleArgs(args) {
		t.Fatalf("unresolved alias after flag became approval eligible: %#v", args)
	}
}

func TestDestructiveAliasesKeepCanonicalConfirmationPolicy(t *testing.T) {
	for _, test := range []struct {
		canonical []string
		alias     []string
	}{
		{canonical: []string{"upstream", "server", "remove", "github"}, alias: []string{"ups", "server", "rm", "github"}},
		{canonical: []string{"workspace", "container", "delete", "wsc_test"}, alias: []string{"ws", "ctr", "rm", "wsc_test"}},
	} {
		canonicalPath := PathFromArgs(test.canonical)
		aliasPath := PathFromArgs(test.alias)
		if aliasPath != canonicalPath {
			t.Fatalf("alias path=%q canonical path=%q", aliasPath, canonicalPath)
		}
		canonicalID, canonicalOK := capability.ForPath(canonicalPath)
		aliasID, aliasOK := capability.ForPath(aliasPath)
		if !canonicalOK || !aliasOK || aliasID != canonicalID {
			t.Fatalf("operation mismatch alias=%q,%t canonical=%q,%t", aliasID, aliasOK, canonicalID, canonicalOK)
		}
		spec, ok := capability.Lookup(aliasID)
		if !ok || spec.Risk != capability.RiskDestructive || !spec.Effects.Destructive || spec.Confirmation.Mode == capability.ConfirmationNone {
			t.Fatalf("destructive alias lost confirmation metadata: %#v", spec)
		}
	}
}

func TestCLIControlPolicyMatchesCanonicalOperationRegistry(t *testing.T) {
	for _, spec := range capability.All() {
		if !spec.HasCLI() {
			continue
		}
		path := capability.NormalizePath(spec.CLI.CanonicalPath)
		if path == capability.RootPath {
			continue
		}
		if got := IsReadOnlyPath(path); got != spec.Effects.ReadOnly {
			t.Errorf("%s read-only policy=%t want %t", spec.ID, got, spec.Effects.ReadOnly)
		}
		args := strings.Fields(path)
		if got := ApprovalEligibleArgs(args); got != spec.Confirmation.ControlApproval {
			t.Errorf("%s approval policy=%t want %t", spec.ID, got, spec.Confirmation.ControlApproval)
		}
	}
}
