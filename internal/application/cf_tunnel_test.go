package application

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/integrations/cftunnel"
)

type fakeCFTunnelManager struct {
	status cftunnel.Status
	calls  []string
}

func (manager *fakeCFTunnelManager) Status() (cftunnel.Status, error) {
	manager.calls = append(manager.calls, "status")
	return manager.status, nil
}
func (manager *fakeCFTunnelManager) Probe(context.Context) (cftunnel.ProbeResult, error) {
	manager.calls = append(manager.calls, "probe")
	return cftunnel.ProbeResult{Status: manager.status, Version: "v0.0.1"}, nil
}
func (manager *fakeCFTunnelManager) Install(context.Context) (cftunnel.InstallResult, error) {
	manager.calls = append(manager.calls, "install")
	return cftunnel.InstallResult{Status: manager.status, Installed: true}, nil
}
func (manager *fakeCFTunnelManager) Update(context.Context) (cftunnel.InstallResult, error) {
	manager.calls = append(manager.calls, "update")
	return cftunnel.InstallResult{Status: manager.status, AlreadyInstalled: true}, nil
}
func (manager *fakeCFTunnelManager) Remove() (cftunnel.RemoveResult, error) {
	manager.calls = append(manager.calls, "remove")
	return cftunnel.RemoveResult{Status: manager.status, Removed: true}, nil
}
func (manager *fakeCFTunnelManager) ResolvePath() (string, error) {
	return "/managed/cf-tunnel", nil
}

func TestCFTunnelOperationsBindCanonicalOwner(t *testing.T) {
	manager := &fakeCFTunnelManager{status: cftunnel.Status{Source: cftunnel.SourceManaged, Version: "v0.0.1", Verified: true}}
	dispatcher := NewDispatcher()
	if err := BindCFTunnelOperations(dispatcher, NewCFTunnelServiceWithManager(manager)); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []capability.ID{capability.IntegrationCFStatus, capability.IntegrationCFProbe, capability.IntegrationCFInstall, capability.IntegrationCFUpdate, capability.IntegrationCFRemove} {
		if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: operation}); err != nil {
			t.Fatalf("dispatch %s: %v", operation, err)
		}
	}
	want := []string{"status", "probe", "install", "update", "remove"}
	if len(manager.calls) != len(want) {
		t.Fatalf("calls=%#v", manager.calls)
	}
	for index := range want {
		if manager.calls[index] != want[index] {
			t.Fatalf("calls=%#v want=%#v", manager.calls, want)
		}
	}
}
