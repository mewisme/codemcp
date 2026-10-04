package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToolLoopGuardBlocksExactContextDuplicates(t *testing.T) {
	guard := NewToolLoopGuard()
	args := map[string]any{"workspace_id": "ws_a", "path": "internal"}
	if decision := guard.Check("session-a", "load_path_rules", args, toolLoopClassContext); decision.blocked || decision.warn {
		t.Fatalf("first decision=%#v", decision)
	}
	if decision := guard.Check("session-a", "load_path_rules", args, toolLoopClassContext); decision.blocked || !decision.warn {
		t.Fatalf("second decision=%#v", decision)
	}
	if decision := guard.Check("session-a", "load_path_rules", args, toolLoopClassContext); !decision.blocked || decision.reason != "exact_duplicate" {
		t.Fatalf("third decision=%#v", decision)
	}
}

func TestToolLoopGuardBlocksRepeatedCycles(t *testing.T) {
	guard := NewToolLoopGuard()
	for index, name := range []string{"project_context", "load_path_rules", "project_context", "load_path_rules", "project_context"} {
		if decision := guard.Check("session-a", name, map[string]any{"workspace_id": "ws_a"}, toolLoopClassContext); decision.blocked {
			t.Fatalf("call %d blocked early: %#v", index, decision)
		}
	}
	decision := guard.Check("session-a", "load_path_rules", map[string]any{"workspace_id": "ws_a"}, toolLoopClassContext)
	if !decision.blocked || decision.reason != "repeated_cycle_2" {
		t.Fatalf("cycle decision=%#v", decision)
	}
}

func TestToolLoopGuardProgressResetsHistory(t *testing.T) {
	guard := NewToolLoopGuard()
	args := map[string]any{"workspace_id": "ws_a"}
	_ = guard.Check("session-a", "project_context", args, toolLoopClassContext)
	_ = guard.Check("session-a", "project_context", args, toolLoopClassContext)
	guard.MarkProgress("session-a")
	if decision := guard.Check("session-a", "project_context", args, toolLoopClassContext); decision.blocked || decision.warn {
		t.Fatalf("post-progress decision=%#v", decision)
	}
}

func TestToolLoopGuardExemptsPollingTools(t *testing.T) {
	guard := NewToolLoopGuard()
	for index := 0; index < 20; index++ {
		if decision := guard.Check("session-a", "process_output", map[string]any{"id": "proc_a"}, toolLoopClassExempt); decision.blocked || decision.warn {
			t.Fatalf("poll %d decision=%#v", index, decision)
		}
	}
}

func TestToolLoopGuardWarnsThenBlocksDuplicateMutations(t *testing.T) {
	guard := NewToolLoopGuard()
	args := map[string]any{"workspace_id": "ws_a", "path": "file.txt"}
	if decision := guard.Check("session-a", "delete_file", args, toolLoopClassMutation); decision.blocked || decision.warn {
		t.Fatalf("first decision=%#v", decision)
	}
	guard.MarkMutationSuccess("session-a", "delete_file", args)
	if decision := guard.Check("session-a", "delete_file", args, toolLoopClassMutation); decision.blocked || !decision.warn || decision.repeats != 2 {
		t.Fatalf("second decision=%#v", decision)
	}
	guard.MarkMutationSuccess("session-a", "delete_file", args)
	if decision := guard.Check("session-a", "delete_file", args, toolLoopClassMutation); !decision.blocked || decision.reason != "duplicate_mutation" || decision.repeats != 3 {
		t.Fatalf("third decision=%#v", decision)
	}
}

