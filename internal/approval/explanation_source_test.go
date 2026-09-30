package approval

import (
	"reflect"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/controlguard"
)

func TestExplanationSourceContainsOnlyExactCommandLifecycleFacts(t *testing.T) {
	manager := NewManager("instance-explain-source")
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller", RequestCorrelationID: "correlation", SessionHash: "session", WorkspaceID: "ws_a",
		Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"command": "echo exact"},
		GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "agent reason must not be exposed",
		Title: "agent title must not be exposed", Command: "echo exact",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "caller", "ws_a")
	if err != nil {
		t.Fatal(err)
	}
	source, err := manager.ExplanationSource(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source.RequestID != request.ID || source.Command != "echo exact" || source.TargetTool != "run_command" || source.Status != StatusPending {
		t.Fatalf("source=%#v", source)
	}
	typeOf := reflect.TypeOf(source)
	for _, forbidden := range []string{"Title", "Reason", "Arguments", "Digest", "GuardReason", "ResolvedBy", "RetryUntil"} {
		if _, ok := typeOf.FieldByName(forbidden); ok {
			t.Fatalf("ExplanationSource unexpectedly exposes %s", forbidden)
		}
	}
}

func TestExplanationSourceReflectsExpiredRequestLifecycle(t *testing.T) {
	manager := NewManager("instance-explain-expiry")
	now := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	request := createExplanationSourceRequest(t, manager, "echo exact")
	now = request.ExpiresAt.Add(time.Millisecond)
	source, err := manager.ExplanationSource(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Status != StatusExpired {
		t.Fatalf("source status=%q want=%q", source.Status, StatusExpired)
	}
	found := false
	for _, event := range manager.Events().Recent(10) {
		if event.RequestID == request.ID && event.Name == EventExpired {
			found = true
		}
	}
	if !found {
		t.Fatal("expiration did not publish canonical request event")
	}
}

func createExplanationSourceRequest(t *testing.T, manager *Manager, command string) Request {
	t.Helper()
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller", RequestCorrelationID: "correlation", SessionHash: "session", WorkspaceID: "ws_a",
		Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"command": command},
		GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "reason", Title: "title", Command: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "caller", "ws_a")
	if err != nil {
		t.Fatal(err)
	}
	return request
}
