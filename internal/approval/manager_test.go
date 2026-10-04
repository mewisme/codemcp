package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/controlguard"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

func TestManagerCoalescesChallengeAndRequest(t *testing.T) {
	manager, now := testManager()
	first, created, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil || !created {
		t.Fatalf("first challenge = %#v created=%t err=%v", first, created, err)
	}
	*now = now.Add(10 * time.Second)
	second, created, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil || created || second.ID != first.ID || !second.ExpiresAt.Equal(now.Add(DefaultChallengeTTL)) {
		t.Fatalf("second challenge = %#v created=%t err=%v", second, created, err)
	}
	request, created, err := manager.CreateRequest(first.ID, "session-a", "ws_x")
	if err != nil || !created || request.Status != StatusPending || request.Title != "Update CodeMCP" || request.Command != "cm update" {
		t.Fatalf("request = %#v created=%t err=%v", request, created, err)
	}
	reused, created, err := manager.CreateRequest(first.ID, "session-a", "ws_x")
	if err != nil || created || reused.ID != request.ID {
		t.Fatalf("reused request = %#v created=%t err=%v", reused, created, err)
	}
}

func TestCreateRequestRejectsOverlongTitle(t *testing.T) {
	manager, _ := testManager()
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = manager.CreateRequestWithTitle(challenge.ID, "session-a", "ws_x", strings.Repeat("x", 121))
	if err == nil || !strings.Contains(err.Error(), "at most 120") {
		t.Fatalf("error=%v", err)
	}
}

func TestRuntimeSessionGrantCrossesMCPSessionUntilTTL(t *testing.T) {
	manager, now := testManager()
	manager.runtimeGrantTTL = time.Hour
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "mcp-session-a", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_a", "command": "git push origin main"}, GuardCode: controlguard.CodeExternalMutation,
		Title: "Push Git commits", Command: "git push origin main", SimilarCommandPattern: "git push **",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "mcp-session-a", "ws_a")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := manager.ApproveRuntimeSession(request.ID, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !approved.RuntimeSessionGrant || approved.Status != StatusApproved || !approved.RetryUntil.IsZero() || approved.GrantExpiresAt.IsZero() {
		t.Fatalf("approved runtime grant = %#v", approved)
	}
	matched, ok := manager.MatchRuntimeGrant(RetryInput{CallerID: "different-mcp-session", WorkspaceID: "ws_a", TargetTool: "run_command", Command: "git push origin feature"})
	if !ok || matched.ID != request.ID {
		t.Fatalf("runtime grant did not cross MCP session: %#v ok=%t", matched, ok)
	}
	for _, input := range []RetryInput{
		{CallerID: "other", WorkspaceID: "ws_b", TargetTool: "run_command", Command: "git push origin feature"},
		{CallerID: "other", WorkspaceID: "ws_a", TargetTool: "start_process", Command: "git push origin feature"},
		{CallerID: "other", WorkspaceID: "ws_a", TargetTool: "run_command", Command: "git status"},
		{CallerID: "other", WorkspaceID: "ws_a", TargetTool: "run_command", Command: "git push && rm -rf build"},
	} {
		if _, ok := manager.MatchRuntimeGrant(input); ok {
			t.Fatalf("runtime grant matched unexpected input %#v", input)
		}
	}
	*now = now.Add(DefaultRuntimeGrantTTL + time.Second)
	manager.PurgeExpired()
	value, ok := manager.Get(request.ID)
	if !ok || value.Status != StatusExpired {
		t.Fatalf("runtime grant did not expire after TTL: %#v ok=%t", value, ok)
	}
	if _, ok := manager.MatchRuntimeGrant(RetryInput{CallerID: "different-mcp-session", WorkspaceID: "ws_a", TargetTool: "run_command", Command: "git push origin feature"}); ok {
		t.Fatal("expired runtime grant still matched")
	}
}

