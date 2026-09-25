package telemetry

import (
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtime/activity"
)

func TestAttachApprovalsPublishesSafeActivityAndVisibleRequestNotice(t *testing.T) {
	manager := approval.NewManager("instance-test")
	stream := activity.NewStream()
	var output lockedApprovalBuffer
	detach := AttachApprovals(manager, stream, logger.NewWithWriter(logger.Info, &output))
	defer detach()
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{SessionID: "session-secret", SessionHash: "hash-session", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --secret value"}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow cm update"})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "session-secret", "ws_test")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), "Control approval requested") && len(stream.Recent(10)) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if text := output.String(); !strings.Contains(text, "Control approval requested") || strings.Contains(text, "--secret") || strings.Contains(text, "session-secret") {
		t.Fatalf("approval log=%q", text)
	}
	events := stream.Recent(10)
	if len(events) != 2 {
		t.Fatalf("activity=%#v", events)
	}
	var pending activity.Event
	for _, event := range events {
		if event.Raw["event"] == approval.EventPending {
			pending = event
		}
		if raw := event.Raw; raw["arguments"] != nil || raw["title"] != nil || strings.Contains(event.Message, "--secret") {
			t.Fatalf("activity leaked unsafe approval content=%#v", event)
		}
	}
	if pending.Kind != "approval" || pending.WorkspaceID != "ws_test" || pending.SessionHash != "hash-session" || pending.Status != "pending" || pending.Raw["request_id"] != request.ID {
		t.Fatalf("pending activity=%#v all=%#v", pending, events)
	}
}

type lockedApprovalBuffer struct {
	mu   sync.Mutex
	data strings.Builder
}

func (b *lockedApprovalBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(data)
}

func (b *lockedApprovalBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}
