package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

func TestNewRuntimeRegistersAgentComplete(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	if runtime.Completions == nil {
		t.Fatal("runtime completion service is missing")
	}
	if _, ok := runtime.Registry.Schema(AgentCompleteToolName); !ok {
		t.Fatal("agent_complete is missing from native runtime catalog")
	}
}

func TestAgentCompleteToolSchemaExcludesCallerControlledIdentity(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	schema, ok := runtime.Registry.Schema(AgentCompleteToolName)
	if !ok {
		t.Fatal("agent_complete is not registered")
	}
	if schema.Title != "Complete Agent Work" || !strings.Contains(schema.Description, "final CodeMCP tool call") || !strings.Contains(schema.Description, "does not close the MCP transport") {
		t.Fatalf("schema=%#v", schema)
	}
	var input map[string]any
	if err := json.Unmarshal(schema.InputSchema, &input); err != nil {
		t.Fatal(err)
	}
	properties, _ := input["properties"].(map[string]any)
	if len(properties) != 4 {
		t.Fatalf("properties=%#v", properties)
	}
	for _, forbidden := range []string{
		"agent_id", "caller_id", "generation_id", "session_id", "session_hash",
		"created_at", "timestamp", "sequence", "source", "supersedes_id",
	} {
		if _, exists := properties[forbidden]; exists {
			t.Fatalf("caller-controlled identity/timestamp field %q exposed in schema", forbidden)
		}
	}
	status := properties["status"].(map[string]any)
	if got := strings.Join(anyStringValues(status["enum"]), ","); got != "completed,partial,blocked,cancelled" {
		t.Fatalf("status enum=%q", got)
	}
	if got := properties["title"].(map[string]any)["maxLength"]; got != float64(agentcompletion.MaxTitleRunes) {
		t.Fatalf("title maxLength=%v", got)
	}
	if got := properties["summary"].(map[string]any)["maxLength"]; got != float64(agentcompletion.MaxSummaryBytes) {
		t.Fatalf("summary maxLength=%v", got)
	}
	if readOnly, _ := schema.Annotations["readOnlyHint"].(bool); readOnly {
		t.Fatal("agent_complete must remain a state mutation")
	}
	if idempotent, _ := schema.Annotations["idempotentHint"].(bool); !idempotent {
		t.Fatal("agent_complete duplicate semantics must be advertised as idempotent")
	}
}

func TestAgentCompleteUsesTrustedRuntimeCorrelationAndCanonicalWorkspace(t *testing.T) {
	runtime, workspaceID := newCompletionToolRuntime(t)
	sessionID := "transport-session-must-not-be-authority"
	ctx := WithMCPSessionID(context.Background(), sessionID)
	ctx = WithCallSource(ctx, "tunnel")
	ctx = WithApprovalCorrelation(ctx, "apc_trusted", "apr_request")
	ctx = WithAgentCompletionCorrelation(ctx, "apc_forged", "instance_forged", "forged")

	for _, key := range []string{"agent_id", "caller_id", "generation_id", "session_id", "session_hash", "created_at", "timestamp", "source"} {
		result, err := runtime.Call(ctx, AgentCompleteToolName, map[string]any{
			"workspace_id": workspaceID,
			"status":       "completed",
			"title":        "Finished work",
			key:            "attacker",
		})
		if err == nil && !result.IsError {
			t.Fatalf("caller-controlled field %q unexpectedly accepted: %#v", key, result)
		}
	}
	if recent, err := runtime.Completions.Recent(10); err != nil || len(recent) != 0 {
		t.Fatalf("spoof attempts history=%#v err=%v", recent, err)
	}

	result, err := runtime.Call(ctx, AgentCompleteToolName, map[string]any{
		"workspace_id": workspaceID,
		"status":       "completed",
		"title":        "Finished work",
		"summary":      "Verified",
	})
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	value, ok := result.StructuredContent.(AgentCompleteResult)
	if !ok {
		t.Fatalf("structured content=%T %#v", result.StructuredContent, result.StructuredContent)
	}
	wantAgentID := agentcompletion.DeriveAgentID("apc_trusted", runtime.runtimeInstanceID())
	if !value.Created || value.Record.WorkspaceID != workspaceID || value.Record.AgentID != wantAgentID || value.Record.Source != "tunnel" {
		t.Fatalf("completion=%#v want_agent=%s", value, wantAgentID)
	}
	if strings.Contains(value.Record.AgentID, sessionID) {
		t.Fatalf("completion identity leaked raw transport session: %#v", value.Record)
	}
}

