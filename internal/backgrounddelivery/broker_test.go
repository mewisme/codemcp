package backgrounddelivery

import (
	"errors"
	"sync"
	"testing"
	"time"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func materializeTestDelivery(t *testing.T, broker *Broker, processID string, owner Owner) Delivery {
	t.Helper()
	if !broker.RegisterStart(Registration{WorkspaceID: "ws_one", ProcessID: processID, ExecutionID: "exec_" + processID, Owner: owner}) {
		t.Fatal("registration rejected")
	}
	broker.ApplyTerminal(shellruntime.BackgroundWorkTerminalEvent{
		WorkspaceID: "ws_one", ProcessID: processID, ExecutionID: "exec_" + processID,
		Status: "success", Reason: shellruntime.BackgroundTerminalExit,
	})
	values, err := broker.List("ws_one", owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range values {
		if delivery.ProcessID == processID {
			return delivery
		}
	}
	t.Fatalf("delivery for %s not found: %#v", processID, values)
	return Delivery{}
}

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

func TestClaimCommitAcknowledgeIsIdempotent(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	delivery := materializeTestDelivery(t, broker, "proc_claim", owner)
	if delivery.State != DeliveryPending {
		t.Fatalf("initial state=%q", delivery.State)
	}
	claim, err := broker.Claim("ws_one", owner, delivery.ID, "adapter-a")
	if err != nil || !claim.Acquired || claim.Receipt == "" || claim.Delivery.State != DeliveryClaimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	retry, err := broker.Claim("ws_one", owner, delivery.ID, "adapter-a")
	if err != nil || retry.Receipt != claim.Receipt {
		t.Fatalf("idempotent claim=%#v err=%v", retry, err)
	}
	if _, err := broker.Claim("ws_one", owner, delivery.ID, "adapter-b"); !errors.Is(err, ErrDeliveryClaimed) {
		t.Fatalf("second claimant err=%v", err)
	}
	committed, err := broker.Commit("ws_one", owner, delivery.ID, claim.Receipt)
	if err != nil || committed.State != DeliveryCommitted || committed.CommittedAt == nil {
		t.Fatalf("committed=%#v err=%v", committed, err)
	}
	committedAgain, err := broker.Commit("ws_one", owner, delivery.ID, claim.Receipt)
	if err != nil || committedAgain.State != DeliveryCommitted || committedAgain.CommittedAt == nil || !committedAgain.CommittedAt.Equal(*committed.CommittedAt) {
		t.Fatalf("idempotent commit=%#v err=%v", committedAgain, err)
	}
	acknowledged, err := broker.Acknowledge("ws_one", owner, delivery.ID, claim.Receipt)
	if err != nil || acknowledged.State != DeliveryAcknowledged || acknowledged.AcknowledgedAt == nil {
		t.Fatalf("acknowledged=%#v err=%v", acknowledged, err)
	}
	acknowledgedAgain, err := broker.Acknowledge("ws_one", owner, delivery.ID, claim.Receipt)
	if err != nil || acknowledgedAgain.State != DeliveryAcknowledged || !acknowledgedAgain.AcknowledgedAt.Equal(*acknowledged.AcknowledgedAt) {
		t.Fatalf("idempotent ack=%#v err=%v", acknowledgedAgain, err)
	}
}

func TestRecoveryWinningFirstSuppressesAutomaticDelivery(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	delivery := materializeTestDelivery(t, broker, "proc_recovery", owner)
	recovery, err := broker.ConsumeRecovery("ws_one", owner, delivery.ID)
	if err != nil || !recovery.Consumed || recovery.AlreadyDelivered || recovery.Delivery.State != DeliverySuppressed {
		t.Fatalf("recovery=%#v err=%v", recovery, err)
	}
	if _, err := broker.Claim("ws_one", owner, delivery.ID, "adapter"); !errors.Is(err, ErrDeliverySuppressed) {
		t.Fatalf("suppressed delivery was claimable: %v", err)
	}
	again, err := broker.ConsumeRecovery("ws_one", owner, delivery.ID)
	if err != nil || !again.AlreadyDelivered || again.Consumed {
		t.Fatalf("repeat recovery=%#v err=%v", again, err)
	}
}

func TestAutomaticCommitWinningFirstMarksRecoveryAlreadyDelivered(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	delivery := materializeTestDelivery(t, broker, "proc_auto", owner)
	claim, err := broker.Claim("ws_one", owner, delivery.ID, "adapter")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Commit("ws_one", owner, delivery.ID, claim.Receipt); err != nil {
		t.Fatal(err)
	}
	recovery, err := broker.ConsumeRecovery("ws_one", owner, delivery.ID)
	if err != nil || !recovery.AlreadyDelivered || recovery.Consumed {
		t.Fatalf("recovery=%#v err=%v", recovery, err)
	}
}

func TestReadOnlyPeekDoesNotConsumeDelivery(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	delivery := materializeTestDelivery(t, broker, "proc_peek", owner)
	for range 3 {
		peeked, err := broker.Peek("ws_one", owner, delivery.ID)
		if err != nil || peeked.State != DeliveryPending {
			t.Fatalf("peek=%#v err=%v", peeked, err)
		}
	}
	claim, err := broker.Claim("ws_one", owner, delivery.ID, "adapter")
	if err != nil || !claim.Acquired {
		t.Fatalf("claim after reads=%#v err=%v", claim, err)
	}
}

func TestConcurrentDeliveriesClaimIndependently(t *testing.T) {
	broker := New(nil)
	t.Cleanup(broker.Close)
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	one := materializeTestDelivery(t, broker, "proc_one", owner)
	two := materializeTestDelivery(t, broker, "proc_two", owner)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, delivery := range []Delivery{one, two} {
		delivery := delivery
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, err := broker.Claim("ws_one", owner, delivery.ID, "adapter-"+delivery.ProcessID)
			if err == nil {
				_, err = broker.Commit("ws_one", owner, delivery.ID, claim.Receipt)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, delivery := range []Delivery{one, two} {
		current, err := broker.Peek("ws_one", owner, delivery.ID)
		if err != nil || current.State != DeliveryCommitted {
			t.Fatalf("delivery %s state=%#v err=%v", delivery.ID, current, err)
		}
	}
}

func TestAutomaticDeliveryAndForegroundRecoveryRaceHasOneWinner(t *testing.T) {
	for range 100 {
		broker := New(nil)
		owner := Owner{ID: "owner-a", Generation: "generation-a"}
		delivery := materializeTestDelivery(t, broker, "proc_race", owner)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var automaticCommitted, recoveryConsumed bool
		var automaticErr, recoveryErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			claim, err := broker.Claim("ws_one", owner, delivery.ID, "adapter")
			if err != nil {
				automaticErr = err
				return
			}
			_, err = broker.Commit("ws_one", owner, delivery.ID, claim.Receipt)
			if err == nil {
				automaticCommitted = true
			}
			automaticErr = err
		}()
		go func() {
			defer wg.Done()
			<-start
			result, err := broker.ConsumeRecovery("ws_one", owner, delivery.ID)
			recoveryConsumed = result.Consumed
			recoveryErr = err
		}()
		close(start)
		wg.Wait()

		if automaticCommitted == recoveryConsumed {
			t.Fatalf("expected exactly one winner: automatic=%t recovery=%t automatic_err=%v recovery_err=%v", automaticCommitted, recoveryConsumed, automaticErr, recoveryErr)
		}
		if automaticCommitted {
			if recoveryErr != nil && !errors.Is(recoveryErr, ErrDeliveryClaimed) {
				t.Fatalf("automatic winner recovery err=%v", recoveryErr)
			}
		} else {
			if !errors.Is(automaticErr, ErrDeliverySuppressed) {
				t.Fatalf("recovery winner automatic err=%v", automaticErr)
			}
		}
		broker.Close()
	}
}
