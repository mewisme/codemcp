package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type managerTestBackend struct {
	mu sync.Mutex

	id       BackendID
	capacity int
	readyErr error

	spawnGate    <-chan struct{}
	spawnStarted chan<- struct{}
	spawnErr     error
	sendErr      error
	snapshotErr  error

	snapshots map[string]BackendSnapshot
	spawns    int
	sends     int
	cancels   int
	closes    int
}

func newManagerTestBackend(id BackendID, capacity int) *managerTestBackend {
	return &managerTestBackend{
		id: id, capacity: capacity,
		snapshots: map[string]BackendSnapshot{},
	}
}

func (backend *managerTestBackend) ID() BackendID { return backend.id }

func (backend *managerTestBackend) Ready(context.Context) (Readiness, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.readyErr != nil {
		return Readiness{}, backend.readyErr
	}
	return Readiness{Available: true, Capacity: Capacity{MaxParallel: backend.capacity}}, nil
}

func (backend *managerTestBackend) Spawn(ctx context.Context, request BackendSpawnRequest) (Handle, error) {
	if err := ValidateBackendSpawnRequest(request); err != nil {
		return nil, err
	}
	backend.mu.Lock()
	backend.spawns++
	gate := backend.spawnGate
	started := backend.spawnStarted
	spawnErr := backend.spawnErr
	backend.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-gate:
		}
	}
	handle := string(request.AgentID)
	backend.mu.Lock()
	backend.snapshots[handle] = BackendSnapshot{Phase: BackendPhaseWorking}
	backend.mu.Unlock()
	if spawnErr != nil {
		return handle, spawnErr
	}
	return handle, nil
}

func (backend *managerTestBackend) Send(_ context.Context, handle Handle, message Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	key, ok := handle.(string)
	if !ok {
		return errors.New("unexpected test handle")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.sends++
	if backend.sendErr != nil {
		return backend.sendErr
	}
	backend.snapshots[key] = BackendSnapshot{Phase: BackendPhaseWorking}
	return nil
}

func (backend *managerTestBackend) Snapshot(_ context.Context, handle Handle) (BackendSnapshot, error) {
	key, ok := handle.(string)
	if !ok {
		return BackendSnapshot{}, errors.New("unexpected test handle")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.snapshotErr != nil {
		return BackendSnapshot{}, backend.snapshotErr
	}
	snapshot, exists := backend.snapshots[key]
	if !exists {
		return BackendSnapshot{}, errors.New("test snapshot not found")
	}
	return snapshot, nil
}

func (backend *managerTestBackend) Cancel(context.Context, Handle) error {
	backend.mu.Lock()
	backend.cancels++
	backend.mu.Unlock()
	return nil
}

func (backend *managerTestBackend) Close(context.Context, Handle) error {
	backend.mu.Lock()
	backend.closes++
	backend.mu.Unlock()
	return nil
}

func (backend *managerTestBackend) setSnapshot(id ID, snapshot BackendSnapshot) {
	backend.mu.Lock()
	backend.snapshots[string(id)] = snapshot
	backend.mu.Unlock()
}

func (backend *managerTestBackend) counts() (spawns, sends, cancels, closes int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.spawns, backend.sends, backend.cancels, backend.closes
}

func newTestManager(t *testing.T, options ManagerOptions, backends ...*managerTestBackend) *Manager {
	t.Helper()
	if options.DefaultBackend == "" && len(backends) > 0 {
		options.DefaultBackend = backends[0].id
	}
	manager, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range backends {
		if err := manager.RegisterBackend(backend); err != nil {
			t.Fatal(err)
		}
	}
	return manager
}

func spawnTestAgent(t *testing.T, manager *Manager, controller Controller, backend BackendID) Snapshot {
	t.Helper()
	snapshot, err := manager.Spawn(t.Context(), controller, ManagedSpawnRequest{Input: SpawnInput{
		WorkspaceID: "ws_test", Prompt: "do bounded work", Backend: backend,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestManagerOwnershipListAndOperatorAuthority(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	ownerA, _ := NewMCPController("session-a")
	ownerB, _ := NewMCPController("session-b")

	a := spawnTestAgent(t, manager, ownerA, "")
	b := spawnTestAgent(t, manager, ownerB, "")
	if _, err := manager.Get(t.Context(), ownerB, a.ID); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("foreign get error=%v", err)
	}
	listA, err := manager.List(t.Context(), ownerA)
	if err != nil || len(listA) != 1 || listA[0].ID != a.ID {
		t.Fatalf("owner A list=%#v err=%v", listA, err)
	}
	all, err := manager.List(t.Context(), OperatorController())
	if err != nil || len(all) != 2 {
		t.Fatalf("operator list=%#v err=%v", all, err)
	}
	if all[0].ID != a.ID && all[0].ID != b.ID {
		t.Fatalf("unexpected operator list=%#v", all)
	}
}

func TestManagerReservesCapacityBeforeBackendSpawnCompletes(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 2)
	backend := newManagerTestBackend("test", 5)
	backend.spawnGate = gate
	backend.spawnStarted = started
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 2}}, backend)
	owner, _ := NewMCPController("session-a")

	type result struct {
		snapshot Snapshot
		err      error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			snapshot, err := manager.Spawn(context.Background(), owner, ManagedSpawnRequest{Input: SpawnInput{
				WorkspaceID: "ws_test", Prompt: "parallel work",
			}})
			results <- result{snapshot: snapshot, err: err}
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("backend spawns did not start")
		}
	}

	if _, err := manager.Spawn(t.Context(), owner, ManagedSpawnRequest{Input: SpawnInput{
		WorkspaceID: "ws_test", Prompt: "must exceed capacity",
	}}); !errors.Is(err, ErrCapacityReached) {
		t.Fatalf("third concurrent spawn error=%v", err)
	}
	close(gate)
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil || result.snapshot.State != StateWorking {
				t.Fatalf("reserved spawn result=%#v err=%v", result.snapshot, result.err)
			}
		case <-time.After(time.Second):
			t.Fatal("reserved spawn did not finish")
		}
	}
	spawns, _, _, _ := backend.counts()
	if spawns != 2 {
		t.Fatalf("backend spawn calls=%d want=2", spawns)
	}
}

