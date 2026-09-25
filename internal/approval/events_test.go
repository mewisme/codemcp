package approval

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/controlguard"
)

func TestApprovalLifecycleEventsAreSequencedDeduplicatedAndSafe(t *testing.T) {
	manager := NewManager("instance-test")
	const (
		commandSecret = "approval-command-secret-marker"
		titleSecret   = "approval-title-secret-marker"
		reasonSecret  = "approval-reason-secret-marker"
	)
	input := ChallengeInput{
		SessionID: "session-secret", SessionHash: "hash-session", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --token " + commandSecret},
		GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow " + titleSecret,
	}
	challenge, created, err := manager.CreateChallenge(input)
	if err != nil || !created {
		t.Fatalf("challenge=%#v created=%t err=%v", challenge, created, err)
	}
	if duplicate, created, err := manager.CreateChallenge(input); err != nil || created || duplicate.ID != challenge.ID {
		t.Fatalf("duplicate challenge=%#v created=%t err=%v", duplicate, created, err)
	}
	request, created, err := manager.CreateRequest(challenge.ID, "session-secret", "ws_test")
	if err != nil || !created {
		t.Fatalf("request=%#v created=%t err=%v", request, created, err)
	}
	if duplicate, created, err := manager.CreateRequest(challenge.ID, "session-secret", "ws_test"); err != nil || created || duplicate.ID != request.ID {
		t.Fatalf("duplicate request=%#v created=%t err=%v", duplicate, created, err)
	}
	if _, err := manager.Approve(request.ID, "admin", reasonSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Approve(request.ID, "admin", reasonSecret); err != nil {
		t.Fatal(err)
	}
	_, _, err = manager.MatchApproved(RetryInput{
		SessionID: "session-secret", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --changed"},
	})
	var mismatch *MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("mismatch err=%v", err)
	}
	if _, matched, err := manager.ClaimApproved(RetryInput{
		SessionID: "session-secret", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --token " + commandSecret},
	}); err != nil || !matched {
		t.Fatalf("claim matched=%t err=%v", matched, err)
	}

	events := manager.Events().Recent(10)
	want := []struct {
		name    string
		subject EventSubject
	}{
		{EventCreated, EventSubjectChallenge},
		{EventPending, EventSubjectRequest},
		{EventApproved, EventSubjectRequest},
		{EventClaimed, EventSubjectRequest},
	}
	if len(events) != len(want) {
		t.Fatalf("events=%#v", events)
	}
	for i, expected := range want {
		event := events[i]
		if event.Sequence != uint64(i+1) || event.Name != expected.name || event.Subject != expected.subject {
			t.Fatalf("event[%d]=%#v want=%#v", i, event, expected)
		}
		if i == 0 {
			if event.ChallengeID != challenge.ID || event.RequestID != "" {
				t.Fatalf("created correlation=%#v", event)
			}
		} else if event.RequestID != request.ID || event.ChallengeID != challenge.ID {
			t.Fatalf("request correlation=%#v", event)
		}
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{commandSecret, titleSecret, reasonSecret} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("approval lifecycle leaked secret %q: %s", secret, encoded)
		}
	}
	for _, unsafeKey := range []string{"arguments", "command", "title", "reason", "guard_reason"} {
		if strings.Contains(string(encoded), "\""+unsafeKey+"\"") {
			t.Fatalf("approval lifecycle exposed unsafe field %q: %s", unsafeKey, encoded)
		}
	}
}

