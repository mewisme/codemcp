package application

import (
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestCapabilityInventoryDiagnosticsAreApplicationReadModels(t *testing.T) {
	inventory := LoadCapabilityInventory()
	diagnostics := LoadCapabilityDiagnostics()
	if inventory.Version != capability.InventoryVersion {
		t.Fatalf("inventory version=%d", inventory.Version)
	}
	if diagnostics.Version != inventory.Version || diagnostics.Operations != len(inventory.Operations) {
		t.Fatalf("diagnostics=%#v inventory operations=%d", diagnostics, len(inventory.Operations))
	}
	if len(diagnostics.SurfaceSummary) != len(inventory.Surfaces) {
		t.Fatalf("surface diagnostics=%d inventory=%d", len(diagnostics.SurfaceSummary), len(inventory.Surfaces))
	}
}
