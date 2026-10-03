package app

import (
	"context"
	"testing"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/tools"
)

type lifecycleClaimBackend struct{}

func (lifecycleClaimBackend) ID() managedagent.BackendID { return "lifecycle-claim-test" }
func (lifecycleClaimBackend) Ready(context.Context) (managedagent.Readiness, error) {
	return managedagent.Readiness{Available: true, Capacity: managedagent.Capacity{MaxParallel: 2}}, nil
}
func (lifecycleClaimBackend) Spawn(_ context.Context, request managedagent.BackendSpawnRequest) (managedagent.Handle, error) {
	return string(request.AgentID), nil
}
func (lifecycleClaimBackend) Send(context.Context, managedagent.Handle, managedagent.Message) error {
	return nil
}
func (lifecycleClaimBackend) Snapshot(context.Context, managedagent.Handle) (managedagent.BackendSnapshot, error) {
	return managedagent.BackendSnapshot{Phase: managedagent.BackendPhaseWorking}, nil
}
func (lifecycleClaimBackend) Cancel(context.Context, managedagent.Handle) error { return nil }
func (lifecycleClaimBackend) Close(context.Context, managedagent.Handle) error  { return nil }

func TestStopRevokesManagedAgentClaimsAndSessionBindings(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager, err := managedagent.NewManager(managedagent.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterBackend(lifecycleClaimBackend{}); err != nil {
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
}
