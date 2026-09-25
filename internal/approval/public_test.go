package approval

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/controlguard"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

func TestConfigSetPublicProjectionKeepsExactArgumentsPrivate(t *testing.T) {
	manager := NewManager("runtime-test")
	secretValue := "credential-like-value"
	arguments := map[string]any{
		"workspace_id": "ws_scope",
		"changes": []any{
			map[string]any{"key": "server.port", "value": "4000"},
			map[string]any{"key": "permissions.allow_dirs", "value": secretValue},
		},
	}
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller", WorkspaceID: "ws_scope", Source: "http",
		TargetTool: mcpconfigwire.SetToolName, Arguments: arguments,
		GuardCode: controlguard.CodeExternalMutation, GuardReason: "contains " + secretValue,
		Title: "Update " + secretValue, Command: "set " + secretValue, SimilarCommandPattern: "set " + secretValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "caller", "ws_scope")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(request.Arguments), secretValue) || !strings.Contains(string(request.Arguments), "4000") {
		t.Fatalf("private approval binding lost exact arguments: %s", request.Arguments)
	}

	public := PublicRequest(request)
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secretValue) || strings.Contains(string(data), "4000") || strings.Contains(string(public.Arguments), "ws_scope") {
		t.Fatalf("public approval projection leaked config values: %s", data)
	}
	var summary mcpconfigwire.BatchSummary
	if err := json.Unmarshal(public.Arguments, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ChangeCount != 2 || len(summary.Keys) != 2 || summary.Keys[0] != "server.port" || summary.Keys[1] != "permissions.allow_dirs" {
		t.Fatalf("public summary=%#v", summary)
	}

	stored, ok := manager.Get(request.ID)
	if !ok || !strings.Contains(string(stored.Arguments), secretValue) {
		t.Fatalf("public projection mutated manager binding: %#v ok=%t", stored, ok)
	}
}

func TestPublicProjectionPreservesUnrelatedToolArguments(t *testing.T) {
	raw := json.RawMessage(`{"workspace_id":"ws_a","command":"git status"}`)
	value := PublicArguments("run_command", raw)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "git status") || !strings.Contains(string(data), "ws_a") {
		t.Fatalf("unrelated tool arguments changed: %s", data)
	}
}