func TestRuntimeSessionGrantRevoke(t *testing.T) {
	manager, _ := testManager()
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "mcp-session-a", WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_a", "command": "git push origin main"}, GuardCode: controlguard.CodeExternalMutation,
		Title: "Push Git commits", Command: "git push origin main", SimilarCommandPattern: "git push **",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "mcp-session-a", "ws_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ApproveRuntimeSession(request.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	revoked, err := manager.RevokeRuntimeGrant(request.ID)
	if err != nil || revoked.Status != StatusExpired {
		t.Fatalf("revoke = %#v err=%v", revoked, err)
	}
	if _, ok := manager.MatchRuntimeGrant(RetryInput{WorkspaceID: "ws_a", TargetTool: "run_command", Command: "git push origin feature"}); ok {
		t.Fatal("revoked runtime grant still matched")
	}
	if grants := manager.ListRuntimeGrants(""); len(grants) != 0 {
		t.Fatalf("list grants after revoke = %#v", grants)
	}
}

func TestManagerBindsChallengeToSessionAndWorkspace(t *testing.T) {
	manager, _ := testManager()
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][2]string{{"session-b", "ws_x"}, {"session-a", "ws_y"}} {
		if _, _, err := manager.CreateRequest(challenge.ID, input[0], input[1]); !errors.Is(err, ErrChallengeMismatch) {
			t.Fatalf("request %v err=%v", input, err)
		}
	}
}

func TestManagerCreateRequestForTargetValidatesExactChallengeBinding(t *testing.T) {
	manager, _ := testManager()
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	exact := ChallengeRequestInput{
		ChallengeID: challenge.ID, CallerID: "session-a", SessionHash: "hash-session-a",
		WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}, Title: "Apply reviewed update",
	}
	request, created, err := manager.CreateRequestForTarget(exact)
	if err != nil || !created || request.Status != StatusPending || request.Title != "Apply reviewed update" {
		t.Fatalf("exact request=%#v created=%t err=%v", request, created, err)
	}
	reused, created, err := manager.CreateRequestForTarget(exact)
	if err != nil || created || reused.ID != request.ID {
		t.Fatalf("reused request=%#v created=%t err=%v", reused, created, err)
	}
}

func TestManagerCreateRequestForTargetRejectsBindingMismatchBeforeReview(t *testing.T) {
	manager, _ := testManager()
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	base := ChallengeRequestInput{
		ChallengeID: challenge.ID, CallerID: "session-a", SessionHash: "hash-session-a",
		WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}, Title: "Update CodeMCP",
	}
	tests := []struct {
		name string
		want error
		edit func(*ChallengeRequestInput)
	}{
		{name: "caller", want: ErrChallengeCaller, edit: func(input *ChallengeRequestInput) { input.CallerID = "session-b" }},
		{name: "session", want: ErrChallengeSession, edit: func(input *ChallengeRequestInput) { input.SessionHash = "hash-other" }},
		{name: "workspace", want: ErrChallengeWorkspace, edit: func(input *ChallengeRequestInput) { input.WorkspaceID = "ws_y" }},
		{name: "source", want: ErrChallengeSource, edit: func(input *ChallengeRequestInput) { input.Source = "stdio" }},
		{name: "tool", want: ErrChallengeTool, edit: func(input *ChallengeRequestInput) { input.TargetTool = "start_process" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			test.edit(&input)
			if _, _, err := manager.CreateRequestForTarget(input); !errors.Is(err, ErrChallengeMismatch) || !errors.Is(err, test.want) {
				t.Fatalf("binding mismatch err=%v want=%v", err, test.want)
			}
			if pending := manager.List(Filter{Status: StatusPending}); len(pending) != 0 {
				t.Fatalf("binding mismatch created review request: %#v", pending)
			}
		})
	}

	mutated := base
	mutated.Arguments = map[string]any{"workspace_id": "ws_x", "command": "cm update --version v2"}
	if _, _, err = manager.CreateRequestForTarget(mutated); !errors.Is(err, ErrChallengeMismatch) || !errors.Is(err, ErrChallengeArguments) {
		t.Fatalf("argument mismatch err=%v", err)
	}
	if pending := manager.List(Filter{Status: StatusPending}); len(pending) != 0 {
		t.Fatalf("argument mismatch created review request: %#v", pending)
	}
}

