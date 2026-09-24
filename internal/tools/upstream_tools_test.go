package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/upstream"
)

type bridgeClient struct {
	tools      []upstream.Tool
	toolsErr   error
	result     upstream.CallResult
	callErr    error
	connected  upstream.Server
	callServer string
	callTool   string
	calls      int
}

func (c *bridgeClient) Connect(_ context.Context, server upstream.Server) error {
	c.connected = server
	return nil
}
func (*bridgeClient) Close(context.Context, string) error { return nil }
func (c *bridgeClient) Tools(context.Context, string) ([]upstream.Tool, error) {
	if c.toolsErr != nil {
		return nil, c.toolsErr
	}
	return append([]upstream.Tool(nil), c.tools...), nil
}
func (c *bridgeClient) Call(_ context.Context, serverID, tool string, _ map[string]any) (upstream.CallResult, error) {
	c.callServer = serverID
	c.callTool = tool
	c.calls++
	return c.result, c.callErr
}
func (*bridgeClient) PID(string) int { return 0 }

func TestUpstreamBridgeAndProxyRegistration(t *testing.T) {
	client := &bridgeClient{
		tools: []upstream.Tool{{
			Name: "echo", Description: "Echo",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		}},
		result: upstream.CallResult{Content: []upstream.Content{{Type: "text", Text: "hello"}}, StructuredContent: map[string]any{"value": "hello"}},
	}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{
		ID: "demo", Name: "Demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all",
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterUpstreamTools(registry, manager)

	toolsResult, err := registry.Call(context.Background(), "upstream_tools", map[string]any{"server_id": "demo"})
	if err != nil || toolsResult.IsError {
		t.Fatalf("upstream_tools failed: %#v %v", toolsResult, err)
	}
	found := false
	for _, schema := range registry.ListSchemas() {
		if schema.Name == "demo__echo" {
			found = true
			var input map[string]any
			if err := json.Unmarshal(schema.InputSchema, &input); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("dynamic proxy was not registered")
	}
	proxy, err := registry.Call(context.Background(), "demo__echo", map[string]any{"text": "hello"})
	if err != nil || proxy.IsError {
		t.Fatalf("proxy failed: %#v %v", proxy, err)
	}
	if proxy.StructuredContent == nil {
		t.Fatal("proxy did not forward structured content")
	}
}

func TestUpstreamCallNormalizesError(t *testing.T) {
	client := &bridgeClient{
		tools:  []upstream.Tool{{Name: "x", InputSchema: map[string]any{"type": "object"}}},
		result: upstream.CallResult{Content: []upstream.Content{{Type: "text", Text: "bad"}}, IsError: true},
	}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterUpstreamTools(registry, manager)
	result, err := registry.Call(context.Background(), "upstream_call", map[string]any{"server_id": "demo", "tool": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error: %#v", result)
	}
}

func TestUpstreamCallEnforcesExposurePolicy(t *testing.T) {
	client := &bridgeClient{tools: []upstream.Tool{{Name: "read"}, {Name: "delete"}}}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{
		ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "allowlist",
		Tools: []string{"read"}, DisabledTools: []string{"delete"},
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterUpstreamTools(registry, manager)
	_, err := registry.Call(context.Background(), "upstream_call", map[string]any{"server_id": "demo", "tool": "delete"})
	if !errors.Is(err, upstream.ErrToolNotExposed) {
		t.Fatalf("hidden tool error=%v", err)
	}
	if client.calls != 0 {
		t.Fatalf("hidden tool reached remote client: calls=%d", client.calls)
	}
}

func TestDynamicProxyBindsCanonicalServerToolAndRisk(t *testing.T) {
	client := &bridgeClient{
		tools: []upstream.Tool{{
			Name: "echo", InputSchema: map[string]any{"type": "object"},
			Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false},
		}},
		result: upstream.CallResult{Content: []upstream.Content{{Type: "text", Text: "ok"}}},
	}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); err != nil {
		t.Fatal(err)
	}
	schema, ok := registry.Schema("demo__echo")
	if !ok {
		t.Fatal("proxy schema missing")
	}
	for key, want := range map[string]bool{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true, "idempotentHint": false} {
		if got, _ := schema.Annotations[key].(bool); got != want {
			t.Fatalf("annotation %s=%v want=%v: %#v", key, got, want, schema.Annotations)
		}
	}
	current, _ := manager.Get("demo")
	current.URL = "https://example.test/current"
	current.Headers = map[string]string{"Authorization": "Bearer current-credential"}
	if err := manager.Add(current); err != nil {
		t.Fatal(err)
	}
	client.connected = upstream.Server{}
	if _, err := registry.Call(context.Background(), "demo__echo", map[string]any{"server_id": "other", "tool": "delete"}); err != nil {
		t.Fatal(err)
	}
	if client.callServer != "demo" || client.callTool != "echo" {
		t.Fatalf("proxy rebound remote identity: server=%q tool=%q", client.callServer, client.callTool)
	}
	if client.connected.ID != "demo" || client.connected.URL != current.URL || client.connected.Headers["Authorization"] != current.Headers["Authorization"] {
		t.Fatalf("proxy did not resolve current authorization context: %#v", client.connected)
	}
}

func TestDynamicProxyBoundsRemoteOutputBeforeRuntime(t *testing.T) {
	client := &bridgeClient{
		tools:  []upstream.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}},
		result: upstream.CallResult{Content: []upstream.Content{{Type: "text", Text: strings.Repeat("x", maxToolResultBytes+1024)}}},
	}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Call(context.Background(), "demo__echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= maxToolResultBytes || result.Meta == nil {
		t.Fatalf("unbounded proxy result bytes=%d meta=%#v", len(data), result.Meta)
	}
}

