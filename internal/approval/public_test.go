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

func TestPublicRequestDropsSessionAndSanitizesGenericArguments(t *testing.T) {
	const secret = "approval-secret-marker"
	raw := Request{
		ID: "req_1", Status: StatusPending, WorkspaceID: "ws_a", SessionHash: secret,
		Source: "tunnel", TargetTool: "run_command",
		Arguments: json.RawMessage(`{"workspace_id":"ws_a","token":"approval-secret-marker","command":"curl -H \"Authorization: Bearer approval-secret-marker\" example.test"}`),
		Command:   "curl -H \"Authorization: Bearer " + secret + "\" example.test",
		Title:     "Run command", GuardReason: "Authorization: Bearer " + secret,
	}
	public := PublicRequest(raw)
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	if strings.Contains(encoded, secret) || strings.Contains(encoded, "session_hash") {
		t.Fatalf("public approval leaked private data: %s", data)
	}
	if !strings.Contains(encoded, "req_1") || !strings.Contains(encoded, "ws_a") || !strings.Contains(encoded, "run_command") {
		t.Fatalf("public approval lost stable safe fields: %s", data)
	}
	if raw.SessionHash == "" || !strings.Contains(string(raw.Arguments), secret) {
		t.Fatalf("public projection mutated private approval truth: %#v", raw)
	}
}

func TestPublicApprovalEventDropsInternalCorrelationIdentity(t *testing.T) {
	value := PublicEvent(Event{Sequence: 3, Name: EventPending, Subject: EventSubjectRequest, ChallengeID: "challenge_private", RequestID: "req_1", WorkspaceID: "ws_a", SessionHash: "private-session", TargetTool: "run_command"})
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-session") || strings.Contains(string(data), "session_hash") || strings.Contains(string(data), "challenge_private") || strings.Contains(string(data), "challenge_id") {
		t.Fatalf("public approval event leaked internal correlation identity: %s", data)
	}
	if !strings.Contains(string(data), "req_1") || !strings.Contains(string(data), "ws_a") {
		t.Fatalf("public approval event lost stable identity: %s", data)
	}
}
