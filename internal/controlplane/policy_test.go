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
		{"--config-dir", "/tmp/config", "config", "get", "server.expose"}, {"--verbose", "status"}, {"--help"},
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
		{"upgrade"}, {"update"}, {"install"}, {"config", "set", "server.port", "41001"}, {"workspace", "access", "add", "ws_test", "/tmp"},
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
