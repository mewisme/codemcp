package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalBrowserManagerSmoke(t *testing.T) {
	if os.Getenv("CM_BROWSER_SMOKE") != "1" {
		t.Skip("set CM_BROWSER_SMOKE=1 to run the local real-browser fixture")
	}
	for _, mode := range []struct {
		name     string
		headless bool
	}{
		{name: "visible"},
		{name: "headless", headless: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CM_CONFIG_DIR", root)
			capability := Detect(context.Background(), Options{Enabled: true, StateRoot: root, Passive: true, Headless: mode.headless})
			if capability.State != StateAvailable || !capability.Available || !capability.Launchable {
				t.Fatalf("local browser unavailable: %#v", capability)
			}
			manager, err := NewManager(ManagerOptions{
				Capability: capability,
				Headless:   mode.headless,
				MaxTabs:    2, AgentIdleTTL: time.Minute, BrowserWarmTTL: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := manager.Acquire(context.Background(), "smoke-agent")
			if err != nil {
				t.Fatal(err)
			}
			if lease.State != LeaseActive || lease.TabID == "" {
				t.Fatalf("lease=%#v", lease)
			}
			endpoint, closeEndpoint := smokeDevToolsEndpoint(t, capability)
			defer closeEndpoint()
			if got := smokePageTargetCount(t, endpoint); got != 1 {
				t.Fatalf("page targets after first lease=%d want=1", got)
			}
			firstTab, ok := manager.Tab(lease.AgentID)
			if !ok {
				t.Fatal("first browser tab is unavailable")
			}
			if err := firstTab.Navigate(context.Background(), "about:blank#codemcp-target"); err != nil {
				t.Fatal(err)
			}
			if got := smokePageTargetCount(t, endpoint); got != 1 {
				t.Fatalf("page targets after first navigation=%d want=1", got)
			}
			second, err := manager.Acquire(context.Background(), "smoke-agent-2")
			if err != nil {
				t.Fatal(err)
			}
			if got := smokePageTargetCount(t, endpoint); got != 2 {
				t.Fatalf("page targets after second lease=%d want=2", got)
			}
			if err := manager.Release(context.Background(), lease.AgentID); err != nil {
				t.Fatal(err)
			}
			if _, ok := manager.Tab(second.AgentID); !ok {
				t.Fatal("closing bootstrap lease disconnected sibling tab")
			}
			if got := smokePageTargetCount(t, endpoint); got != 1 {
				t.Fatalf("page targets after first release=%d want=1", got)
			}
			if !mode.headless {
				if err := manager.Minimize(context.Background()); err != nil {
					t.Fatalf("minimize after bootstrap release: %v", err)
				}
			}
			if err := manager.Release(context.Background(), second.AgentID); err != nil {
				t.Fatal(err)
			}
			if err := manager.Close(context.Background()); err != nil {
				t.Fatalf("close managed browser: %v", err)
			}
		})
	}
}

func smokeDevToolsEndpoint(t *testing.T, capability Capability) (string, func()) {
	t.Helper()
	if capability.Profile == nil {
		t.Fatal("browser capability profile is unavailable")
	}
	port, err := readDevToolsPort(filepath.Join(capability.Profile.LocalPath, "DevToolsActivePort"))
	if err != nil {
		t.Fatal(err)
	}
	if capability.Transport != TransportWSLHost {
		return fmt.Sprintf("http://127.0.0.1:%d", port), func() {}
	}
	relay, err := startWindowsLoopbackRelay(port)
	if err != nil {
		t.Fatal(err)
	}
	return relay.URL(), func() { _ = relay.Close() }
}

func smokePageTargetCount(t *testing.T, endpoint string) int {
	t.Helper()
	response, err := http.Get(endpoint + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var targets []struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(response.Body).Decode(&targets); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range targets {
		if item.Type == "page" {
			count++
		}
	}
	return count
}
