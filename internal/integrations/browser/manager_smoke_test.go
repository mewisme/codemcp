package browser

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLocalBrowserManagerSmoke(t *testing.T) {
	if os.Getenv("CM_BROWSER_SMOKE") != "1" {
		t.Skip("set CM_BROWSER_SMOKE=1 to run the local real-browser fixture")
	}
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	capability := Detect(context.Background(), Options{Enabled: true, StateRoot: root})
	if capability.State != StateAvailable || !capability.Usable {
		t.Fatalf("local browser unavailable: %#v", capability)
	}
	manager, err := NewManager(ManagerOptions{
		Capability: capability,
		MaxTabs:    1, AgentIdleTTL: time.Minute, BrowserWarmTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	lease, err := manager.Acquire(context.Background(), "smoke-agent")
	if err != nil {
		t.Fatal(err)
	}
	if lease.State != LeaseActive || lease.TabID == "" {
		t.Fatalf("lease=%#v", lease)
	}
	if err := manager.Release(context.Background(), lease.AgentID); err != nil {
		t.Fatal(err)
	}
}
