package backgroundcontinuation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/controlguard"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

type lifecycleHarness struct {
	workspaceID string
	processes   *shellruntime.ProcessManager
	executions  *shellruntime.ExecutionHub
	broker      *backgrounddelivery.Broker
}

func newLifecycleHarness(t *testing.T) *lifecycleHarness {
	t.Helper()
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	registry := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := registry.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executions := shellruntime.NewExecutionHub()
	shell := shellruntime.NewManagerWithExecutions(registry, filepath.Join(t.TempDir(), "shell-state"), executions)
	processes := shellruntime.NewProcessManagerWithExecutions(registry, shell, executions)
	broker := backgrounddelivery.New(processes)
	t.Cleanup(func() {
		broker.Close()
		executions.Close()
		processes.CloseSubscriptions()
	})
	return &lifecycleHarness{workspaceID: item.ID, processes: processes, executions: executions, broker: broker}
}

func (h *lifecycleHarness) start(t *testing.T, owner backgrounddelivery.Owner, processName string, delay time.Duration) shellruntime.StartResult {
	t.Helper()
	command := delayedCompletionCommand(delay, processName)
	ctx := shellruntime.WithExecutionMetadata(context.Background(), shellruntime.ExecutionMetadata{
		Source: "behavior-test",
		CallID: "call_" + processName,
	})
	started, err := h.processes.Start(ctx, h.workspaceID, command)
	if err != nil {
		t.Fatal(err)
	}
	if !h.broker.RegisterStart(backgrounddelivery.Registration{
		WorkspaceID: h.workspaceID,
		ProcessID:   started.ID,
		ExecutionID: started.ExecutionID,
		CallID:      "call_" + processName,
		Owner:       owner,
	}) {
		t.Fatal("background delivery registration was rejected")
	}
	return started
}

func delayedCompletionCommand(delay time.Duration, marker string) string {
	if os.PathSeparator == '\\' {
		milliseconds := max(int(delay.Milliseconds()), 1)
		return fmt.Sprintf("Start-Sleep -Milliseconds %d; Write-Output %q", milliseconds, marker)
	}
	seconds := float64(delay) / float64(time.Second)
	return fmt.Sprintf("sleep %.3f; printf '%%s' %q", seconds, marker)
}

func waitForDelivery(t *testing.T, events <-chan backgrounddelivery.Delivery, processID string) backgrounddelivery.Delivery {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case delivery, ok := <-events:
			if !ok {
				t.Fatal("background delivery stream closed")
			}
			if delivery.ProcessID == processID {
				return delivery
			}
		case <-timeout.C:
			t.Fatalf("terminal delivery for %s was not published", processID)
		}
	}
}

func TestLifecycleStartIndependentWorkAndAutomaticContinuationWithoutPolling(t *testing.T) {
	h := newLifecycleHarness(t)
	owner := backgrounddelivery.Owner{ID: "owner-independent", Generation: "generation-independent"}
	adapter := &testAdapter{
		id: "reference-capable", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
		committed:  make(chan Request, 1),
	}
	coordinator := &Coordinator{Broker: h.broker, Adapter: adapter, CoalesceWindow: -1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, h.workspaceID) }()

	workRelease := make(chan struct{})
	workDone := make(chan struct{})
	go func() {
		<-workRelease
		close(workDone)
	}()
	started := h.start(t, owner, "independent", 75*time.Millisecond)

	var request Request
	select {
	case request = <-adapter.delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("reference capable harness did not receive automatic continuation")
	}
	if request.Mode != ModeIdleContinuation || len(request.Completions) != 1 || request.Completions[0].ProcessID != started.ID {
		t.Fatalf("continuation request=%#v", request)
	}
	select {
	case <-workDone:
		t.Fatal("independent foreground work unexpectedly completed before background continuation")
	default:
	}
	close(workRelease)
	<-workDone
	select {
	case <-adapter.committed:
	case <-time.After(time.Second):
		t.Fatal("automatic continuation was not committed")
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	values, err := h.broker.List(h.workspaceID, owner)
	if err != nil || len(values) != 1 || values[0].State != backgrounddelivery.DeliveryCommitted {
		t.Fatalf("delivery truth=%#v err=%v", values, err)
	}
	adapter.mu.Lock()
	requests := len(adapter.requests)
	adapter.mu.Unlock()
	if requests != 1 {
		t.Fatalf("model-visible continuation attempts=%d want=1", requests)
	}
}