func TestManagerEnforcesBackendCapacityWithoutReducingGlobalCapacity(t *testing.T) {
	a := newManagerTestBackend("a", 1)
	b := newManagerTestBackend("b", 2)
	manager := newTestManager(t, ManagerOptions{
		DefaultBackend: "a", GlobalCapacity: Capacity{MaxParallel: 3},
	}, a, b)
	owner, _ := NewMCPController("session-a")

	spawnTestAgent(t, manager, owner, "a")
	if _, err := manager.Spawn(t.Context(), owner, ManagedSpawnRequest{Input: SpawnInput{
		WorkspaceID: "ws_test", Prompt: "second a", Backend: "a",
	}}); !errors.Is(err, ErrCapacityReached) {
		t.Fatalf("second backend-a spawn error=%v", err)
	}
	spawnTestAgent(t, manager, owner, "b")
	spawnTestAgent(t, manager, owner, "b")
	if _, err := manager.Spawn(t.Context(), owner, ManagedSpawnRequest{Input: SpawnInput{
		WorkspaceID: "ws_test", Prompt: "global overflow", Backend: "b",
	}}); !errors.Is(err, ErrCapacityReached) {
		t.Fatalf("global overflow spawn error=%v", err)
	}
}

func TestManagerWaitSendAndTurnRevisionLifecycle(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{
		GlobalCapacity: Capacity{MaxParallel: 5}, PollInterval: time.Millisecond,
	}, backend)
	owner, _ := NewMCPController("session-a")
	spawned := spawnTestAgent(t, manager, owner, "")
	if spawned.State != StateWorking || spawned.Turn != 1 || spawned.Revision < 2 {
		t.Fatalf("spawned=%#v", spawned)
	}

	backend.setSnapshot(spawned.ID, BackendSnapshot{Phase: BackendPhaseIdle, Result: "first"})
	idle, err := manager.Wait(t.Context(), owner, spawned.ID, spawned.Revision, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if idle.State != StateIdle || idle.Result != "first" || idle.Revision <= spawned.Revision {
		t.Fatalf("idle=%#v", idle)
	}

	sending, err := manager.Send(t.Context(), owner, spawned.ID, Message{Content: "follow up"})
	if err != nil {
		t.Fatal(err)
	}
	if sending.State != StateWorking || sending.Turn != 2 || sending.Revision <= idle.Revision {
		t.Fatalf("sending=%#v", sending)
	}
	if _, err := manager.Send(t.Context(), owner, spawned.ID, Message{Content: "too soon"}); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("busy send error=%v", err)
	}

	backend.setSnapshot(spawned.ID, BackendSnapshot{Phase: BackendPhaseIdle, Result: "second"})
	second, err := manager.Wait(t.Context(), owner, spawned.ID, sending.Revision, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != StateIdle || second.Result != "second" || second.Turn != 2 {
		t.Fatalf("second=%#v", second)
	}
	_, sends, _, _ := backend.counts()
	if sends != 1 {
		t.Fatalf("backend sends=%d want=1", sends)
	}
}

