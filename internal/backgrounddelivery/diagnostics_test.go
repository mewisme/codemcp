package backgrounddelivery

import (
	"testing"
	"time"
)

func TestInspectDiagnosticsDoesNotPruneExpiredDeliveries(t *testing.T) {
	broker := newBroker(nil, "")
	owner := Owner{ID: "owner-a", Generation: "generation-a"}
	now := time.Now().UTC()
	delivery := Delivery{
		ID: "delivery_expired", WorkspaceID: "ws_one", OwnerID: owner.ID, OwnerGeneration: owner.Generation,
		State: DeliveryPending, CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	broker.deliveries[delivery.ID] = delivery
	broker.order = []string{delivery.ID}
	broker.byOwner[ownerKey(owner)] = []string{delivery.ID}

	diagnostics := broker.InspectDiagnostics()
	if diagnostics.Pending != 1 || diagnostics.OldestPendingAgeMS <= 0 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if _, ok := broker.deliveries[delivery.ID]; !ok || len(broker.order) != 1 || len(broker.byOwner[ownerKey(owner)]) != 1 {
		t.Fatalf("read-only diagnostics pruned delivery: deliveries=%#v order=%#v owners=%#v", broker.deliveries, broker.order, broker.byOwner)
	}
}
