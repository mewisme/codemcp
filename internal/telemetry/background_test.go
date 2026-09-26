package telemetry

import (
	"testing"

	"go.mewis.me/codemcp/internal/runtime/activity"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestBackgroundTerminalProjectsSanitizedActivityEvent(t *testing.T) {
	stream := activity.NewStream()
	publishBackgroundTelemetry(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_activity", ProcessID: "proc_activity", ExecutionID: "exec_activity",
		CallID: "call_activity", SessionHash: "session_hash", Tool: "start_process",
		Status: shellruntime.ExecutionStatusFailed, Reason: shellruntime.BackgroundTerminalFailure,
	}, stream, nil)
	events := stream.Recent(1)
	if len(events) != 1 {
		t.Fatalf("activity events=%#v", events)
	}
	event := events[0]
	if event.Kind != string(activity.EventBackground) || event.Phase != "finish" || event.Status != shellruntime.ExecutionStatusFailed ||
		event.WorkspaceID != "ws_activity" || event.Tool != "start_process" {
		t.Fatalf("background activity=%#v", event)
	}
	for _, forbidden := range []string{"command", "stdout", "stderr", "output"} {
		if _, ok := event.Raw[forbidden]; ok {
			t.Fatalf("background activity exposed %q: %#v", forbidden, event.Raw)
		}
	}
}
