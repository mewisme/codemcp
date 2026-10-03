package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/tools"
)

type lifecycleClaimBackend struct {
	mu      sync.Mutex
	cancels int
	closes  int
}

func (*lifecycleClaimBackend) ID() managedagent.BackendID { return "lifecycle-claim-test" }
func (*lifecycleClaimBackend) Ready(context.Context) (managedagent.Readiness, error) {
	return managedagent.Readiness{Available: true, Capacity: managedagent.Capacity{MaxParallel: 2}}, nil
}
func (*lifecycleClaimBackend) Spawn(_ context.Context, request managedagent.BackendSpawnRequest) (managedagent.Handle, error) {
	return string(request.AgentID), nil
}
func (*lifecycleClaimBackend) Send(context.Context, managedagent.Handle, managedagent.Message) error {
	return nil
}
func (*lifecycleClaimBackend) Snapshot(context.Context, managedagent.Handle) (managedagent.BackendSnapshot, error) {
	return managedagent.BackendSnapshot{Phase: managedagent.BackendPhaseWorking}, nil
}
func (backend *lifecycleClaimBackend) Cancel(context.Context, managedagent.Handle) error {
	backend.mu.Lock()
	backend.cancels++
	backend.mu.Unlock()
	return nil
}
func (backend *lifecycleClaimBackend) Close(context.Context, managedagent.Handle) error {
	backend.mu.Lock()
	backend.closes++
	backend.mu.Unlock()
	return nil
}
func (backend *lifecycleClaimBackend) counts() (int, int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.cancels, backend.closes
}

func TestStopRevokesManagedAgentClaimsAndSessionBindings(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager, err := managedagent.NewManager(managedagent.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backend := &lifecycleClaimBackend{}
	if err := manager.RegisterBackend(backend); err != nil {
		t.Fatal(err)
	}
	claimedAgent, err := manager.Spawn(t.Context(), managedagent.OperatorController(), managedagent.ManagedSpawnRequest{
		Input: managedagent.SpawnInput{
			WorkspaceID: "ws_claimed",
			Prompt:      "claimed child",
			Backend:     "lifecycle-claim-test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimedCredential, err := manager.IssueClaim(claimedAgent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ConsumeClaim(claimedAgent.ID, claimedCredential.Token(), "child-session"); err != nil {
		t.Fatal(err)
	}

	pendingAgent, err := manager.Spawn(t.Context(), managedagent.OperatorController(), managedagent.ManagedSpawnRequest{
		Input: managedagent.SpawnInput{
			WorkspaceID: "ws_pending",
			Prompt:      "pending child",
			Backend:     "lifecycle-claim-test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pendingCredential, err := manager.IssueClaim(pendingAgent.ID)
	if err != nil {
		t.Fatal(err)
	}

	value := &App{Tools: &tools.Runtime{Agents: manager}}
	if err := value.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.SessionBinding("child-session"); ok {
		t.Fatal("runtime stop retained claimed child binding")
	}
	if _, err := manager.ConsumeClaim(pendingAgent.ID, pendingCredential.Token(), "late-session"); err == nil {
		t.Fatal("runtime stop retained outstanding claim")
	}
	for _, id := range []managedagent.ID{claimedAgent.ID, pendingAgent.ID} {
		snapshot, err := manager.Get(context.Background(), managedagent.OperatorController(), id)
		if err != nil || snapshot.State != managedagent.StateCancelled || snapshot.Error != "runtime shutdown" {
			t.Fatalf("shutdown snapshot=%#v err=%v", snapshot, err)
		}
	}
	if cancels, closes := backend.counts(); cancels != 2 || closes != 2 {
		t.Fatalf("shutdown cleanup cancel=%d close=%d", cancels, closes)
	}
	if _, err := manager.Spawn(context.Background(), managedagent.OperatorController(), managedagent.ManagedSpawnRequest{Input: managedagent.SpawnInput{
		WorkspaceID: "ws_after_stop", Prompt: "must not start",
	}}); !errors.Is(err, managedagent.ErrManagerClosed) {
		t.Fatalf("spawn after app stop error=%v", err)
	}
}