func TestManagerCreateRequestForTargetRejectsFakeAndExpiredChallenges(t *testing.T) {
	manager, now := testManager()
	input := ChallengeRequestInput{
		ChallengeID: "chg_missing", CallerID: "session-a", SessionHash: "hash-session-a",
		WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}, Title: "Update CodeMCP",
	}
	if _, _, err := manager.CreateRequestForTarget(input); !errors.Is(err, ErrChallengeNotFound) {
		t.Fatalf("fake challenge err=%v", err)
	}
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	input.ChallengeID = challenge.ID
	*now = challenge.ExpiresAt
	if _, _, err := manager.CreateRequestForTarget(input); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("expired challenge err=%v", err)
	}
	if pending := manager.List(Filter{Status: StatusPending}); len(pending) != 0 {
		t.Fatalf("fake/expired challenge created review request: %#v", pending)
	}
}

func TestManagerAllowsOnlyOneActiveRequestPerSession(t *testing.T) {
	manager, _ := testManager()
	first, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	second, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm install"))
	if _, _, err := manager.CreateRequest(first.ID, "session-a", "ws_x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateRequest(second.ID, "session-a", "ws_x"); !errors.Is(err, ErrSessionRequestActive) {
		t.Fatalf("second active request err=%v", err)
	}
}

func TestManagerApprovalExactRetryAndOneShotConsumption(t *testing.T) {
	manager, _ := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	approved, err := manager.Approve(request.ID, "cli", "reviewed")
	if err != nil || approved.Status != StatusApproved || approved.RetryUntil.IsZero() {
		t.Fatalf("approved = %#v err=%v", approved, err)
	}
	unrelated, matched, err := manager.MatchApproved(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "git_status", Arguments: map[string]any{"workspace_id": "ws_x"}})
	if err != nil || matched || unrelated.ID != "" {
		t.Fatalf("unrelated match = %#v/%t/%v", unrelated, matched, err)
	}
	_, matched, err = manager.MatchApproved(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update --version v2.0.0"}})
	var mismatch *MismatchError
	if matched || !errors.As(err, &mismatch) || mismatch.RequestID != request.ID {
		t.Fatalf("mismatch = matched=%t err=%v typed=%#v", matched, err, mismatch)
	}
	matchedRequest, matched, err := manager.MatchApproved(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"command": "cm update", "workspace_id": "ws_x"}})
	if err != nil || !matched || matchedRequest.ID != request.ID {
		t.Fatalf("exact match = %#v/%t/%v", matchedRequest, matched, err)
	}
	consumed, err := manager.Consume(request.ID)
	if err != nil || consumed.Status != StatusConsumed || consumed.ConsumedAt.IsZero() {
		t.Fatalf("consumed = %#v err=%v", consumed, err)
	}
	if _, err := manager.Consume(request.ID); !errors.Is(err, ErrRequestNotApproved) {
		t.Fatalf("second consume err=%v", err)
	}
	if _, matched, err := manager.MatchApproved(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}}); err != nil || matched {
		t.Fatalf("consumed grant remained active: matched=%t err=%v", matched, err)
	}
}

