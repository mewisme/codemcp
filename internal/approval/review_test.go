package approval

import (
	"errors"
	"sync"
	"testing"

	"go.mewis.me/codemcp/internal/controlguard"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

func TestReviewServiceCanonicalOperationsAndSimilarGrant(t *testing.T) {
	manager := NewManager("instance-review")
	reviews := NewReviewService(manager)
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller-a", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_a", "command": "git push origin main"},
		GuardCode: controlguard.CodeExternalMutation, GuardReason: "guarded", Title: "Push commits",
		Command: "git push origin main", SimilarCommandPattern: "git push **",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-a", "ws_a", "Push commits")
	if err != nil {
		t.Fatal(err)
	}

	listed, err := reviews.List(Filter{WorkspaceID: "ws_a", Status: StatusPending})
	if err != nil || len(listed) != 1 || listed[0].ID != request.ID {
		t.Fatalf("list=%#v err=%v", listed, err)
	}
	viewed, err := reviews.View(request.ID[:8])
	if err != nil || viewed.ID != request.ID {
		t.Fatalf("view=%#v err=%v", viewed, err)
	}

	approved, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: ReviewApprove, ResolvedBy: "reviewer", Reason: "reviewed", AllowSimilar: true})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != StatusApproved || !approved.RuntimeSessionGrant || approved.GrantExpiresAt.IsZero() || !approved.RetryUntil.IsZero() {
		t.Fatalf("approved=%#v", approved)
	}
	grants, err := reviews.ListGrants("ws_a")
	if err != nil || len(grants) != 1 || grants[0].ID != request.ID {
		t.Fatalf("grants=%#v err=%v", grants, err)
	}
	revoked, err := reviews.RevokeGrant(request.ID[:8])
	if err != nil || revoked.Status != StatusExpired {
		t.Fatalf("revoked=%#v err=%v", revoked, err)
	}
}

func TestReviewServiceRejectsStaleAndDestructiveSimilarGrant(t *testing.T) {
	manager := NewManager("instance-review")
	reviews := NewReviewService(manager)
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller-a", WorkspaceID: "ws_a", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_a", "command": "rm file"},
		GuardCode: controlguard.CodeDestructiveMutation, Title: "Delete file", Command: "rm file", SimilarCommandPattern: "rm **",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-a", "ws_a", "Delete file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: ReviewApprove, ResolvedBy: "reviewer", AllowSimilar: true}); err == nil {
		t.Fatal("destructive similar-command grant unexpectedly allowed")
	}
	current, _ := reviews.View(request.ID)
	if current.Status != StatusPending {
		t.Fatalf("destructive rejection mutated request: %#v", current)
	}
	if _, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: ReviewApprove, ResolvedBy: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: ReviewDeny, ResolvedBy: "other"}); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("stale review err=%v", err)
	}
}

func TestReviewServiceConcurrentResolutionHasSingleWinner(t *testing.T) {
	manager := NewManager("instance-review")
	reviews := NewReviewService(manager)
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller-a", WorkspaceID: "ws_a", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_a", "command": "cm update"},
		GuardCode: controlguard.CodeControlPlaneMutation, Title: "Update CodeMCP",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-a", "ws_a", "Update CodeMCP")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, decision := range []ReviewDecision{ReviewApprove, ReviewDeny} {
		wg.Add(1)
		go func(decision ReviewDecision) {
			defer wg.Done()
			_, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: decision, ResolvedBy: string(decision)})
			errs <- err
		}(decision)
	}
	wg.Wait()
	close(errs)
	success, stale := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrRequestResolved):
			stale++
		default:
			t.Fatalf("unexpected review error: %v", err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("success=%d stale=%d", success, stale)
	}
	final, err := reviews.View(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusApproved && final.Status != StatusDenied {
		t.Fatalf("final=%#v", final)
	}
}

func TestReviewPayloadCannotWeakenRetryBinding(t *testing.T) {
	manager := NewManager("instance-review")
	reviews := NewReviewService(manager)
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller-a", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_a", "command": "cm update"},
		GuardCode: controlguard.CodeControlPlaneMutation, Title: "Update CodeMCP",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-a", "ws_a", "Update CodeMCP")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: ReviewApprove, ResolvedBy: "reviewer", Reason: "approved"}); err != nil {
		t.Fatal(err)
	}
	if matched, ok, err := manager.MatchApproved(RetryInput{CallerID: "caller-other", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_a", "command": "cm update"}}); err != nil || ok || matched.ID != "" {
		t.Fatalf("different caller matched review grant: request=%#v matched=%t err=%v", matched, ok, err)
	}
	if _, ok, err := manager.MatchApproved(RetryInput{CallerID: "caller-a", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_a", "command": "cm update --changed"}}); ok || err == nil {
		t.Fatalf("changed arguments weakened retry binding: matched=%t err=%v", ok, err)
	}
	final, err := reviews.View(request.ID)
	if err != nil || final.Status != StatusApproved {
		t.Fatalf("mismatch changed approved truth: request=%#v err=%v", final, err)
	}
}

func TestConfigSetReviewNeverCreatesRuntimeSessionGrant(t *testing.T) {
	manager := NewManager("instance-review")
	reviews := NewReviewService(manager)
	arguments := map[string]any{
		"workspace_id":             "ws_a",
		"changes":                  []any{map[string]any{"key": "http.mcp.port", "value": "41001"}},
		"__codemcp_config_binding": map[string]any{"version": 1, "config_root": "/private/root", "config_fingerprint": "private-fingerprint"},
	}
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller-a", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: mcpconfigwire.SetToolName,
		Arguments: arguments, GuardCode: controlguard.CodeControlPlaneMutation, Title: "Update CodeMCP settings",
		SimilarCommandPattern: "cm config set **",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-a", "ws_a", "Update CodeMCP settings")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: ReviewApprove, ResolvedBy: "reviewer", AllowSimilar: true}); err == nil {
		t.Fatal("config_set unexpectedly accepted a runtime session grant")
	}
	current, err := reviews.View(request.ID)
	if err != nil || current.Status != StatusPending || current.RuntimeSessionGrant {
		t.Fatalf("runtime-grant rejection mutated request: %#v err=%v", current, err)
	}
	if _, err := reviews.Resolve(ReviewInput{Request: request.ID, Decision: ReviewApprove, ResolvedBy: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if granted, ok := manager.MatchRuntimeGrant(RetryInput{
		CallerID: "caller-other", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: mcpconfigwire.SetToolName,
		Arguments: arguments, Command: "cm config set http.mcp.port 41001",
	}); ok || granted.ID != "" {
		t.Fatalf("config_set matched runtime grant: %#v", granted)
	}
}
