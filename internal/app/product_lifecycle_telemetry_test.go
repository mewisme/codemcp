package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/configformat"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	"go.mewis.me/codemcp/internal/tools"
)

type lifecycleRecorder struct {
	mu     sync.Mutex
	events []capturedRuntimeProductEvent
}

func (recorder *lifecycleRecorder) Record(_ context.Context, name producttelemetry.EventName, usage producttelemetry.Usage) bool {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, capturedRuntimeProductEvent{name: name, usage: usage})
	recorder.mu.Unlock()
	return false
}

func (*lifecycleRecorder) SetEnabled(bool)       {}
func (*lifecycleRecorder) Close(context.Context) {}

func (recorder *lifecycleRecorder) snapshot() []capturedRuntimeProductEvent {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]capturedRuntimeProductEvent(nil), recorder.events...)
}

func TestApprovalProductUsageUsesOnlyLifecycleCategory(t *testing.T) {
	secret := "SECRET_APPROVAL_VALUE"
	tests := []struct {
		event approval.Event
		name  producttelemetry.EventName
		want  string
		ok    bool
	}{
		{approval.Event{Name: approval.EventPending, Subject: approval.EventSubjectRequest, Status: approval.StatusPending}, producttelemetry.EventApprovalRequested, "pending", true},
		{approval.Event{Name: approval.EventApproved, Subject: approval.EventSubjectRequest, Status: approval.StatusApproved}, producttelemetry.EventApprovalResolved, "approved", true},
		{approval.Event{Name: approval.EventDenied, Subject: approval.EventSubjectRequest, Status: approval.StatusDenied}, producttelemetry.EventApprovalResolved, "denied", true},
		{approval.Event{Name: approval.EventExpired, Subject: approval.EventSubjectRequest, Status: approval.StatusExpired}, producttelemetry.EventApprovalResolved, "expired", true},
		{approval.Event{Name: approval.EventCancelled, Subject: approval.EventSubjectRequest, Status: approval.StatusCancelled}, producttelemetry.EventApprovalResolved, "cancelled", true},
		{approval.Event{Name: approval.EventCreated, Subject: approval.EventSubjectChallenge}, "", "", false},
		{approval.Event{Name: approval.EventClaimed, Subject: approval.EventSubjectRequest}, "", "", false},
	}
	for _, test := range tests {
		test.event.RequestID = "req_" + secret
		test.event.ChallengeID = "challenge_" + secret
		test.event.WorkspaceID = "/private/" + secret
		test.event.SessionHash = "session_" + secret
		test.event.Source = secret
		test.event.TargetTool = "run_command_" + secret
		name, usage, ok := approvalProductUsage(test.event)
		if ok != test.ok || name != test.name {
			t.Fatalf("event=%s name=%q ok=%t", test.event.Name, name, ok)
		}
		if !ok {
			continue
		}
		if usage.Interface != producttelemetry.InterfaceRuntime || usage.Feature != test.want || !usage.OmitDuration || !usage.OmitSuccess {
			t.Fatalf("event=%s usage=%#v", test.event.Name, usage)
		}
		assertSafeLifecycleSerialization(t, name, usage, secret)
	}
}

func TestBackgroundProductUsageUsesOnlyTerminalStatusAndCategory(t *testing.T) {
	secret := "SECRET_BACKGROUND_VALUE"
	event := shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "/private/" + secret,
		ProcessID:   "process_" + secret,
		ExecutionID: "execution_" + secret,
		Tool:        "run_command_" + secret,
		SessionHash: "session_" + secret,
		CallID:      "call_" + secret,
		Status:      shellruntime.ExecutionStatusTimedOut,
		Reason:      shellruntime.BackgroundTerminalTimeout,
		StartedAt:   secret,
		FinishedAt:  secret,
	}
	usage, ok := backgroundProductUsage(event)
	if !ok {
		t.Fatal("canonical terminal event was rejected")
	}
	if usage.Interface != producttelemetry.InterfaceRuntime ||
		usage.Feature != "background.timed_out.timeout" ||
		!usage.OmitDuration || !usage.OmitSuccess {
		t.Fatalf("usage=%#v", usage)
	}
	assertSafeLifecycleSerialization(t, producttelemetry.EventBackgroundCompleted, usage, secret)

	event.Status = "private-" + secret
	if _, ok := backgroundProductUsage(event); ok {
		t.Fatal("unbounded background status was accepted")
	}
}

func TestLifecycleTelemetryFailureDoesNotBlockApprovalResolution(t *testing.T) {
	manager := approval.NewManager("instance-secret")
	recorder := &lifecycleRecorder{}
	bridge := newProductLifecycleTelemetry(recorder, manager, nil)
	bridge.Start(t.Context())
	defer bridge.Stop()

	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		CallerID: "caller", SessionHash: "session-secret", WorkspaceID: "ws-secret",
		Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"command": "echo SECRET"},
		GuardCode: "test", GuardReason: "private", Title: "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "caller", "ws-secret")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.Approve(request.ID, "reviewer", "private reason")
	if err != nil || resolved.Status != approval.StatusApproved {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(recorder.snapshot()) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("lifecycle events=%#v", events)
	}
	if events[0].name != producttelemetry.EventApprovalRequested || events[1].name != producttelemetry.EventApprovalResolved {
		t.Fatalf("lifecycle events=%#v", events)
	}
}