type subscriptionBridgeClient struct {
	mu      sync.Mutex
	tools   []upstream.Tool
	started chan struct{}
	trigger chan struct{}
}

func (*subscriptionBridgeClient) Connect(context.Context, upstream.Server) error { return nil }
func (*subscriptionBridgeClient) Close(context.Context, string) error            { return nil }
func (c *subscriptionBridgeClient) Tools(context.Context, string) ([]upstream.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]upstream.Tool(nil), c.tools...), nil
}
func (*subscriptionBridgeClient) Call(context.Context, string, string, map[string]any) (upstream.CallResult, error) {
	return upstream.CallResult{}, nil
}
func (*subscriptionBridgeClient) PID(string) int { return 0 }
func (c *subscriptionBridgeClient) ListenToolsChanged(ctx context.Context, _ string, onChange func()) error {
	select {
	case c.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.trigger:
		onChange()
		<-ctx.Done()
		return ctx.Err()
	}
}

func TestUpstreamToolSubscriptionRefreshesDynamicProxy(t *testing.T) {
	client := &subscriptionBridgeClient{
		tools:   []upstream.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}},
		started: make(chan struct{}, 1),
		trigger: make(chan struct{}, 1),
	}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{
		ID: "demo", Name: "Demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all",
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterUpstreamTools(registry, manager)
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("subscription did not start")
	}
	if !hasSchema(registry.ListSchemas(), "demo__echo") {
		t.Fatal("initial proxy missing")
	}

	client.mu.Lock()
	client.tools = []upstream.Tool{{Name: "goodbye", InputSchema: map[string]any{"type": "object"}}}
	client.mu.Unlock()
	client.trigger <- struct{}{}

	deadline := time.Now().Add(time.Second)
	for {
		schemas := registry.ListSchemas()
		if hasSchema(schemas, "demo__goodbye") && !hasSchema(schemas, "demo__echo") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dynamic proxies were not refreshed: %#v", schemas)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshUpstreamProxiesPreservesCatalogOnTransientFailure(t *testing.T) {
	client := &bridgeClient{tools: []upstream.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterUpstreamTools(registry, manager)
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); err != nil {
		t.Fatal(err)
	}
	if !hasSchema(registry.ListSchemas(), "demo__echo") {
		t.Fatal("initial proxy missing")
	}
	changes := registry.SubscribeChanges()
	defer registry.UnsubscribeChanges(changes)
	client.toolsErr = errors.New("transient discovery failure")
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, true); err == nil {
		t.Fatal("expected transient refresh failure")
	}
	if !hasSchema(registry.ListSchemas(), "demo__echo") {
		t.Fatal("existing proxy was removed by failed refresh")
	}
	select {
	case <-changes:
		t.Fatal("failed refresh signaled tool catalog change")
	default:
	}
}

