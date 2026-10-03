package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeBackend struct {
	id        BackendID
	readiness Readiness
	readyErr  error
	readyCall int
}

func (backend *fakeBackend) ID() BackendID { return backend.id }
func (backend *fakeBackend) Ready(context.Context) (Readiness, error) {
	backend.readyCall++
	return backend.readiness, backend.readyErr
}
func (*fakeBackend) Spawn(context.Context, BackendSpawnRequest) (Handle, error) {
	return "handle", nil
}
func (*fakeBackend) Send(context.Context, Handle, Message) error { return nil }
func (*fakeBackend) Snapshot(context.Context, Handle) (BackendSnapshot, error) {
	return BackendSnapshot{Phase: BackendPhaseIdle}, nil
}
func (*fakeBackend) Cancel(context.Context, Handle) error { return nil }
func (*fakeBackend) Close(context.Context, Handle) error  { return nil }

var _ Backend = (*fakeBackend)(nil)

func TestResolveBackendUsesExactRequestedOrDefaultWithoutFallback(t *testing.T) {
	readyA := &fakeBackend{id: "a", readiness: Readiness{Available: true, Capacity: Capacity{MaxParallel: 2}}}
	readyB := &fakeBackend{id: "b", readiness: Readiness{Available: true, Capacity: Capacity{MaxParallel: 3}}}
	backends := map[BackendID]Backend{"a": readyA, "b": readyB}

	selected, readiness, err := ResolveBackend(t.Context(), backends, "", "a")
	if err != nil || selected != readyA || readiness.Capacity.MaxParallel != 2 {
		t.Fatalf("default resolution selected=%v readiness=%#v err=%v", selected, readiness, err)
	}
	selected, _, err = ResolveBackend(t.Context(), backends, "b", "a")
	if err != nil || selected != readyB {
		t.Fatalf("requested resolution selected=%v err=%v", selected, err)
	}
	if readyA.readyCall != 1 || readyB.readyCall != 1 {
		t.Fatalf("unexpected readiness calls a=%d b=%d", readyA.readyCall, readyB.readyCall)
	}

	if _, _, err := ResolveBackend(t.Context(), backends, "missing", "a"); !errors.Is(err, ErrBackendNotFound) {
		t.Fatalf("missing requested backend error=%v", err)
	}
	if readyA.readyCall != 1 {
		t.Fatalf("missing requested backend fell back to default: calls=%d", readyA.readyCall)
	}
}

func TestResolveBackendRejectsUnavailableWithoutFallback(t *testing.T) {
	unavailable := &fakeBackend{id: "a", readiness: Readiness{Available: false, Reason: "login required"}}
	ready := &fakeBackend{id: "b", readiness: Readiness{Available: true, Capacity: Capacity{MaxParallel: 1}}}
	backends := map[BackendID]Backend{"a": unavailable, "b": ready}
	if _, _, err := ResolveBackend(t.Context(), backends, "a", "b"); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("unavailable requested backend error=%v", err)
	}
	if ready.readyCall != 0 {
		t.Fatalf("unavailable requested backend fell back: calls=%d", ready.readyCall)
	}
}

func TestCapacityAndBackendSnapshotContracts(t *testing.T) {
	effective, err := EffectiveCapacity(Capacity{MaxParallel: 8}, Capacity{MaxParallel: 5})
	if err != nil || effective.MaxParallel != 5 {
		t.Fatalf("effective capacity=%#v err=%v", effective, err)
	}
	if _, err := EffectiveCapacity(Capacity{}, Capacity{MaxParallel: 1}); err == nil {
		t.Fatal("zero global capacity accepted")
	}
	snapshot, err := NormalizeBackendSnapshot(BackendSnapshot{
		Phase:  BackendPhaseIdle,
		Result: strings.Repeat("r", MaxResultBytes+50),
		Error:  strings.Repeat("e", MaxErrorBytes+50),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Result) != MaxResultBytes || len(snapshot.Error) != MaxErrorBytes {
		t.Fatalf("snapshot not bounded: result=%d error=%d", len(snapshot.Result), len(snapshot.Error))
	}
	if _, err := NormalizeBackendSnapshot(BackendSnapshot{Phase: BackendPhaseFailed}); err == nil {
		t.Fatal("failed snapshot without error accepted")
	}
}

func TestBackendSpawnAndMessageValidation(t *testing.T) {
	request := BackendSpawnRequest{
		AgentID: "agent_0123456789abcdef", WorkspaceID: "ws_test", Prompt: "task", Depth: 1,
	}
	if err := ValidateBackendSpawnRequest(request); err != nil {
		t.Fatal(err)
	}
	request.Depth = 2
	if err := ValidateBackendSpawnRequest(request); err == nil {
		t.Fatal("nested spawn request unexpectedly accepted")
	}
	if err := (Message{Content: "follow up"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Message{Content: strings.Repeat("x", MaxPromptBytes+1)}).Validate(); err == nil {
		t.Fatal("oversized message accepted")
	}
}
