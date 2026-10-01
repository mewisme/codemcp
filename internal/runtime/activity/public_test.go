package activity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPublicActivityProjectionPreservesStableFieldsAndDropsPrivatePayload(t *testing.T) {
	const secret = "activity-secret-marker"
	raw := Event{
		Sequence: 9, CallID: "call_1", Kind: string(EventToolCall), Phase: "finish",
		Method: "tools/call", Source: "tunnel", Tool: "run_command", WorkspaceID: "ws_1",
		SessionHash: secret, SessionAccess: "manage", SessionWorkspaceCount: 4,
		ReceivedByInstanceID: "receiver_private", ExecutedByInstanceID: "executor_private",
		Status: "success", DurationMS: 17, Message: "Authorization: Bearer " + secret,
		Raw:       map[string]any{"token": secret, "provider_payload": map[string]any{"secret": secret}},
		Timestamp: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC),
	}

	public := PublicEvent(raw)
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, forbidden := range []string{secret, "session_hash", "session_access", "session_workspace_count", "received_by_instance_id", "executed_by_instance_id", "provider_payload", `"raw"`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("public activity leaked %q: %s", forbidden, encoded)
		}
	}
	for _, want := range []string{"call_1", "tool_call", "tools/call", "run_command", "ws_1", "success", "2026-09-28T08:00:00Z"} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("public activity lost %q: %s", want, encoded)
		}
	}
	if raw.SessionHash == "" || raw.Raw == nil || raw.ReceivedByInstanceID == "" {
		t.Fatalf("public projection mutated runtime truth: %#v", raw)
	}
}

func TestPublicToolCallDetailKeepsRequestAndResponseDistinctAndSanitized(t *testing.T) {
	const secret = "tool-detail-secret-marker"
	detail := ToolCallDetail{
		Event: Event{
			Sequence: 11, CallID: "call_detail", Kind: string(EventToolCall), Phase: "finish",
			Method: "tools/call", Source: "tunnel", Tool: "run_command", WorkspaceID: "ws_1",
			SessionHash: secret, ReceivedByInstanceID: "receiver_private", ExecutedByInstanceID: "executor_private",
			Status: "ok", Raw: map[string]any{"secret": secret}, Timestamp: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		},
		Request: map[string]any{
			"tool":      "run_command",
			"arguments": map[string]any{"command": "printf request", "authorization": "Bearer " + secret},
		},
		Response: map[string]any{
			"stdout": "response-only",
			"token":  secret,
		},
	}

	public := PublicToolCallDetail(detail)
	request, ok := public.Request.(map[string]any)
	if !ok {
		t.Fatalf("request projection=%#v", public.Request)
	}
	response, ok := public.Response.(map[string]any)
	if !ok {
		t.Fatalf("response projection=%#v", public.Response)
	}
	if _, ok := request["stdout"]; ok {
		t.Fatalf("request contains response payload: %#v", request)
	}
	if _, ok := response["arguments"]; ok {
		t.Fatalf("response contains request payload: %#v", response)
	}
	if !public.Diagnostic.Redacted {
		t.Fatalf("detail did not report redaction: %#v", public.Diagnostic)
	}
	arguments, ok := request["arguments"].(map[string]any)
	if !ok || arguments["authorization"] != "<redacted>" || response["token"] != "<redacted>" {
		t.Fatalf("detail redaction mismatch request=%#v response=%#v", request, response)
	}

	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, forbidden := range []string{secret, "session_hash", "received_by_instance_id", "executed_by_instance_id", `"raw"`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("public tool detail leaked %q: %s", forbidden, encoded)
		}
	}
	for _, want := range []string{"printf request", "response-only", "call_detail"} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("public tool detail lost %q: %s", want, encoded)
		}
	}
}
