package approval

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/controlguard"
)

func TestApprovalLifecycleEventsAreDeduplicatedAndSafe(t *testing.T) {
	manager := NewManager("instance-test")
	const commandSecret = "approval-command-secret-marker"
	challenge, _, err := manager.CreateChallenge(ChallengeInput{SessionID: "session-secret", SessionHash: "hash-session", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --token " + commandSecret}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow cm update"})
	if err != nil {
		t.Fatal(err)
	}
	request, created, err := manager.CreateRequest(challenge.ID, "session-secret", "ws_test")
	if err != nil || !created {
		t.Fatalf("request=%#v created=%t err=%v", request, created, err)
	}
	if _, created, err := manager.CreateRequest(challenge.ID, "session-secret", "ws_test"); err != nil || created {
		t.Fatalf("duplicate created=%t err=%v", created, err)
	}
	events := manager.Events().Recent(10)
	if len(events) != 1 || events[0].Name != EventRequested || events[0].RequestID != request.ID || events[0].SessionHash != "hash-session" {
		t.Fatalf("requested events=%#v", events)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), commandSecret) {
		t.Fatalf("approval notification leaked command secret: %s", encoded)
	}
	if _, err := manager.Approve(request.ID, "admin", "reviewed"); err != nil {
		t.Fatal(err)
	}
	_, _, err = manager.MatchApproved(RetryInput{SessionID: "session-secret", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --changed"}})
	var mismatch *MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("mismatch err=%v", err)
	}
	if _, matched, err := manager.ClaimApproved(RetryInput{SessionID: "session-secret", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --token " + commandSecret}}); err != nil || !matched {
		t.Fatalf("claim matched=%t err=%v", matched, err)
	}
	events = manager.Events().Recent(10)
	want := []string{EventRequested, EventApproved, EventMismatch, EventConsumed}
	if len(events) != len(want) {
		t.Fatalf("events=%#v", events)
	}
	for i, name := range want {
		if events[i].Name != name || events[i].RequestID != request.ID {
			t.Fatalf("event[%d]=%#v", i, events[i])
		}
	}
	encoded, err = json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), commandSecret) {
		t.Fatalf("approval lifecycle notifications leaked command secret: %s", encoded)
	}
}

func TestApprovalExpiryPublishesLifecycleEvent(t *testing.T) {
	manager := NewManager("instance-test")
	now := time.Unix(1_700_000_000, 0).UTC()
	manager.now = func() time.Time { return now }
	challenge, _, err := manager.CreateChallenge(ChallengeInput{SessionID: "session-a", SessionHash: "hash-a", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update"}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Update CodeMCP"})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "session-a", "ws_test")
	if err != nil {
		t.Fatal(err)
	}
	now = request.ExpiresAt
	manager.PurgeExpired()
	events := manager.Events().Recent(10)
	if len(events) != 2 || events[1].Name != EventExpired || events[1].Status != StatusExpired || events[1].RequestID != request.ID {
		t.Fatalf("expiry events=%#v", events)
	}
}