func TestAgentCompleteDuplicateCallsRemainDomainIdempotent(t *testing.T) {
	runtime, workspaceID := newCompletionToolRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "session-duplicate")
	ctx = WithCallSource(ctx, "stdio")
	ctx = WithApprovalCorrelation(ctx, "apc_duplicate", "apr_duplicate")
	sub, _ := runtime.Completions.SubscribeSnapshot(0)
	defer runtime.Completions.Unsubscribe(sub)

	args := map[string]any{
		"workspace_id": workspaceID,
		"status":       "blocked",
		"title":        "Waiting for access",
		"summary":      "A concrete credential is required",
	}
	var firstID string
	for index := 0; index < 4; index++ {
		result, err := runtime.Call(ctx, AgentCompleteToolName, args)
		if err != nil || result.IsError {
			t.Fatalf("call %d result=%#v err=%v", index, result, err)
		}
		value := result.StructuredContent.(AgentCompleteResult)
		if index == 0 {
			if !value.Created {
				t.Fatalf("first call created=false: %#v", value)
			}
			firstID = value.Record.ID
		} else if value.Created || value.Record.ID != firstID {
			t.Fatalf("duplicate call %d=%#v first=%s", index, value, firstID)
		}
	}
	recent, err := runtime.Completions.Recent(10)
	if err != nil || len(recent) != 1 || recent[0].ID != firstID {
		t.Fatalf("history=%#v err=%v", recent, err)
	}
	select {
	case event := <-sub.Events:
		if event.Name != agentcompletion.EventAccepted || event.Record.ID != firstID {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("canonical completion event missing")
	}
	select {
	case event := <-sub.Events:
		t.Fatalf("duplicate completion event=%#v", event)
	default:
	}
}

func TestAgentCompleteRejectsMalformedInputDeterministically(t *testing.T) {
	runtime, workspaceID := newCompletionToolRuntime(t)
	ctx := WithMCPSessionID(context.Background(), "session-invalid")
	ctx = WithCallSource(ctx, "http")
	ctx = WithApprovalCorrelation(ctx, "apc_invalid", "apr_invalid")

	cases := []map[string]any{
		nil,
		{},
		{"workspace_id": workspaceID, "status": "running", "title": "No"},
		{"workspace_id": workspaceID, "status": "completed", "title": strings.Repeat("x", agentcompletion.MaxTitleRunes+1)},
		{"workspace_id": workspaceID, "status": "completed", "title": "Done", "summary": strings.Repeat("x", agentcompletion.MaxSummaryBytes+1)},
		{"workspace_id": workspaceID, "status": "completed", "title": "Done", "summary": 42},
		{"workspace_id": workspaceID, "status": "completed", "title": 42},
		{"workspace_id": workspaceID, "status": "completed", "title": "Done", "unexpected": true},
		{"workspace_id": "ws_missing", "status": "completed", "title": "Done"},
	}
	for index, args := range cases {
		first, firstErr := runtime.Call(ctx, AgentCompleteToolName, args)
		second, secondErr := runtime.Call(ctx, AgentCompleteToolName, args)
		if firstErr == nil && !first.IsError {
			t.Fatalf("case %d unexpectedly succeeded: %#v", index, first)
		}
		if secondErr == nil && !second.IsError {
			t.Fatalf("case %d second call unexpectedly succeeded: %#v", index, second)
		}
		if completionErrorText(first, firstErr) != completionErrorText(second, secondErr) {
			t.Fatalf("case %d nondeterministic errors: first=%q second=%q", index, completionErrorText(first, firstErr), completionErrorText(second, secondErr))
		}
	}
	recent, err := runtime.Completions.Recent(10)
	if err != nil || len(recent) != 0 {
		t.Fatalf("invalid calls created history=%#v err=%v", recent, err)
	}

	untrusted := WithAgentCompletionCorrelation(context.Background(), "apc_forged", "instance_forged", "forged")
	result, err := runtime.Call(untrusted, AgentCompleteToolName, map[string]any{
		"workspace_id": workspaceID, "status": "completed", "title": "Done",
	})
	if err == nil && !result.IsError {
		t.Fatalf("missing trusted runtime correlation unexpectedly succeeded: %#v", result)
	}
}

func TestCompletionProductionSignalHasSingleAuthoritativeWriter(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	var writers []string
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "agentcompletion.Input{") {
			writers = append(writers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(writers) != 1 || filepath.Base(writers[0]) != "agent_completion_tool.go" {
		t.Fatalf("completion writers=%v; only agent_complete may construct authoritative completion input", writers)
	}
}

func TestProjectContextIncludesTerminalCompletionGuidance(t *testing.T) {
	runtime, workspaceID := newCompletionToolRuntime(t)
	result, err := runtime.Call(context.Background(), "project_context", map[string]any{
		"workspace_id": workspaceID,
		"include_git":  false,
	})
	if err != nil || result.IsError {
		t.Fatalf("project_context=%#v err=%v", result, err)
	}
	project, ok := result.StructuredContent.(ProjectContextResult)
	if !ok {
		t.Fatalf("structured content=%T %#v", result.StructuredContent, result.StructuredContent)
	}
	for _, expected := range []string{
		"agent_complete is available in the current tool profile",
		"finish verification first",
		"final CodeMCP tool call",
		"completed only when",
		"partial",
		"blocked",
		"cancelled",
		"MCP disconnect",
		"never successful completion",
		"does not close the MCP transport",
	} {
		if !strings.Contains(project.InstructionContext.AgentWorkflow, expected) {
			t.Fatalf("project workflow missing %q: %s", expected, project.InstructionContext.AgentWorkflow)
		}
	}
}

func newCompletionToolRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return runtime, item.ID
}

func anyStringValues(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		result = append(result, text)
	}
	return result
}

func completionErrorText(result Result, err error) string {
	if err != nil {
		return err.Error()
	}
	if len(result.Content) > 0 {
		return result.Content[0].Text
	}
	return ""
}