func TestManagerClaimApprovedIsAtomic(t *testing.T) {
	manager, _ := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	if _, err := manager.Approve(request.ID, "cli", "reviewed"); err != nil {
		t.Fatal(err)
	}
	input := RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}}
	start := make(chan struct{})
	type claimResult struct {
		request Request
		matched bool
		err     error
	}
	results := make(chan claimResult, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, matched, err := manager.ClaimApproved(input)
			results <- claimResult{request: value, matched: matched, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	claimed := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.matched {
			claimed++
			if result.request.Status != StatusConsumed || result.request.ConsumedAt.IsZero() {
				t.Fatalf("claimed request = %#v", result.request)
			}
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed = %d, want 1", claimed)
	}
}

func TestManagerClaimMismatchDoesNotConsumeApproval(t *testing.T) {
	manager, _ := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	if _, err := manager.Approve(request.ID, "cli", "reviewed"); err != nil {
		t.Fatal(err)
	}
	_, matched, err := manager.ClaimApproved(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update --version v2.0.0"}})
	var mismatch *MismatchError
	if matched || !errors.As(err, &mismatch) {
		t.Fatalf("mismatch claim = matched=%t err=%v", matched, err)
	}
	value, ok := manager.Get(request.ID)
	if !ok || value.Status != StatusApproved {
		t.Fatalf("approval was consumed by mismatch: %#v ok=%t", value, ok)
	}
}

func TestManagerResolveRequestIDExactUniquePrefixAndAmbiguity(t *testing.T) {
	manager := NewManager("instance-test")
	manager.requests["req_alpha123456"] = &requestRecord{value: Request{ID: "req_alpha123456", Status: StatusPending}, resolved: make(chan struct{})}
	manager.requests["req_beta123456"] = &requestRecord{value: Request{ID: "req_beta123456", Status: StatusDenied}, resolved: make(chan struct{})}

	exact, err := manager.Resolve("req_alpha123456")
	if err != nil || exact.ID != "req_alpha123456" {
		t.Fatalf("exact resolve = %#v err=%v", exact, err)
	}
	partial, err := manager.Resolve("req_alp")
	if err != nil || partial.ID != "req_alpha123456" {
		t.Fatalf("prefix resolve = %#v err=%v", partial, err)
	}
	if _, err := manager.Resolve("req_"); !errors.Is(err, ErrRequestAmbiguous) {
		t.Fatalf("ambiguous prefix err=%v", err)
	}
	if _, err := manager.Resolve("req_missing"); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("missing prefix err=%v", err)
	}
}

func TestManagerCLICapabilityExactMismatchReplayAndExpiry(t *testing.T) {
	t.Run("exact and replay", func(t *testing.T) {
		manager, _ := testManager()
		challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
		request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
		if _, err := manager.Approve(request.ID, "test", ""); err != nil {
			t.Fatal(err)
		}
		claimed, capability, matched, err := manager.ClaimApprovedCLI(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}}, CLIInvocation{Program: "cm", Args: []string{"update"}})
		if err != nil || !matched || capability == "" || claimed.Status != StatusConsumed {
			t.Fatalf("claim = %#v capability=%q matched=%t err=%v", claimed, capability, matched, err)
		}
		requestID, err := manager.ConsumeCLI(capability, []string{"update"})
		if err != nil || requestID != request.ID {
			t.Fatalf("consume = %q err=%v", requestID, err)
		}
		if _, err := manager.ConsumeCLI(capability, []string{"update"}); !errors.Is(err, ErrCapabilityNotFound) {
			t.Fatalf("replay err=%v", err)
		}
	})

	t.Run("mismatch preserves token", func(t *testing.T) {
		manager, _ := testManager()
		challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
		request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
		_, _ = manager.Approve(request.ID, "test", "")
		_, capability, matched, err := manager.ClaimApprovedCLI(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}}, CLIInvocation{Program: "cm", Args: []string{"update"}})
		if err != nil || !matched {
			t.Fatalf("claim matched=%t err=%v", matched, err)
		}
		_, err = manager.ConsumeCLI(capability, []string{"update", "--version", "v2.0.0"})
		var mismatch *CapabilityMismatchError
		if !errors.As(err, &mismatch) || len(mismatch.Expected) != 1 || mismatch.Expected[0] != "update" {
			t.Fatalf("mismatch=%#v err=%v", mismatch, err)
		}
		if requestID, err := manager.ConsumeCLI(capability, []string{"update"}); err != nil || requestID != request.ID {
			t.Fatalf("exact after mismatch = %q err=%v", requestID, err)
		}
	})

	t.Run("expiry", func(t *testing.T) {
		manager, now := testManager()
		challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
		request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
		_, _ = manager.Approve(request.ID, "test", "")
		_, capability, _, err := manager.ClaimApprovedCLI(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}}, CLIInvocation{Program: "cm", Args: []string{"update"}})
		if err != nil {
			t.Fatal(err)
		}
		*now = now.Add(DefaultCLICapabilityTTL)
		if _, err := manager.ConsumeCLI(capability, []string{"update"}); !errors.Is(err, ErrCapabilityExpired) {
			t.Fatalf("expired capability err=%v", err)
		}
	})
}

