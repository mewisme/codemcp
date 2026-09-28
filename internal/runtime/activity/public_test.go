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
