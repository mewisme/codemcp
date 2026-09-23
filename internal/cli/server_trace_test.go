package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/runtimecontrol"
	"go.mewis.me/codemcp/internal/runtimeevent"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type serverTraceCollector struct {
	mu     sync.Mutex
	events []tracepkg.Event
}

func (c *serverTraceCollector) Observe(event tracepkg.Event) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

func (c *serverTraceCollector) Snapshot() []tracepkg.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tracepkg.Event(nil), c.events...)
}

func TestListenerFallbackTraceIncludesConflictAndSelectedPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	configuredPort := testServerPort(t, occupied.Addr())
	collector := &serverTraceCollector{}
	ctx := tracepkg.WithObserver(context.Background(), collector.Observe)
	cfg := config.Default()
	cfg.Server.Enabled = false
	cfg.Admin.Enabled = true
	cfg.Admin.Port = configuredPort
	bindings, err := openHTTPBindingsContext(ctx, cfg, listenerPlan{Hosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer bindings.CloseUnstarted()
	if bindings.cfg.Admin.Port == configuredPort {
		t.Fatalf("admin port did not fall back from %d", configuredPort)
	}
	events := collector.Snapshot()
	if !serverTraceHasFields(events, "server.listener.bind.failed", map[string]any{"component": "admin", "configured_port": configuredPort, "attempted_port": configuredPort, "fallback_attempt": 0, "address_in_use": true}) {
		t.Fatalf("missing bind conflict trace: %#v", events)
	}
	if !serverTraceHasFields(events, "server.listener.port-selected", map[string]any{"component": "admin", "configured_port": configuredPort, "selected_port": bindings.cfg.Admin.Port}) {
		t.Fatalf("missing selected port trace: %#v", events)
	}
}

func TestHTTPReadinessTraceAggregatesRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	collector := &serverTraceCollector{}
	ctx := tracepkg.WithObserver(context.Background(), collector.Observe)
	cfg := config.Default()
	cfg.Server.Port = testServerPort(t, server.Listener.Addr())
	cfg.Admin.Enabled = false
	if err := waitRuntimeHTTPReady(ctx, cfg, time.Second); err != nil {
		t.Fatal(err)
	}
	events := collector.Snapshot()
	firstFailures := 0
	for _, event := range events {
		if event.Name == "server.http-readiness.first-failure" {
			firstFailures++
		}
	}
	if firstFailures != 1 {
		t.Fatalf("first failure events = %d, want 1: %#v", firstFailures, events)
	}
	completed, ok := serverTraceEvent(events, "server.http-readiness.completed")
	if !ok {
		t.Fatalf("missing readiness completion trace: %#v", events)
	}
	if attempts, _ := serverTraceField(completed, "attempts"); attempts.(int) < 3 {
		t.Fatalf("readiness attempts = %v, want >= 3", attempts)
	}
	if count, _ := serverTraceField(completed, "endpoint_count"); count != 1 {
		t.Fatalf("endpoint_count = %v, want 1", count)
	}
}

func TestRuntimeControlTraceNeverLeaksToken(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	collector := &serverTraceCollector{}
	ctx := tracepkg.WithObserver(context.Background(), collector.Observe)
	control, err := startRuntimeControlContext(ctx, runtimeControlOptions{RunID: "run_trace", Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: 1}, nil
	}, Status: func() runtimeStatusResult { return runtimeStatusResult{PID: 1} }, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	token := control.state.Token
	if token == "" {
		t.Fatal("control token is empty")
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	events := collector.Snapshot()
	text := fmt.Sprintf("%#v", events)
	if strings.Contains(text, token) {
		t.Fatalf("runtime control token leaked into trace: %s", text)
	}
	for _, name := range []string{"runtime.control.bind.completed", "runtime.control.state.write.completed", "runtime.control.start.completed", "runtime.control.cleanup.completed"} {
		if _, ok := serverTraceEvent(events, name); !ok {
			t.Fatalf("missing %s trace: %#v", name, events)
		}
	}
}

func TestServerReloadTraceIncludesDecisionPortsAndShutdown(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	initialMCP := freeServerTracePort(t)
	initialAdmin := freeServerTracePort(t)
	cfg := config.Default()
	cfg.Server.Port = initialMCP
	cfg.Admin.Port = initialAdmin
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Enabled = false
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	collector := &serverTraceCollector{}
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(tracepkg.WithObserver(baseCtx, collector.Observe))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	done := make(chan error, 1)
	go func() { done <- runServer(cmd, nil) }()
	waitServerTraceReady(t)

	nextMCP := freeServerTracePort(t)
	nextAdmin := freeServerTracePort(t)
	cfg.Server.Port = nextMCP
	cfg.Admin.Port = nextAdmin
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	requestCtx, requestCancel := context.WithTimeout(context.Background(), 3*time.Second)
	result, err := requestRuntimeReload(requestCtx)
	requestCancel()
	if err != nil {
		t.Fatal(err)
	}
	if !result.NetworkRestarted || result.ServerPort == initialMCP || result.AdminPort == initialAdmin {
		t.Fatalf("reload result = %#v", result)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := requestRuntimeShutdown(shutdownCtx); err != nil {
		shutdownCancel()
		t.Fatal(err)
	}
	shutdownCancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
	events := collector.Snapshot()
	if !serverTraceHasFields(events, "server.reload.completed", map[string]any{"network_restarted": true, "old_mcp_port": initialMCP, "new_mcp_port": result.ServerPort, "old_admin_port": initialAdmin, "new_admin_port": result.AdminPort}) {
		t.Fatalf("missing reload completion facts: %#v", events)
	}
	if !serverTraceHasFields(events, "server.reload.decision", map[string]any{"network_restarted": true, "old_mcp_port": initialMCP, "new_mcp_port": nextMCP, "old_admin_port": initialAdmin, "new_admin_port": nextAdmin}) {
		t.Fatalf("missing reload decision facts: %#v", events)
	}
	for _, name := range []string{"server.reload.candidate-open.completed", "server.reload.previous-listeners-shutdown.completed", "server.shutdown.completed", "server.listeners.shutdown.completed", "app.mcp.subscriptions.close.completed", "app.upstream.shutdown.completed", "runtime.control.cleanup.completed", "runtime.session.completed"} {
		if _, ok := serverTraceEvent(events, name); !ok {
			t.Fatalf("missing %s trace: %#v", name, events)
		}
	}
	if !serverTraceHasFields(events, "server.shutdown.completed", map[string]any{"reason": "runtime_control"}) {
		t.Fatalf("missing shutdown reason: %#v", events)
	}
}

func waitServerTraceReady(t *testing.T) runtimeStatusResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		status, err := requestRuntimeStatus(ctx)
		cancel()
		if err == nil && status.Lifecycle == "ready" && !status.Starting {
			return status
		}
		if err != nil && !runtimecontrol.IsUnavailable(err) {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
	return runtimeStatusResult{}
}

func freeServerTracePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := testServerPort(t, listener.Addr())
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func serverTraceEvent(events []tracepkg.Event, name string) (tracepkg.Event, bool) {
	for _, event := range events {
		if event.Name == name {
			return event, true
		}
	}
	return tracepkg.Event{}, false
}

func serverTraceHasFields(events []tracepkg.Event, name string, expected map[string]any) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		matched := true
		for key, want := range expected {
			got, ok := serverTraceField(event, key)
			if !ok || fmt.Sprint(got) != fmt.Sprint(want) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func serverTraceField(event tracepkg.Event, key string) (any, bool) {
	for _, field := range event.Fields {
		if field.Key == key {
			return field.Value, true
		}
	}
	return nil, false
}
