package telemetry

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/activity"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/logger"
)

func TestAttachApprovalsPublishesSafeActivityAndVisibleRequestNotice(t *testing.T) {
	manager := approval.NewManager("instance-test")
	stream := activity.NewStream()
	var output bytes.Buffer
	AttachApprovals(manager, stream, logger.NewWithWriter(logger.Info, &output))
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{SessionID: "session-secret", SessionHash: "hash-session", WorkspaceID: "ws_test", Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": "ws_test", "command": "cm update --secret value"}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "guarded", Title: "Allow cm update"})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "session-secret", "ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, "Control approval requested") || strings.Contains(text, "--secret") || strings.Contains(text, "session-secret") {
		t.Fatalf("approval log=%q", text)
	}
	events := stream.Recent(10)
	if len(events) != 1 {
		t.Fatalf("activity=%#v", events)
	}
	event := events[0]
	if event.Kind != "approval" || event.WorkspaceID != "ws_test" || event.SessionHash != "hash-session" || event.Status != "pending" || event.Raw["event"] != approval.EventRequested || event.Raw["request_id"] != request.ID {
		t.Fatalf("activity=%#v", event)
	}
	if raw := event.Raw; raw["arguments"] != nil || strings.Contains(event.Message, "--secret") {
		t.Fatalf("activity leaked arguments=%#v", event)
	}
}