func TestManagerPendingAndApprovedExpiry(t *testing.T) {
	manager, now := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	*now = now.Add(DefaultRequestTTL)
	manager.PurgeExpired()
	expired, ok := manager.Get(request.ID)
	if !ok || expired.Status != StatusExpired {
		t.Fatalf("pending expiry = %#v ok=%t", expired, ok)
	}

	challenge, _, _ = manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ = manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	approved, err := manager.Approve(request.ID, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	*now = approved.RetryUntil
	manager.PurgeExpired()
	expired, ok = manager.Get(request.ID)
	if !ok || expired.Status != StatusExpired || expired.Reason != "approved retry window expired" {
		t.Fatalf("approved expiry = %#v ok=%t", expired, ok)
	}
}

func TestManagerChallengeExpiry(t *testing.T) {
	manager, now := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	*now = challenge.ExpiresAt
	if _, _, err := manager.CreateRequest(challenge.ID, "session-a", "ws_x"); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("expired challenge err=%v", err)
	}
}

func TestManagerDenyCancelAndList(t *testing.T) {
	manager, _ := testManager()
	firstChallenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	first, _, _ := manager.CreateRequest(firstChallenge.ID, "session-a", "ws_x")
	denied, err := manager.Deny(first.ID, "cli", "not now")
	if err != nil || denied.Status != StatusDenied || denied.ResolvedBy != "cli" || denied.Reason != "not now" {
		t.Fatalf("denied = %#v err=%v", denied, err)
	}
	secondChallenge, _, _ := manager.CreateChallenge(testChallenge("session-b", "ws_y", "cm install"))
	second, _, _ := manager.CreateRequest(secondChallenge.ID, "session-b", "ws_y")
	cancelled, err := manager.Cancel(second.ID, "mcp", "request context cancelled")
	if err != nil || cancelled.Status != StatusCancelled {
		t.Fatalf("cancelled = %#v err=%v", cancelled, err)
	}
	all := manager.List(Filter{})
	if len(all) != 2 {
		t.Fatalf("list = %#v", all)
	}
	if filtered := manager.List(Filter{WorkspaceID: "ws_x", Status: StatusDenied}); len(filtered) != 1 || filtered[0].ID != first.ID {
		t.Fatalf("filtered = %#v", filtered)
	}
}

func TestManagerPendingLimits(t *testing.T) {
	manager, _ := testManager()
	manager.pendingLimit = 2
	manager.workspacePendingLimit = 1
	first, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if _, _, err := manager.CreateRequest(first.ID, "session-a", "ws_x"); err != nil {
		t.Fatal(err)
	}
	second, _, _ := manager.CreateChallenge(testChallenge("session-b", "ws_x", "cm install"))
	if _, _, err := manager.CreateRequest(second.ID, "session-b", "ws_x"); !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("workspace pending limit err=%v", err)
	}
	third, _, _ := manager.CreateChallenge(testChallenge("session-b", "ws_y", "cm install"))
	if _, _, err := manager.CreateRequest(third.ID, "session-b", "ws_y"); err != nil {
		t.Fatal(err)
	}
	fourth, _, _ := manager.CreateChallenge(testChallenge("session-c", "ws_z", "cm update"))
	if _, _, err := manager.CreateRequest(fourth.ID, "session-c", "ws_z"); !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("global pending limit err=%v", err)
	}
}