func TestRefreshUpstreamProxiesRejectsCrossServerNameCollisionAtomically(t *testing.T) {
	client := &bridgeClient{tools: []upstream.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	manager := upstream.NewManagerWithClient(nil, client)
	for _, id := range []string{"one", "two"} {
		if err := manager.Add(upstream.Server{ID: id, Enabled: true, Transport: "http", URL: "https://example.test/" + id, Expose: "all", ToolPrefix: "shared"}); err != nil {
			t.Fatal(err)
		}
	}
	registry := NewRegistry()
	if err := registry.ReplaceOwned("upstream:stable", map[string]Entry{
		"stable__tool": {Schema: Schema{Name: "stable__tool"}, Handler: func(context.Context, map[string]any) (Result, error) { return TextResult("stable"), nil }},
	}); err != nil {
		t.Fatal(err)
	}
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); !errors.Is(err, ErrToolAlreadyRegistered) {
		t.Fatalf("collision error = %v", err)
	}
	if _, err := registry.Call(context.Background(), "stable__tool", nil); err != nil {
		t.Fatalf("stable catalog was mutated after collision: %v", err)
	}
}

func TestRefreshUpstreamProxiesRejectsSanitizedCollisionWithinServer(t *testing.T) {
	client := &bridgeClient{tools: []upstream.Tool{
		{Name: "echo.v1", InputSchema: map[string]any{"type": "object"}},
		{Name: "echo v1", InputSchema: map[string]any{"type": "object"}},
	}}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); !errors.Is(err, ErrToolAlreadyRegistered) {
		t.Fatalf("sanitized collision error=%v", err)
	}
	if hasSchema(registry.ListSchemas(), "demo__echo_v1") {
		t.Fatal("colliding proxy was partially registered")
	}
}

func TestStaleDynamicProxyRejectsDisabledAndRemovedUpstreams(t *testing.T) {
	client := &bridgeClient{
		tools:  []upstream.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}},
		result: upstream.CallResult{Content: []upstream.Content{{Type: "text", Text: "ok"}}},
	}
	manager := upstream.NewManagerWithClient(nil, client)
	for _, id := range []string{"disabled", "removed"} {
		if err := manager.Add(upstream.Server{ID: id, Enabled: true, Transport: "http", URL: "https://example.test/" + id, Expose: "all"}); err != nil {
			t.Fatal(err)
		}
	}
	registry := NewRegistry()
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); err != nil {
		t.Fatal(err)
	}
	disabled, _ := manager.Get("disabled")
	disabled.Enabled = false
	if err := manager.Add(disabled); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove("removed"); err != nil {
		t.Fatal(err)
	}
	client.calls = 0
	if _, err := registry.Call(context.Background(), "disabled__echo", nil); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("stale disabled proxy error=%v", err)
	}
	if _, err := registry.Call(context.Background(), "removed__echo", nil); err == nil || !strings.Contains(err.Error(), "unknown upstream server") {
		t.Fatalf("stale removed proxy error=%v", err)
	}
	if client.calls != 0 {
		t.Fatalf("stale proxy reached remote client: calls=%d", client.calls)
	}
}

func TestRefreshUpstreamProxiesRemovesDisabledServerOnSuccessfulSwap(t *testing.T) {
	client := &bridgeClient{tools: []upstream.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	manager := upstream.NewManagerWithClient(nil, client)
	server := upstream.Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all"}
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); err != nil {
		t.Fatal(err)
	}
	if !hasSchema(registry.ListSchemas(), "demo__echo") {
		t.Fatal("initial proxy missing")
	}
	server.Enabled = false
	server.Expose = "none"
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	if err := RefreshUpstreamProxies(context.Background(), registry, manager, false); err != nil {
		t.Fatal(err)
	}
	if hasSchema(registry.ListSchemas(), "demo__echo") {
		t.Fatal("disabled server proxy survived successful refresh")
	}
}

func hasSchema(values []Schema, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}