func TestToolLoopGuardDifferentMutationResetsDuplicateStreak(t *testing.T) {
	guard := NewToolLoopGuard()
	firstArgs := map[string]any{"workspace_id": "ws_a", "path": "a.txt"}
	secondArgs := map[string]any{"workspace_id": "ws_a", "path": "b.txt"}
	guard.MarkMutationSuccess("session-a", "delete_file", firstArgs)
	guard.MarkMutationSuccess("session-a", "delete_file", firstArgs)
	guard.MarkMutationSuccess("session-a", "delete_file", secondArgs)
	if decision := guard.Check("session-a", "delete_file", firstArgs, toolLoopClassMutation); decision.blocked || decision.warn || decision.repeats != 1 {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestToolLoopGuardBoundsSessionsAndHashesKeys(t *testing.T) {
	guard := NewToolLoopGuard()
	guard.maxSessions = 2
	now := time.Unix(100, 0)
	guard.now = func() time.Time { return now }
	for _, sessionID := range []string{"session-secret-a", "session-secret-b", "session-secret-c"} {
		_ = guard.Check(sessionID, "project_context", map[string]any{}, toolLoopClassContext)
		now = now.Add(time.Minute)
	}
	if len(guard.sessions) != 2 {
		t.Fatalf("session count = %d", len(guard.sessions))
	}
	if _, exists := guard.sessions[mcpSessionStateKey("session-secret-a")]; exists {
		t.Fatal("oldest loop-guard session was not evicted")
	}
	if _, exists := guard.sessions["session-secret-c"]; exists {
		t.Fatal("raw session id stored as loop-guard map key")
	}
}

func TestRuntimeDuplicateMutationProtectionCountsOnlySuccessfulDispatches(t *testing.T) {
	registry := NewRegistry()
	calls := 0
	registry.MustRegister("edit_probe", Schema{Name: "edit_probe", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: ToolAnnotations(RiskEdit)}, func(context.Context, map[string]any) (Result, error) {
		calls++
		return TextResult("edited"), nil
	})
	runtime := &Runtime{Registry: registry, LoopGuard: NewToolLoopGuard()}
	ctx := WithMCPSessionID(context.Background(), "session-a")
	for index := 0; index < 2; index++ {
		result, err := runtime.Call(ctx, "edit_probe", map[string]any{})
		if err != nil || result.IsError {
			t.Fatalf("mutation %d result=%#v err=%v", index, result, err)
		}
	}
	blocked, err := runtime.Call(ctx, "edit_probe", map[string]any{})
	if err != nil || !blocked.IsError || calls != 2 || len(blocked.Content) == 0 || !strings.Contains(blocked.Content[0].Text, "Duplicate mutation blocked") {
		t.Fatalf("blocked=%#v err=%v calls=%d", blocked, err, calls)
	}
}

func TestInlineApprovalMetadataDoesNotChangeLoopMutationIdentity(t *testing.T) {
	business := map[string]any{"workspace_id": "ws_a", "command": "touch file.txt"}
	withApproval := inlineApprovalArgs(business, "chg_loop_identity", "Touch file")
	stripped, envelope, err := splitInlineApprovalArguments(withApproval)
	if err != nil || envelope == nil {
		t.Fatalf("split inline approval stripped=%#v envelope=%#v err=%v", stripped, envelope, err)
	}
	if _, exists := stripped[InlineApprovalArgumentKey]; exists {
		t.Fatalf("runtime approval metadata remained in loop arguments: %#v", stripped)
	}
	if got, want := toolCallFingerprint("run_command", stripped), toolCallFingerprint("run_command", business); got != want {
		t.Fatalf("inline approval changed loop mutation identity: got=%q want=%q", got, want)
	}
}

func TestRuntimeLoopGuardBlocksContextLoopAndMutationResetsIt(t *testing.T) {
	registry := NewRegistry()
	readSchema := Schema{Name: "project_context", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: ToolAnnotations(RiskRead)}
	registry.MustRegister("project_context", readSchema, func(context.Context, map[string]any) (Result, error) { return TextResult("context"), nil })
	registry.MustRegister("edit_probe", Schema{Name: "edit_probe", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: ToolAnnotations(RiskEdit)}, func(context.Context, map[string]any) (Result, error) { return TextResult("edited"), nil })
	runtime := &Runtime{Registry: registry, LoopGuard: NewToolLoopGuard()}
	ctx := WithMCPSessionID(context.Background(), "session-a")
	for index := 0; index < 2; index++ {
		result, err := runtime.Call(ctx, "project_context", map[string]any{})
		if err != nil || result.IsError {
			t.Fatalf("read %d result=%#v err=%v", index, result, err)
		}
	}
	blocked, err := runtime.Call(ctx, "project_context", map[string]any{})
	if err != nil || !blocked.IsError || len(blocked.Content) == 0 || !strings.Contains(blocked.Content[0].Text, "Tool loop detected") {
		t.Fatalf("blocked=%#v err=%v", blocked, err)
	}
	if result, err := runtime.Call(ctx, "edit_probe", map[string]any{}); err != nil || result.IsError {
		t.Fatalf("mutation result=%#v err=%v", result, err)
	}
	if result, err := runtime.Call(ctx, "project_context", map[string]any{}); err != nil || result.IsError {
		t.Fatalf("post-progress result=%#v err=%v", result, err)
	}
}

func TestRuntimeLoopGuardUsesTrustedControllerWithoutMCPSession(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister("project_context", Schema{Name: "project_context", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: ToolAnnotations(RiskRead)}, func(context.Context, map[string]any) (Result, error) {
		return TextResult("context"), nil
	})
	runtime := &Runtime{Registry: registry, LoopGuard: NewToolLoopGuard()}
	ctx := WithTrustedControllerID(context.Background(), "openai:loop-session")
	for index := 0; index < 2; index++ {
		result, err := runtime.Call(ctx, "project_context", map[string]any{})
		if err != nil || result.IsError {
			t.Fatalf("call %d result=%#v err=%v", index, result, err)
		}
	}
	blocked, err := runtime.Call(ctx, "project_context", map[string]any{})
	if err != nil || !blocked.IsError || len(blocked.Content) == 0 || !strings.Contains(blocked.Content[0].Text, "Tool loop detected") {
		t.Fatalf("controller loop guard blocked=%#v err=%v", blocked, err)
	}
}

func TestToolLoopClassForMixedActionTools(t *testing.T) {
	schema := Schema{Annotations: ToolAnnotations(RiskDestructive)}
	for _, test := range []struct {
		name string
		args map[string]any
		want toolLoopClass
	}{
		{name: "git_branch", args: map[string]any{}, want: toolLoopClassRead},
		{name: "git_branch", args: map[string]any{"action": "switch"}, want: toolLoopClassMutation},
		{name: "git_stash", args: map[string]any{"action": "list"}, want: toolLoopClassRead},
		{name: "git_stash", args: map[string]any{"action": "pop"}, want: toolLoopClassMutation},
		{name: "rewind", args: map[string]any{"action": "preview"}, want: toolLoopClassRead},
		{name: "rewind", args: map[string]any{"action": "restore"}, want: toolLoopClassMutation},
		{name: "node_repl", args: map[string]any{"action": "status"}, want: toolLoopClassRead},
		{name: "node_repl", args: map[string]any{"action": "eval"}, want: toolLoopClassMutation},
	} {
		if got := toolLoopClassFor(test.name, schema, test.args); got != test.want {
			t.Fatalf("%s %#v class=%s want=%s", test.name, test.args, got, test.want)
		}
	}
}
