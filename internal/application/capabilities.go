package application

import "go.mewis.me/codemcp/internal/capability"

func LoadCapabilityInventory() capability.InventorySnapshot {
	return capability.Inventory()
}

func LoadCapabilityDiagnostics() capability.InventoryDiagnostics {
	return capability.Diagnostics()
}