func TestLifecycleTelemetryStopRemovesProcessSubscription(t *testing.T) {
	processes := shellruntime.NewProcessManager(nil, nil)
	recorder := &lifecycleRecorder{}
	bridge := newProductLifecycleTelemetry(recorder, nil, processes)
	bridge.Start(t.Context())
	if got := processes.Diagnostics().TerminalSubscribers; got != 1 {
		t.Fatalf("terminal subscribers=%d want=1", got)
	}
	bridge.Stop()
	if got := processes.Diagnostics().TerminalSubscribers; got != 0 {
		t.Fatalf("terminal subscribers after stop=%d want=0", got)
	}
}

func TestLifecycleTelemetryFailureDoesNotBlockProcessReapingOrBackgroundDelivery(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	runtime := tools.NewRuntime()
	defer runtime.BackgroundDeliveries.Close()
	defer runtime.Processes.CloseSubscriptions()
	defer runtime.Executions.Close()
	defer runtime.Completions.Close()
	defer runtime.CompletionHooks.Stop()

	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recorder := &lifecycleRecorder{}
	bridge := newProductLifecycleTelemetry(recorder, runtime.Approvals, runtime.Processes)
	bridge.Start(t.Context())
	defer bridge.Stop()

	started, err := runtime.Processes.Start(t.Context(), workspace.ID, "printf done")
	if err != nil {
		t.Fatal(err)
	}
	owner := backgrounddelivery.DeriveOwner("subject", "organization", "generation")
	if !runtime.BackgroundDeliveries.RegisterStart(backgrounddelivery.Registration{
		WorkspaceID: workspace.ID,
		ProcessID:   started.ID,
		ExecutionID: started.ExecutionID,
		Owner:       owner,
	}) {
		t.Fatal("background delivery registration failed")
	}

	deadline := time.Now().Add(2 * time.Second)
	var terminal bool
	var deliveries []backgrounddelivery.Delivery
	for time.Now().Before(deadline) {
		statuses, statusErr := runtime.Processes.Status(workspace.ID, started.ID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		terminal = len(statuses) == 1 && !statuses[0].Running
		deliveries, err = runtime.BackgroundDeliveries.List(workspace.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
		if terminal && len(deliveries) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !terminal {
		t.Fatal("background process was not reaped")
	}
	if len(deliveries) != 1 || deliveries[0].State != backgrounddelivery.DeliveryPending {
		t.Fatalf("background deliveries=%#v", deliveries)
	}
	if got := recorder.snapshot(); len(got) != 1 ||
		got[0].name != producttelemetry.EventBackgroundCompleted ||
		got[0].usage.Feature != "background.success.exit" {
		t.Fatalf("telemetry events=%#v", got)
	}
}

func TestLifecycleTelemetryStopDetachesApprovalWorker(t *testing.T) {
	manager := approval.NewManager("instance")
	recorder := &lifecycleRecorder{}
	bridge := newProductLifecycleTelemetry(recorder, manager, nil)
	bridge.Start(t.Context())
	bridge.Stop()

	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		CallerID: "caller", SessionHash: "session", WorkspaceID: "workspace",
		Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"command": "echo later"},
		GuardCode: "test", GuardReason: "guarded", Title: "later",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, "caller", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Deny(request.ID, "reviewer", "later"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if events := recorder.snapshot(); len(events) != 0 {
		t.Fatalf("approval worker remained attached after stop: %#v", events)
	}
}

func assertSafeLifecycleSerialization(t *testing.T, name producttelemetry.EventName, usage producttelemetry.Usage, secret string) {
	t.Helper()
	fields := producttelemetry.EventFields{Interface: usage.Interface, Command: usage.Command, Feature: usage.Feature, ErrorCode: usage.ErrorCode}
	if !usage.OmitDuration {
		value := usage.Duration.Milliseconds()
		fields.DurationMS = &value
	}
	if !usage.OmitSuccess {
		value := usage.Success
		fields.Success = &value
	}
	event, err := producttelemetry.NewEvent(name, producttelemetry.ClientFields{
		AnonymousID: "123e4567-e89b-12d3-a456-426614174000",
		Version:     "1.2.3", OS: "linux", Arch: "amd64",
	}, fields)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) {
		t.Fatalf("serialized telemetry leaked secret: %s", text)
	}
	for _, forbidden := range []string{
		"request_id", "challenge_id", "process_id", "execution_id", "task_id", "delivery_id",
		"workspace_id", "session", "arguments", "output", "target_tool",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("serialized telemetry contains forbidden field %q: %s", forbidden, text)
		}
	}
}