func TestAcceptedCompletionStaysPendingUntilFinalBackendSnapshot(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{
		GlobalCapacity: Capacity{MaxParallel: 5}, PollInterval: time.Millisecond,
	}, backend)
	owner, _ := NewMCPController("session-a")
	spawned := spawnTestAgent(t, manager, owner, "")

	event := CompletionEvent{AgentID: spawned.ID, WorkspaceID: "ws_test", Status: StateCompleted}
	if err := manager.ObserveAcceptedCompletion(event); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.Get(t.Context(), owner, spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != StateCompletionPending {
		t.Fatalf("accepted completion became terminal early: %#v", pending)
	}
	if err := manager.ObserveAcceptedCompletion(event); err != nil {
		t.Fatalf("duplicate accepted completion should be idempotent: %v", err)
	}

	backend.setSnapshot(spawned.ID, BackendSnapshot{Phase: BackendPhaseIdle, Result: "final answer"})
	final, err := manager.Wait(t.Context(), owner, spawned.ID, pending.Revision, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != StateCompleted || final.Result != "final answer" {
		t.Fatalf("final=%#v", final)
	}
	_, _, _, closes := backend.counts()
	if closes != 1 {
		t.Fatalf("backend close calls=%d want=1", closes)
	}
	if err := manager.ObserveAcceptedCompletion(event); err != nil {
		t.Fatalf("terminal duplicate completion should be idempotent: %v", err)
	}
}

func TestWaitersWakeOnCompletionRevisionNotification(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{
		GlobalCapacity: Capacity{MaxParallel: 5}, PollInterval: time.Hour,
	}, backend)
	owner, _ := NewMCPController("session-a")
	spawned := spawnTestAgent(t, manager, owner, "")

	const waiters = 12
	results := make(chan Snapshot, waiters)
	errs := make(chan error, waiters)
	var ready sync.WaitGroup
	ready.Add(waiters)
	for range waiters {
		go func() {
			ready.Done()
			snapshot, err := manager.Wait(context.Background(), owner, spawned.ID, spawned.Revision, time.Second)
			if err != nil {
				errs <- err
				return
			}
			results <- snapshot
		}()
	}
	ready.Wait()
	if err := manager.ObserveAcceptedCompletion(CompletionEvent{
		AgentID: spawned.ID, WorkspaceID: "ws_test", Status: StatePartial,
	}); err != nil {
		t.Fatal(err)
	}
	for range waiters {
		select {
		case err := <-errs:
			t.Fatal(err)
		case snapshot := <-results:
			if snapshot.State != StateCompletionPending || snapshot.Revision <= spawned.Revision {
				t.Fatalf("waiter snapshot=%#v", snapshot)
			}
		case <-time.After(time.Second):
			t.Fatal("waiter did not wake on revision change")
		}
	}
}

func TestConcurrentAcceptedCompletionSignalsAreIdempotent(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("session-a")
	spawned := spawnTestAgent(t, manager, owner, "")
	event := CompletionEvent{AgentID: spawned.ID, WorkspaceID: "ws_test", Status: StateBlocked}

	const callers = 20
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.ObserveAcceptedCompletion(event); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	pending, err := manager.Get(t.Context(), owner, spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != StateCompletionPending {
		t.Fatalf("pending=%#v", pending)
	}
}

func TestCancelIsIdempotentAndBlocksFurtherSend(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("session-a")
	spawned := spawnTestAgent(t, manager, owner, "")

	cancelled, err := manager.Cancel(t.Context(), owner, spawned.ID)
	if err != nil || cancelled.State != StateCancelled {
		t.Fatalf("cancelled=%#v err=%v", cancelled, err)
	}
	again, err := manager.Cancel(t.Context(), owner, spawned.ID)
	if err != nil || again.State != StateCancelled {
		t.Fatalf("second cancel=%#v err=%v", again, err)
	}
	if _, err := manager.Send(t.Context(), owner, spawned.ID, Message{Content: "too late"}); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("send after cancel error=%v", err)
	}
	_, _, cancels, closes := backend.counts()
	if cancels != 1 || closes != 1 {
		t.Fatalf("cancel cleanup calls cancel=%d close=%d", cancels, closes)
	}
}