func TestManagerConcurrentRequestCreationCoalesces(t *testing.T) {
	manager, _ := testManager()
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	start := make(chan struct{})
	type result struct {
		request Request
		created bool
		err     error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request, created, err := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
			results <- result{request: request, created: created, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	createdCount := 0
	requestID := ""
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		if item.created {
			createdCount++
		}
		if requestID == "" {
			requestID = item.request.ID
		} else if item.request.ID != requestID {
			t.Fatalf("request ids diverged: %s != %s", item.request.ID, requestID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
}

func TestManagerConcurrentExactTargetRequestCreationCoalesces(t *testing.T) {
	manager, _ := testManager()
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	input := ChallengeRequestInput{
		ChallengeID: challenge.ID, CallerID: "session-a", SessionHash: "hash-session-a",
		WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}, Title: "Update CodeMCP",
	}
	const workers = 8
	start := make(chan struct{})
	type result struct {
		request Request
		created bool
		err     error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request, created, err := manager.CreateRequestForTarget(input)
			results <- result{request: request, created: created, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	createdCount := 0
	requestID := ""
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		if item.created {
			createdCount++
		}
		if requestID == "" {
			requestID = item.request.ID
		} else if item.request.ID != requestID {
			t.Fatalf("request ids diverged: %s != %s", item.request.ID, requestID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
}

func TestManagerCreateRequestForTargetConfigSetMismatchStaysValueFreePublicly(t *testing.T) {
	manager, _ := testManager()
	const originalValue = "/private/original"
	const changedValue = "/private/changed"
	arguments := map[string]any{
		"workspace_id": "ws_scope",
		"changes":      []any{map[string]any{"key": "permissions.allow_dirs", "value": originalValue}},
		mcpconfigwire.SetApprovalBindingKey: map[string]any{
			"version": mcpconfigwire.SetApprovalBindingVersion, "config_root": "/private/config",
			"config_fingerprint": "private-fingerprint-a",
		},
	}
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller-a", SessionHash: "session-hash-a", WorkspaceID: "ws_scope", Source: "tunnel",
		TargetTool: mcpconfigwire.SetToolName, Arguments: arguments, GuardCode: controlguard.CodeControlPlaneMutation,
		Title: "Update settings",
	})
	if err != nil {
		t.Fatal(err)
	}
	mutated := map[string]any{
		"workspace_id": "ws_scope",
		"changes":      []any{map[string]any{"key": "permissions.allow_dirs", "value": changedValue}},
		mcpconfigwire.SetApprovalBindingKey: map[string]any{
			"version": mcpconfigwire.SetApprovalBindingVersion, "config_root": "/private/config",
			"config_fingerprint": "private-fingerprint-b",
		},
	}
	_, _, err = manager.CreateRequestForTarget(ChallengeRequestInput{
		ChallengeID: challenge.ID, CallerID: "caller-a", SessionHash: "session-hash-a",
		WorkspaceID: "ws_scope", Source: "tunnel", TargetTool: mcpconfigwire.SetToolName,
		Arguments: mutated, Title: "Update settings",
	})
	if !errors.Is(err, ErrChallengeMismatch) || !errors.Is(err, ErrChallengeArguments) {
		t.Fatalf("config mismatch err=%v", err)
	}
	public := err.Error()
	for _, secret := range []string{originalValue, changedValue, "/private/config", "private-fingerprint-a", "private-fingerprint-b"} {
		if strings.Contains(public, secret) {
			t.Fatalf("config mismatch leaked private value %q: %s", secret, public)
		}
	}
	if pending := manager.List(Filter{Status: StatusPending}); len(pending) != 0 {
		t.Fatalf("config mismatch created review request: %#v", pending)
	}
}

func TestManagerConcurrentResolutionHasSingleWinner(t *testing.T) {
	manager, _ := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, approve := range []bool{true, false} {
		wg.Add(1)
		go func(approve bool) {
			defer wg.Done()
			<-start
			if approve {
				_, err := manager.Approve(request.ID, "test", "")
				results <- err
				return
			}
			_, err := manager.Deny(request.ID, "test", "")
			results <- err
		}(approve)
	}
	close(start)
	wg.Wait()
	close(results)
	success, resolved := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrRequestResolved):
			resolved++
		default:
			t.Fatalf("unexpected resolution error: %v", err)
		}
	}
	if success != 1 || resolved != 1 {
		t.Fatalf("success/resolved = %d/%d", success, resolved)
	}
}

func TestManagerWaitWakesOnApproval(t *testing.T) {
	manager := NewManager("instance-test")
	manager.requestTTL = 5 * time.Second
	challenge, _, err := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = manager.Approve(request.ID, "test", "")
		close(done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resolved, err := manager.Wait(ctx, request.ID)
	if err != nil || resolved.Status != StatusApproved {
		t.Fatalf("wait = %#v err=%v", resolved, err)
	}
	<-done
}

func TestManagerWaitCancellationDetachesPendingRequest(t *testing.T) {
	manager, _ := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := manager.Wait(ctx, request.ID)
	if !errors.Is(err, context.Canceled) || value.Status != StatusPending {
		t.Fatalf("detached wait = %#v err=%v", value, err)
	}
	pending := manager.List(Filter{Status: StatusPending})
	if len(pending) != 1 || pending[0].ID != request.ID {
		t.Fatalf("pending after detached wait = %#v", pending)
	}
	if _, err := manager.Approve(request.ID, "test", "reviewed later"); err != nil {
		t.Fatal(err)
	}
	matched, ok, err := manager.MatchApproved(RetryInput{CallerID: "session-a", WorkspaceID: "ws_x", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_x", "command": "cm update"}})
	if err != nil || !ok || matched.ID != request.ID {
		t.Fatalf("approved detached retry = %#v matched=%t err=%v", matched, ok, err)
	}
}

func TestManagerPendingRequestSurvivesLinkedChallengeExpiry(t *testing.T) {
	manager, now := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("session-a", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "session-a", "ws_x")
	*now = challenge.ExpiresAt
	manager.PurgeExpired()
	value, ok := manager.Get(request.ID)
	if !ok || value.Status != StatusPending {
		t.Fatalf("request after challenge expiry = %#v ok=%t", value, ok)
	}
	if _, err := manager.Approve(request.ID, "test", "reviewed after challenge expiry"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerKeepsPrivateBindingIdentity(t *testing.T) {
	manager, _ := testManager()
	challenge, _, _ := manager.CreateChallenge(testChallenge("raw-secret-session", "ws_x", "cm update"))
	request, _, _ := manager.CreateRequest(challenge.ID, "raw-secret-session", "ws_x")
	if request.callerID != "raw-secret-session" || request.challengeID != challenge.ID || request.Digest == "" {
		t.Fatalf("internal identity missing: %#v", request)
	}
}

func testManager() (*Manager, *time.Time) {
	manager := NewManager("instance-test")
	now := time.Unix(1_700_000_000, 0).UTC()
	manager.now = func() time.Time { return now }
	sequence := 0
	manager.newID = func(prefix string) (string, error) {
		sequence++
		return fmt.Sprintf("%s_%02d", prefix, sequence), nil
	}
	return manager, &now
}

func TestConfigSetApprovedRetryExpiresBeforeLateClaim(t *testing.T) {
	manager, now := testManager()
	arguments := map[string]any{
		"workspace_id":             "ws_a",
		"changes":                  []any{map[string]any{"key": "http.mcp.port", "value": "41001"}},
		"__codemcp_config_binding": map[string]any{"version": 1, "config_root": "/private/root", "config_fingerprint": "fingerprint-a"},
	}
	challenge, _, err := manager.CreateChallenge(ChallengeInput{
		CallerID: "caller-a", RequestCorrelationID: "transport-a", WorkspaceID: "ws_a", Source: "tunnel",
		TargetTool: mcpconfigwire.SetToolName, Arguments: arguments, GuardCode: controlguard.CodeControlPlaneMutation,
		Title: "Update settings",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequestWithCorrelation(challenge.ID, "caller-a", "ws_a", "Update settings")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := manager.Approve(request.ID, "reviewer", "")
	if err != nil {
		t.Fatal(err)
	}
	if approved.RetryUntil.IsZero() {
		t.Fatal("approved config_set request has no retry deadline")
	}
	*now = approved.RetryUntil.Add(time.Nanosecond)
	claimed, matched, err := manager.ClaimApproved(RetryInput{
		CallerID: "caller-a", RequestID: "transport-b", WorkspaceID: "ws_a", Source: "tunnel",
		TargetTool: mcpconfigwire.SetToolName, Arguments: arguments,
	})
	if err != nil || matched || claimed.ID != "" {
		t.Fatalf("late retry claimed approval: claimed=%#v matched=%t err=%v", claimed, matched, err)
	}
	expired, ok := manager.Get(request.ID)
	if !ok || expired.Status != StatusExpired {
		t.Fatalf("late retry status=%#v ok=%t", expired, ok)
	}
}

func testChallenge(sessionID, workspaceID, command string) ChallengeInput {
	title := "Run shell command"
	switch command {
	case "cm update":
		title = "Update CodeMCP"
	case "cm install":
		title = "Install CodeMCP"
	}
	return ChallengeInput{
		CallerID: sessionID, SessionHash: "hash-" + sessionID, WorkspaceID: workspaceID, Source: "tunnel", TargetTool: "run_command",
		Arguments: map[string]any{"workspace_id": workspaceID, "command": command}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "control-plane mutation denied", Title: title, Command: command,
	}
}
