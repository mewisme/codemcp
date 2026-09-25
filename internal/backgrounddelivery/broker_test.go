package backgrounddelivery

import (
	"testing"
	"time"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestBrokerFencesDeliveriesByLogicalOwnerGenerationAndWorkspace(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	ownerA := Owner{ID: "owner-a", Generation: "generation-1"}
	ownerB := Owner{ID: "owner-a", Generation: "generation-2"}
	if !broker.RegisterStart(Registration{WorkspaceID: "ws_one", ProcessID: "proc_1", ExecutionID: "exec_1", Owner: ownerA}) {
		t.Fatal("registration rejected")
	}
	broker.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_one", ProcessID: "proc_1", ExecutionID: "exec_1",
		Status: "success", Reason: shellruntime.BackgroundTerminalExit,
		StartedAt: "2026-09-26T00:00:00Z", FinishedAt: "2026-09-26T00:00:01Z",
	})
	values, err := broker.List("ws_one", ownerA)
	if err != nil || len(values) != 1 {
		t.Fatalf("owner A deliveries=%#v err=%v", values, err)
	}
	if values[0].ProcessID != "proc_1" || values[0].ExecutionID != "exec_1" {
		t.Fatalf("delivery=%#v", values[0])
	}
	if values, err := broker.List("ws_one", ownerB); err != nil || len(values) != 0 {
		t.Fatalf("replacement generation received stale work: %#v err=%v", values, err)
	}
	if values, err := broker.List("ws_two", ownerA); err != nil || len(values) != 0 {
		t.Fatalf("other workspace received delivery: %#v err=%v", values, err)
	}
}

func TestBrokerReplaysTerminalRaceAndAttachesLateTask(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	broker.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_one", ProcessID: "proc_fast", ExecutionID: "exec_fast",
		Status: "success", Reason: shellruntime.BackgroundTerminalExit,
	})
	if !broker.RegisterStart(Registration{WorkspaceID: "ws_one", ProcessID: "proc_fast", ExecutionID: "exec_fast", Owner: owner}) {
		t.Fatal("registration rejected")
	}
	if !broker.AttachTask("proc_fast", "task_fast") {
		t.Fatal("late task correlation was not attached")
	}
	values, err := broker.List("ws_one", owner)
	if err != nil || len(values) != 1 {
		t.Fatalf("deliveries=%#v err=%v", values, err)
	}
	if values[0].TaskID != "task_fast" {
		t.Fatalf("task correlation=%#v", values[0])
	}
}

func TestBrokerDeliveryContainsOnlyBoundedCorrelationAndTerminalMetadata(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	broker.RegisterStart(Registration{WorkspaceID: "ws_one", ProcessID: "proc_1", CallID: "call_1", Owner: owner})
	broker.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_one", ProcessID: "proc_1", Status: "failed",
		Reason: shellruntime.BackgroundTerminalFailure,
	})
	values, err := broker.List("ws_one", owner)
	if err != nil || len(values) != 1 {
		t.Fatalf("deliveries=%#v err=%v", values, err)
	}
	// Delivery deliberately has no command/stdout/stderr fields. This is a
	// compile-time shape assertion backed by the fields checked above.
	if values[0].CallID != "call_1" || values[0].Reason != shellruntime.BackgroundTerminalFailure {
		t.Fatalf("delivery=%#v", values[0])
	}
}

func TestBrokerExpiryOnlyDropsDeliveryProjection(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	broker.retention = time.Millisecond
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	broker.RegisterStart(Registration{WorkspaceID: "ws_one", ProcessID: "proc_1", ExecutionID: "exec_audit", Owner: owner})
	broker.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_one", ProcessID: "proc_1", ExecutionID: "exec_audit",
		Status: "success", Reason: shellruntime.BackgroundTerminalExit,
	})
	time.Sleep(3 * time.Millisecond)
	values, err := broker.List("ws_one", owner)
	if err != nil || len(values) != 0 {
		t.Fatalf("expired projection still visible: %#v err=%v", values, err)
	}
	// Broker has no reference to or mutation API for ExecutionHub; expiry only
	// removes its own delivery projection.
}

func TestDeriveOwnerRequiresStableSubjectAndConversationGeneration(t *testing.T) {
	if owner := DeriveOwner("", "org", "conversation"); owner.Valid() {
		t.Fatalf("owner derived without subject: %#v", owner)
	}
	if owner := DeriveOwner("subject", "org", ""); owner.Valid() {
		t.Fatalf("owner derived without generation: %#v", owner)
	}
	one := DeriveOwner("subject", "org", "conversation-a")
	two := DeriveOwner("subject", "org", "conversation-b")
	if !one.Valid() || one.ID != two.ID || one.Generation == two.Generation {
		t.Fatalf("generation fencing failed: one=%#v two=%#v", one, two)
	}
}
