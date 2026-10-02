package application

import (
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestSystemOperationsBindCanonicalOwners(t *testing.T) {
	dispatcher := NewDispatcher()
	if err := BindSystemOperations(dispatcher, SystemOperationServices{}); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []capability.ID{
		capability.VersionAbout,
		capability.RuntimeUp, capability.RuntimeDown, capability.RuntimeRestart,
		capability.UpdateCheck, capability.UpdateApply, capability.InstallRun,
		capability.ProjectContextRead, capability.ToolInventoryRead,
		capability.PromptList, capability.PromptGet, capability.PromptCreate, capability.PromptUpdate, capability.PromptDelete,
	} {
		if dispatcher.handlers[operation] == nil {
			t.Fatalf("canonical system operation %s is not bound", operation)
		}
	}
}
