package approval

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/controlguard"
)

func TestApprovalReviewerSurfaceCountDoesNotChangeTruth(t *testing.T) {
	for _, reviewers := range []int{0, 1, 4} {
		t.Run(string(rune('0'+reviewers)), func(t *testing.T) {
			manager := NewManager("instance-reviewers")
			subs := make([]*EventSubscription, 0, reviewers)
			for range reviewers {
				sub := manager.Events().Subscribe()
				subs = append(subs, sub)
				defer manager.Events().Unsubscribe(sub)
			}
			request := seedParityRequest(t, manager, "caller-reviewers", "ws_reviewers")
			if request.Status != StatusPending {
				t.Fatalf("request status=%s", request.Status)
			}
			viewed, err := NewReviewService(manager).View(request.ID)
			if err != nil || viewed.Status != StatusPending {
				t.Fatalf("reviewers=%d viewed=%#v err=%v", reviewers, viewed, err)
			}
			for _, sub := range subs {
				event := waitApprovalEvent(t, sub, EventPending)
				if event.RequestID != request.ID {
					t.Fatalf("reviewers=%d event=%#v", reviewers, event)
				}
			}
		})
	}
}

func TestTelegramTUIBrowserResolutionRaceHasOneTerminalTruth(t *testing.T) {
	manager := NewManager("instance-race")
	request := seedParityRequest(t, manager, "caller-race", "ws_race")
	surfaces := []string{"cli", "tui", "telegram", "browser", "admin-api"}
	subs := make([]*EventSubscription, 0, len(surfaces))
	for range surfaces {
		sub := manager.Events().Subscribe()
		subs = append(subs, sub)
		defer manager.Events().Unsubscribe(sub)
	}

	type result struct {
		surface string
		value   Request
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, len(surfaces))
	var wg sync.WaitGroup
	for index, surface := range surfaces {
		wg.Add(1)
		go func(index int, surface string) {
			defer wg.Done()
			<-start
			decision := ReviewApprove
			if index%2 == 1 {
				decision = ReviewDeny
			}
			value, err := NewReviewService(manager).Resolve(ReviewInput{Request: request.ID, Decision: decision, ResolvedBy: surface})
			results <- result{surface: surface, value: value, err: err}
		}(index, surface)
	}
	close(start)
	wg.Wait()
	close(results)

	successes, resolvedErrors := 0, 0
	var winner Request
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.value
			continue
		}
		if errors.Is(result.err, ErrRequestResolved) {
			resolvedErrors++
			continue
		}
		t.Fatalf("%s unexpected error: %v", result.surface, result.err)
	}
	if successes != 1 || resolvedErrors != len(surfaces)-1 {
		t.Fatalf("successes=%d resolved_errors=%d winner=%#v", successes, resolvedErrors, winner)
	}
	final, err := NewReviewService(manager).View(request.ID)
	if err != nil || final.Status != winner.Status || final.ResolvedBy != winner.ResolvedBy {
		t.Fatalf("final=%#v winner=%#v err=%v", final, winner, err)
	}
	expectedEvent := EventApproved
	if final.Status == StatusDenied {
		expectedEvent = EventDenied
	}
	for index, sub := range subs {
		event := waitApprovalEvent(t, sub, expectedEvent)
		if event.RequestID != request.ID || event.Status != final.Status {
			t.Fatalf("%s terminal event=%#v final=%#v", surfaces[index], event, final)
		}
	}
}

func TestApprovalExpiryIsBroadcastAndRemovesPendingState(t *testing.T) {
	manager := NewManager("instance-expiry")
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	request := seedParityRequest(t, manager, "caller-expiry", "ws_expiry")
	sub := manager.Events().Subscribe()
	defer manager.Events().Unsubscribe(sub)

	now = request.ExpiresAt
	pending := manager.List(Filter{Status: StatusPending})
	if len(pending) != 0 {
		t.Fatalf("expired request remained pending: %#v", pending)
	}
	final, err := NewReviewService(manager).View(request.ID)
	if err != nil || final.Status != StatusExpired {
		t.Fatalf("expired request=%#v err=%v", final, err)
	}
	event := waitApprovalEvent(t, sub, EventExpired)
	if event.RequestID != request.ID || event.Status != StatusExpired {
		t.Fatalf("expiry event=%#v", event)
	}
}

func seedParityRequest(t *testing.T, manager *Manager, callerID, workspaceID string) Request {
	t.Helper()
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: callerID, RequestCorrelationID: "parity", SessionHash: "hash-parity",
		WorkspaceID: workspaceID, Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": workspaceID, "command": "echo parity"},
		GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow parity command",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, callerID, workspaceID, "Allow parity command")
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func waitApprovalEvent(t *testing.T, sub *EventSubscription, name string) Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-sub.Events:
			if !ok {
				t.Fatal("approval subscription closed")
			}
			if event.Name == name {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", name)
		}
	}
}