func TestIdleExpiryAndTerminalRetentionAreBounded(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{
		GlobalCapacity: Capacity{MaxParallel: 5},
		IdleTTL:        time.Minute,
		TerminalTTL:    time.Hour,
		MaxTerminal:    2,
		Now:            func() time.Time { return now },
	}, backend)
	owner, _ := NewMCPController("session-a")

	first := spawnTestAgent(t, manager, owner, "")
	backend.setSnapshot(first.ID, BackendSnapshot{Phase: BackendPhaseIdle, Result: "done for now"})
	if _, err := manager.Get(t.Context(), owner, first.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if expired := manager.SweepExpired(t.Context()); expired != 1 {
		t.Fatalf("expired=%d want=1", expired)
	}
	snapshot, err := manager.Get(t.Context(), owner, first.ID)
	if err != nil || snapshot.State != StateExpired {
		t.Fatalf("expired snapshot=%#v err=%v", snapshot, err)
	}

	second := spawnTestAgent(t, manager, owner, "")
	third := spawnTestAgent(t, manager, owner, "")
	if _, err := manager.Cancel(t.Context(), owner, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Cancel(t.Context(), owner, third.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(t.Context(), owner, first.ID); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("oldest terminal record was not evicted by max retention: %v", err)
	}

	now = now.Add(2 * time.Hour)
	list, err := manager.List(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("terminal ttl did not evict records: %#v", list)
	}
}

func TestConcurrentSpawnWaitSendCancelIsRaceSafe(t *testing.T) {
	backend := newManagerTestBackend("test", 32)
	manager := newTestManager(t, ManagerOptions{
		GlobalCapacity: Capacity{MaxParallel: 32}, PollInterval: time.Millisecond,
	}, backend)
	owner, _ := NewMCPController("session-race")

	const count = 20
	agents := make([]Snapshot, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			snapshot, err := manager.Spawn(context.Background(), owner, ManagedSpawnRequest{Input: SpawnInput{
				WorkspaceID: "ws_test", Prompt: fmt.Sprintf("task-%d", index),
			}})
			if err != nil {
				t.Errorf("spawn %d: %v", index, err)
				return
			}
			agents[index] = snapshot
		}(i)
	}
	wg.Wait()

	for i := range agents {
		if agents[i].ID == "" {
			continue
		}
		backend.setSnapshot(agents[i].ID, BackendSnapshot{Phase: BackendPhaseIdle, Result: "idle"})
	}
	for i := range agents {
		if agents[i].ID == "" {
			continue
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			idle, err := manager.Wait(context.Background(), owner, agents[index].ID, agents[index].Revision, time.Second)
			if err != nil {
				t.Errorf("wait %d: %v", index, err)
				return
			}
			if index%2 == 0 {
				if _, err := manager.Send(context.Background(), owner, idle.ID, Message{Content: "more"}); err != nil {
					t.Errorf("send %d: %v", index, err)
				}
			} else {
				if _, err := manager.Cancel(context.Background(), owner, idle.ID); err != nil {
					t.Errorf("cancel %d: %v", index, err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestRepeatedBoundedWaitPollingDoesNotOwnAgentLifetime(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{
		GlobalCapacity: Capacity{MaxParallel: 5}, PollInterval: time.Hour,
	}, backend)
	owner, _ := NewMCPController("session-bounded-wait")
	spawned := spawnTestAgent(t, manager, owner, "")

	started := time.Now()
	for range 3 {
		snapshot, err := manager.Wait(context.Background(), owner, spawned.ID, spawned.Revision, 5*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.State != StateWorking || snapshot.Revision != spawned.Revision {
			t.Fatalf("bounded wait snapshot=%#v", snapshot)
		}
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded waits held parent too long: %s", elapsed)
	}

	backend.setSnapshot(spawned.ID, BackendSnapshot{Phase: BackendPhaseIdle, Result: "after tool rounds"})
	idle, err := manager.Wait(context.Background(), owner, spawned.ID, spawned.Revision, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if idle.State != StateIdle || idle.Result != "after tool rounds" {
		t.Fatalf("idle after repeated polling=%#v", idle)
	}
}

func TestShutdownCancelsActiveHandlesAndPreventsInPlaceResume(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("session-shutdown")
	first := spawnTestAgent(t, manager, owner, "")
	second := spawnTestAgent(t, manager, owner, "")

	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown must be idempotent: %v", err)
	}
	for _, id := range []ID{first.ID, second.ID} {
		snapshot, err := manager.Get(context.Background(), owner, id)
		if err != nil || snapshot.State != StateCancelled || snapshot.Error != "runtime shutdown" {
			t.Fatalf("shutdown snapshot=%#v err=%v", snapshot, err)
		}
	}
	_, _, cancels, closes := backend.counts()
	if cancels != 2 || closes != 2 {
		t.Fatalf("shutdown cleanup cancel=%d close=%d", cancels, closes)
	}
	if _, err := manager.Spawn(context.Background(), owner, ManagedSpawnRequest{Input: SpawnInput{WorkspaceID: "ws_test", Prompt: "must not resume"}}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("spawn after shutdown error=%v", err)
	}

	restarted, err := NewManager(ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	list, err := restarted.List(context.Background(), OperatorController())
	if err != nil || len(list) != 0 {
		t.Fatalf("fresh runtime restored managed agents: %#v err=%v", list, err)
	}
}

func TestShutdownDuringSpawnCancelsLateHandle(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	started := make(chan struct{}, 1)
	gate := make(chan struct{})
	backend.spawnStarted = started
	backend.spawnGate = gate
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("session-shutdown-spawn")

	spawnDone := make(chan error, 1)
	go func() {
		_, err := manager.Spawn(context.Background(), owner, ManagedSpawnRequest{Input: SpawnInput{
			WorkspaceID: "ws_test", Prompt: "shutdown while spawning",
		}})
		spawnDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend spawn did not start")
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(gate)
	select {
	case err := <-spawnDone:
		if err == nil || !strings.Contains(err.Error(), "terminated during spawn") {
			t.Fatalf("spawn after concurrent shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("spawn did not finish after shutdown")
	}
	_, _, cancels, closes := backend.counts()
	if cancels != 1 || closes != 1 {
		t.Fatalf("late handle cleanup cancel=%d close=%d", cancels, closes)
	}
}

func TestCompletionCancelRaceProducesSingleTerminalState(t *testing.T) {
	for range 25 {
		backend := newManagerTestBackend("test", 5)
		manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
		owner, _ := NewMCPController("session-completion-cancel")
		spawned := spawnTestAgent(t, manager, owner, "")

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = manager.ObserveAcceptedCompletion(CompletionEvent{AgentID: spawned.ID, WorkspaceID: "ws_test", Status: StateCompleted})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = manager.Cancel(context.Background(), owner, spawned.ID)
		}()
		close(start)
		wg.Wait()

		snapshot, err := manager.Get(context.Background(), owner, spawned.ID)
		if err != nil || snapshot.State != StateCancelled {
			t.Fatalf("completion/cancel race snapshot=%#v err=%v", snapshot, err)
		}
		_, _, cancels, closes := backend.counts()
		if cancels != 1 || closes != 1 {
			t.Fatalf("completion/cancel cleanup cancel=%d close=%d", cancels, closes)
		}
	}
}

func TestSendExpiryRaceDoesNotDoubleTerminal(t *testing.T) {
	for range 25 {
		now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
		backend := newManagerTestBackend("test", 5)
		manager := newTestManager(t, ManagerOptions{
			GlobalCapacity: Capacity{MaxParallel: 5}, IdleTTL: time.Minute, Now: func() time.Time { return now },
		}, backend)
		owner, _ := NewMCPController("session-send-expiry")
		spawned := spawnTestAgent(t, manager, owner, "")
		backend.setSnapshot(spawned.ID, BackendSnapshot{Phase: BackendPhaseIdle, Result: "idle"})
		idle, err := manager.Get(context.Background(), owner, spawned.ID)
		if err != nil || idle.State != StateIdle {
			t.Fatalf("idle=%#v err=%v", idle, err)
		}
		now = now.Add(2 * time.Minute)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = manager.Send(context.Background(), owner, spawned.ID, Message{Content: "race expiry"})
		}()
		go func() {
			defer wg.Done()
			<-start
			manager.SweepExpired(context.Background())
		}()
		close(start)
		wg.Wait()

		snapshot, err := manager.Get(context.Background(), owner, spawned.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch snapshot.State {
		case StateExpired:
		case StateWorking:
			if _, err := manager.Cancel(context.Background(), owner, spawned.ID); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("send/expiry race state=%s", snapshot.State)
		}
	}
}

func TestClaimCancelRaceAlwaysRevokesChildAuthority(t *testing.T) {
	for range 25 {
		backend := newManagerTestBackend("test", 5)
		manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
		owner, _ := NewMCPController("session-claim-cancel")
		spawned := spawnTestAgent(t, manager, owner, "")
		credential, err := manager.IssueClaim(spawned.ID)
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = manager.ConsumeClaim(spawned.ID, credential.Token(), "child-race-session")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = manager.Cancel(context.Background(), owner, spawned.ID)
		}()
		close(start)
		wg.Wait()

		if binding, ok := manager.SessionBinding("child-race-session"); ok && binding.Active {
			t.Fatalf("cancel left active child binding: %#v", binding)
		}
		if _, err := manager.ConsumeClaim(spawned.ID, credential.Token(), "late-session"); err == nil {
			t.Fatal("cancel left claim reusable")
		}
	}
}