func TestLifecycleCompletionDoesNotResolvePendingApproval(t *testing.T) {
	h := newLifecycleHarness(t)
	owner := backgrounddelivery.Owner{ID: "owner-approval", Generation: "generation-approval"}
	approvals := approval.NewManager("behavior-test")
	challenge, _, err := approvals.CreateChallenge(approval.ChallengeInput{
		CallerID:    "caller-pending",
		WorkspaceID: h.workspaceID,
		Source:      "behavior-test",
		TargetTool:  "run_command",
		Arguments:   map[string]any{"workspace_id": h.workspaceID, "command": "echo pending"},
		GuardCode:   controlguard.CodeShellExecution,
		Title:       "Pending user approval",
		Command:     "echo pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := approvals.CreateRequest(challenge.ID, "caller-pending", h.workspaceID)
	if err != nil {
		t.Fatal(err)
	}

	adapter := &testAdapter{
		id: "approval-independent", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		committed:  make(chan Request, 1),
	}
	coordinator := &Coordinator{Broker: h.broker, Adapter: adapter, CoalesceWindow: -1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, h.workspaceID) }()
	h.start(t, owner, "approval_pending", 50*time.Millisecond)

	select {
	case <-adapter.committed:
	case <-time.After(2 * time.Second):
		t.Fatal("background completion was blocked by pending approval")
	}
	current, ok := approvals.Get(pending.ID)
	if !ok || current.Status != approval.StatusPending {
		t.Fatalf("background completion changed approval truth: %#v ok=%t", current, ok)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLifecycleDisconnectReplacementAndAuthorizedReconnectAreOwnerFenced(t *testing.T) {
	h := newLifecycleHarness(t)
	owner := backgrounddelivery.Owner{ID: "owner-reconnect", Generation: "generation-a"}
	replacement := backgrounddelivery.Owner{ID: owner.ID, Generation: "generation-b"}
	events := h.broker.Subscribe()
	defer h.broker.Unsubscribe(events)

	disconnectedAdapter := &testAdapter{
		id: "disconnected", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
	}
	disconnected := &Coordinator{Broker: h.broker, Adapter: disconnectedAdapter, CoalesceWindow: -1}
	disconnectedCtx, disconnect := context.WithCancel(context.Background())
	disconnectedDone := make(chan error, 1)
	go func() { disconnectedDone <- disconnected.Run(disconnectedCtx, h.workspaceID) }()

	started := h.start(t, owner, "reconnect", 100*time.Millisecond)
	disconnect()
	if err := <-disconnectedDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	replacementAdapter := &testAdapter{
		id: "replacement", owner: replacement,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
	}
	replacementCoordinator := &Coordinator{Broker: h.broker, Adapter: replacementAdapter, CoalesceWindow: -1}
	replacementCtx, replacementCancel := context.WithCancel(context.Background())
	replacementDone := make(chan error, 1)
	go func() { replacementDone <- replacementCoordinator.Run(replacementCtx, h.workspaceID) }()

	delivery := waitForDelivery(t, events, started.ID)
	select {
	case request := <-replacementAdapter.delivered:
		t.Fatalf("replacement owner received stale completion: %#v", request)
	case <-time.After(25 * time.Millisecond):
	}
	replacementCancel()
	if err := <-replacementDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	reconnectedAdapter := &testAdapter{
		id: "reconnected", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 1),
		committed:  make(chan Request, 1),
	}
	reconnected := &Coordinator{Broker: h.broker, Adapter: reconnectedAdapter, CoalesceWindow: -1}
	reconnectedCtx, reconnectedCancel := context.WithCancel(context.Background())
	defer reconnectedCancel()
	reconnectedDone := make(chan error, 1)
	go func() { reconnectedDone <- reconnected.Run(reconnectedCtx, h.workspaceID) }()
	select {
	case request := <-reconnectedAdapter.delivered:
		if len(request.Completions) != 1 || request.Completions[0].DeliveryID != delivery.ID {
			t.Fatalf("authorized reconnect request=%#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("authorized reconnect did not reclaim pending completion")
	}
	select {
	case <-reconnectedAdapter.committed:
	case <-time.After(time.Second):
		t.Fatal("reconnected delivery was not committed")
	}
	reconnectedCancel()
	if err := <-reconnectedDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLifecycleExplicitRecoveryAndAutomaticDeliveryRaceHaveSingleWinner(t *testing.T) {
	h := newLifecycleHarness(t)
	owner := backgrounddelivery.Owner{ID: "owner-recovery", Generation: "generation-recovery"}
	events := h.broker.Subscribe()
	defer h.broker.Unsubscribe(events)

	first := h.start(t, owner, "recovery_wins", 40*time.Millisecond)
	firstDelivery := waitForDelivery(t, events, first.ID)
	recovery, err := h.broker.ConsumeRecovery(h.workspaceID, owner, firstDelivery.ID)
	if err != nil || !recovery.Consumed {
		t.Fatalf("recovery result=%#v err=%v", recovery, err)
	}
	output, err := h.processes.Output(h.workspaceID, first.ID, 1024)
	if err != nil || output.Running || strings.TrimSpace(output.Stdout) != "recovery_wins" {
		t.Fatalf("single explicit recovery inspection=%#v err=%v", output, err)
	}

	adapter := &testAdapter{
		id: "automatic", owner: owner,
		capability: Capability{IdleContinuation: true},
		deliver:    DeliverResult{Outcome: OutcomeDelivered},
		commit:     DeliverResult{Outcome: OutcomeDelivered},
		delivered:  make(chan Request, 2),
		committed:  make(chan Request, 2),
	}
	coordinator := &Coordinator{Broker: h.broker, Adapter: adapter, CoalesceWindow: -1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx, h.workspaceID) }()
	select {
	case request := <-adapter.delivered:
		t.Fatalf("automatic delivery ignored recovery suppression: %#v", request)
	case <-time.After(25 * time.Millisecond):
	}

	second := h.start(t, owner, "automatic_wins", 40*time.Millisecond)
	secondDelivery := waitForDelivery(t, events, second.ID)
	select {
	case request := <-adapter.delivered:
		if len(request.Completions) != 1 || request.Completions[0].DeliveryID != secondDelivery.ID {
			t.Fatalf("automatic winner request=%#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("automatic continuation did not win second delivery")
	}
	select {
	case <-adapter.committed:
	case <-time.After(time.Second):
		t.Fatal("automatic continuation was not committed")
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	recovery, err = h.broker.ConsumeRecovery(h.workspaceID, owner, secondDelivery.ID)
	if err != nil || !recovery.AlreadyDelivered || recovery.Consumed {
		t.Fatalf("post-automatic recovery=%#v err=%v", recovery, err)
	}
}