func TestApprovalLifecyclePublishesDeniedExpiredAndRevoked(t *testing.T) {
	deniedManager := NewManager("instance-denied")
	deniedChallenge, _, err := deniedManager.CreateChallenge(testChallenge("session-denied", "ws_denied", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	deniedRequest, _, err := deniedManager.CreateRequest(deniedChallenge.ID, "session-denied", "ws_denied")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deniedManager.Deny(deniedRequest.ID, "admin", "no"); err != nil {
		t.Fatal(err)
	}
	assertApprovalEvent(t, deniedManager.Events().Recent(10), EventDenied, EventSubjectRequest, deniedRequest.ID)

	expiredManager := NewManager("instance-expired")
	now := time.Unix(1_700_000_000, 0).UTC()
	expiredManager.now = func() time.Time { return now }
	expiredChallenge, _, err := expiredManager.CreateChallenge(testChallenge("session-expired", "ws_expired", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	expiredRequest, _, err := expiredManager.CreateRequest(expiredChallenge.ID, "session-expired", "ws_expired")
	if err != nil {
		t.Fatal(err)
	}
	now = expiredRequest.ExpiresAt
	expiredManager.PurgeExpired()
	assertApprovalEvent(t, expiredManager.Events().Recent(10), EventExpired, EventSubjectRequest, expiredRequest.ID)
	assertApprovalChallengeEvent(t, expiredManager.Events().Recent(10), EventExpired, expiredChallenge.ID)

	grantManager := NewManager("instance-grant")
	grantChallenge, _, err := grantManager.CreateChallenge(ChallengeInput{
		SessionID: "session-grant", WorkspaceID: "ws_grant", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_grant", "command": "git push origin main"},
		GuardCode: controlguard.CodeExternalMutation, GuardReason: "guarded", Title: "Push Git commits",
		Command: "git push origin main", SimilarCommandPattern: "git push **",
	})
	if err != nil {
		t.Fatal(err)
	}
	grantRequest, _, err := grantManager.CreateRequest(grantChallenge.ID, "session-grant", "ws_grant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grantManager.ApproveRuntimeSession(grantRequest.ID, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := grantManager.RevokeRuntimeGrant(grantRequest.ID); err != nil {
		t.Fatal(err)
	}
	assertApprovalEvent(t, grantManager.Events().Recent(10), EventRevoked, EventSubjectGrant, grantRequest.ID)
}

func TestApprovalEventStreamSnapshotBarrierAndDedup(t *testing.T) {
	stream := newEventStreamWithLimits(3, 2)
	for i := 1; i <= 3; i++ {
		stream.Publish(Event{Name: EventPending, Subject: EventSubjectRequest, RequestID: fmt.Sprintf("req_%d", i), WorkspaceID: "ws_a", TargetTool: "run_command", Status: StatusPending})
	}
	duplicate := Event{Name: EventPending, Subject: EventSubjectRequest, RequestID: "req_3", WorkspaceID: "ws_a", TargetTool: "run_command", Status: StatusPending}
	stream.Publish(duplicate)
	stream.Publish(Event{Name: EventPending, Subject: EventSubjectRequest, RequestID: "req_other", WorkspaceID: "ws_b", TargetTool: "run_command", Status: StatusPending})

	sub, snapshot := stream.SubscribeWorkspaceSnapshot("ws_a", 2)
	defer stream.Unsubscribe(sub)
	if snapshot.LatestSequence != 4 || len(snapshot.Events) != 2 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Events[0].RequestID != "req_2" || snapshot.Events[0].Sequence != 2 || snapshot.Events[1].RequestID != "req_3" || snapshot.Events[1].Sequence != 3 {
		t.Fatalf("snapshot events=%#v", snapshot.Events)
	}

	stream.Publish(Event{Name: EventApproved, Subject: EventSubjectRequest, RequestID: "req_3", WorkspaceID: "ws_a", TargetTool: "run_command", Status: StatusApproved})
	event := <-sub.Events
	if event.Sequence != 5 || event.Name != EventApproved || event.RequestID != "req_3" {
		t.Fatalf("post-barrier event=%#v", event)
	}
}

func TestApprovalSubscriberOverflowCannotChangeRequestTruth(t *testing.T) {
	manager := NewManager("instance-overflow")
	sub := manager.Events().Subscribe()
	defer manager.Events().Unsubscribe(sub)

	const requests = 10
	for i := 0; i < requests; i++ {
		sessionID := fmt.Sprintf("session-%d", i)
		workspaceID := fmt.Sprintf("ws_%d", i)
		challenge, _, err := manager.CreateChallenge(testChallenge(sessionID, workspaceID, "cm update"))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := manager.CreateRequest(challenge.ID, sessionID, workspaceID); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case overflow := <-sub.Overflow:
		if overflow.DroppedSequence == 0 {
			t.Fatalf("overflow=%#v", overflow)
		}
	default:
		t.Fatal("stalled subscriber did not report overflow")
	}
	pending := manager.List(Filter{Status: StatusPending})
	if len(pending) != requests {
		t.Fatalf("pending requests=%d want=%d", len(pending), requests)
	}
	for _, request := range pending {
		if request.Status != StatusPending {
			t.Fatalf("request truth changed after subscriber overflow: %#v", request)
		}
	}
}

func assertApprovalEvent(t *testing.T, events []Event, name string, subject EventSubject, requestID string) {
	t.Helper()
	for _, event := range events {
		if event.Name == name && event.Subject == subject && event.RequestID == requestID {
			return
		}
	}
	t.Fatalf("missing %s/%s for %s: %#v", name, subject, requestID, events)
}

func assertApprovalChallengeEvent(t *testing.T, events []Event, name, challengeID string) {
	t.Helper()
	for _, event := range events {
		if event.Name == name && event.Subject == EventSubjectChallenge && event.ChallengeID == challengeID {
			return
		}
	}
	t.Fatalf("missing %s challenge %s: %#v", name, challengeID, events)
}
