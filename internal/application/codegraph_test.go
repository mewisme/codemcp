package application

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
)

func TestCodeGraphOperationsUseCanonicalDispatcher(t *testing.T) {
	cfg := config.Default()
	service := &CodeGraphService{LoadConfig: func() (config.Config, error) { return cfg, nil }}
	dispatcher := NewDispatcher()
	if err := BindCodeGraphOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capability.ID{
		capability.IntegrationCodeGraphStatus,
		capability.IntegrationCodeGraphProbe,
		capability.IntegrationCodeGraphInstall,
	} {
		if dispatcher.handlers[id] == nil {
			t.Fatalf("operation %s is not bound", id)
		}
		if _, ok := capability.Lookup(id); !ok {
			t.Fatalf("operation %s is not canonical", id)
		}
	}

	result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: capability.IntegrationCodeGraphStatus})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := result.Value.(codegraph.Status)
	if !ok || status.Enabled || status.Resolution.Source != codegraph.ExecutableDisabled || status.PinnedVersion != codegraph.Version {
		t.Fatalf("status=%#v", result.Value)
	}
}
