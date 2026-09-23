package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/upstream"
)

type bridgeClient struct {
	tools    []upstream.Tool
	toolsErr error
	result   upstream.CallResult
}

func (*bridgeClient) Connect(context.Context, upstream.Server) error { return nil }
func (*bridgeClient) Close(context.Context, string) error            { return nil }
func (c *bridgeClient) Tools(context.Context, string) ([]upstream.Tool, error) {
	if c.toolsErr != nil {
		return nil, c.toolsErr
	}
	return append([]upstream.Tool(nil), c.tools...), nil
}
func (c *bridgeClient) Call(context.Context, string, string, map[string]any) (upstream.CallResult, error) {
	return c.result, nil
}
func (*bridgeClient) PID(string) int { return 0 }

func TestMCPBridgeAndProxyRegistration(t *testing.T) {
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

	toolsResult, err := registry.Call(context.Background(), "mcp_tools", map[string]any{"server_id": "demo"})
	if err != nil || toolsResult.IsError {
		t.Fatalf("mcp_tools failed: %#v %v", toolsResult, err)
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

func TestMCPCallNormalizesUpstreamError(t *testing.T) {
	client := &bridgeClient{
		result: upstream.CallResult{Content: []upstream.Content{{Type: "text", Text: "bad"}}, IsError: true},
	}
	manager := upstream.NewManagerWithClient(nil, client)
	if err := manager.Add(upstream.Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test", Expose: "all"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	RegisterUpstreamTools(registry, manager)
	result, err := registry.Call(context.Background(), "mcp_call", map[string]any{"server_id": "demo", "tool": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error: %#v", result)
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
