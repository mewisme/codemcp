package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/controlplane"
	"go.mewis.me/codemcp/internal/testutil"
)

func TestMain(m *testing.M) {
	_, cleanup, err := testutil.IsolateConfigHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	value, exists := os.LookupEnv(controlplane.ToolContextEnv)
	_ = os.Unsetenv(controlplane.ToolContextEnv)
	restoreAncestorContext := controlplane.DisableAncestorContextForTesting()
	code := m.Run()
	restoreAncestorContext()
	if exists {
		_ = os.Setenv(controlplane.ToolContextEnv, value)
	}
	cleanup()
	os.Exit(code)
}

func TestMCPToolContextAllowsOnlyReadOnlyCLICommands(t *testing.T) {
	t.Setenv(controlplane.ToolContextEnv, "1")
	root := newRootCommand()
	for _, path := range [][]string{{"status"}, {"config", "list"}, {"auth", "status"}, {"request", "list"}, {"request", "view"}, {"workspace", "access", "list"}, {"upstream", "server", "show"}, {"tunnel", "status"}, {"upgrade", "check"}, {"update", "check"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := prepareCommand(cmd, nil); err != nil {
			t.Fatalf("read-only command denied: %v: %v", path, err)
		}
	}
	for _, path := range [][]string{{"install"}, {"upgrade"}, {"update"}, {"serve"}, {"up"}, {"down"}, {"_service", "run"}, {"logs", "clear", "--force"}, {"config", "set"}, {"config", "export"}, {"config", "import"}, {"auth", "mcp", "create"}, {"request", "approve"}, {"request", "deny"}, {"workspace", "access", "add"}, {"upstream", "server", "add"}, {"tunnel", "enable"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		err = prepareCommand(cmd, nil)
		if err == nil || !strings.Contains(err.Error(), "control-plane command denied") {
			t.Fatalf("mutating command was not denied: %v: %v", path, err)
		}
	}
}

func TestMCPToolContextAllowsExactOneShotRuntimeApproval(t *testing.T) {
	manager, capability := mintToolContextCapability(t, []string{"config", "set", "http.mcp.port", "41001"})
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	t.Setenv(controlplane.ToolContextEnv, "1")
	t.Setenv(controlplane.ControlApprovalEnv, capability)
	previous := processCommandArgs
	processCommandArgs = func() []string { return []string{"config", "set", "http.mcp.port", "41001"} }
	defer func() { processCommandArgs = previous }()
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"config", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareCommand(cmd, []string{"http.mcp.port", "41001"}); err != nil {
		t.Fatalf("exact approved command denied: %v", err)
	}
	if err := prepareCommand(cmd, []string{"http.mcp.port", "41001"}); err == nil || !strings.Contains(err.Error(), "approval verification failed") {
		t.Fatalf("replayed capability was accepted: %v", err)
	}
}

func TestMCPToolContextAllowsAliasedOneShotRuntimeApproval(t *testing.T) {
	aliasArgs := []string{"cfg", "set", "http.mcp.port", "41001"}
	manager, capability := mintToolContextCapability(t, aliasArgs)
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	t.Setenv(controlplane.ToolContextEnv, "1")
	t.Setenv(controlplane.ControlApprovalEnv, capability)
	previous := processCommandArgs
	processCommandArgs = func() []string { return append([]string(nil), aliasArgs...) }
	defer func() { processCommandArgs = previous }()
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"cfg", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareCommand(cmd, []string{"http.mcp.port", "41001"}); err != nil {
		t.Fatalf("approved aliased command denied: %v", err)
	}
	if err := prepareCommand(cmd, []string{"http.mcp.port", "41001"}); err == nil || !strings.Contains(err.Error(), "approval verification failed") {
		t.Fatalf("aliased one-shot capability was replayable: %v", err)
	}
}

func TestMCPToolContextApprovalMismatchDoesNotBurnCapability(t *testing.T) {
	manager, capability := mintToolContextCapability(t, []string{"config", "set", "http.mcp.port", "41001"})
	control, err := startRuntimeControl(runtimeControlOptions{Approvals: manager, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	t.Setenv(controlplane.ToolContextEnv, "1")
	t.Setenv(controlplane.ControlApprovalEnv, capability)
	actual := []string{"config", "set", "http.mcp.port", "41002"}
	previous := processCommandArgs
	processCommandArgs = func() []string { return append([]string(nil), actual...) }
	defer func() { processCommandArgs = previous }()
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"config", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareCommand(cmd, []string{"http.mcp.port", "41002"}); err == nil || !strings.Contains(err.Error(), "approval verification failed") {
		t.Fatalf("mismatch was accepted: %v", err)
	}
	actual = []string{"config", "set", "http.mcp.port", "41001"}
	if err := prepareCommand(cmd, []string{"http.mcp.port", "41001"}); err != nil {
		t.Fatalf("exact retry after mismatch denied: %v", err)
	}
}

func mintToolContextCapability(t *testing.T, cliArgs []string) (*approval.Manager, string) {
	t.Helper()
	manager := approval.NewManager("instance-test")
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{CallerID: "session-a", SessionHash: "hash-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm " + strings.Join(cliArgs, " ")}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "denied", Title: "Allow command"})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Approve(request.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	_, capability, matched, err := manager.ClaimApprovedCLI(approval.RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm " + strings.Join(cliArgs, " ")}}, approval.CLIInvocation{Program: "cm", Args: cliArgs})
	if err != nil || !matched || capability == "" {
		t.Fatalf("mint capability=%q matched=%t err=%v", capability, matched, err)
	}
	return manager, capability
}
