package upstream

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type traceTestClient struct {
	connects int
	tools    int
	toolsErr error
}

func (c *traceTestClient) Connect(context.Context, Server) error { c.connects++; return nil }
func (*traceTestClient) Close(context.Context, string) error     { return nil }
func (c *traceTestClient) Tools(context.Context, string) ([]Tool, error) {
	c.tools++
	if c.toolsErr != nil {
		return nil, c.toolsErr
	}
	return []Tool{{Name: "echo"}}, nil
}
func (*traceTestClient) Call(context.Context, string, string, map[string]any) (CallResult, error) {
	return CallResult{}, nil
}
func (*traceTestClient) PID(string) int { return 321 }

func TestManagerToolsTraceReportsCacheLifecycle(t *testing.T) {
	client := &traceTestClient{}
	events := []tracepkg.Event{}
	observer := func(event tracepkg.Event) { events = append(events, event) }
	manager := NewManagerWithClient(nil, client).SetTraceObserver(observer)
	if err := manager.Add(Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test/mcp"}); err != nil {
		t.Fatal(err)
	}
	ctx := tracepkg.WithObserver(t.Context(), observer)
	if _, err := manager.Tools(ctx, "demo", false); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Tools(ctx, "demo", false); err != nil {
		t.Fatal(err)
	}
	if client.connects != 1 || client.tools != 1 {
		t.Fatalf("connects=%d tools=%d, want 1/1", client.connects, client.tools)
	}
	if !traceEventHasField(events, "upstream.tools.discover.completed", "cache_hit", false) {
		t.Fatalf("missing cache miss completion: %#v", events)
	}
	if !traceEventHasField(events, "upstream.tools.discover.completed", "cache_hit", true) {
		t.Fatalf("missing cache hit completion: %#v", events)
	}
	if !traceEventHasField(events, "upstream.tools.list.completed", "tool_count", 1) {
		t.Fatalf("missing tool count trace: %#v", events)
	}
}

func TestInspectStatusesDoesNotConnectOrDiscoverTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstreams.json")
	store := NewStore(path)
	if err := store.Save([]Server{{ID: "alpha", Name: "Alpha", Enabled: true, Transport: "http", URL: "https://example.test/mcp"}}); err != nil {
		t.Fatal(err)
	}
	client := &traceTestClient{}
	manager := NewManagerWithClient(store, client)
	statuses, err := manager.InspectStatuses()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].ID != "alpha" || statuses[0].Health != HealthUnknown {
		t.Fatalf("statuses=%#v", statuses)
	}
	if client.connects != 0 || client.tools != 0 {
		t.Fatalf("inspection performed upstream I/O: connects=%d tools=%d", client.connects, client.tools)
	}
}

func TestUpstreamTraceDoesNotExposeConfiguredSecrets(t *testing.T) {
	secretHeader := "header-super-secret"
	secretEnv := "env-super-secret"
	server := Server{
		ID: "demo", Enabled: true, Transport: "http",
		URL:     "https://example.test/mcp?token=query-super-secret&view=tools",
		Headers: map[string]string{"Authorization": "Bearer " + secretHeader}, Env: map[string]string{"PASSWORD": secretEnv},
	}
	events := []tracepkg.Event{}
	manager := NewManagerWithClient(nil, &traceTestClient{}).SetTraceObserver(func(event tracepkg.Event) { events = append(events, event) })
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprint(events)
	for _, secret := range []string{secretHeader, secretEnv, "query-super-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("trace leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "token=%3Credacted%3E") {
		t.Fatalf("sanitized endpoint missing redaction marker: %s", text)
	}
}

func TestSanitizeProcessArgsAndEnvTraceMetadata(t *testing.T) {
	args := sanitizeProcessArgs([]string{"serve", "--token", "token-secret", "--api-key=key-secret", "--mode", "safe"})
	text := strings.Join(args, " ")
	if strings.Contains(text, "token-secret") || strings.Contains(text, "key-secret") {
		t.Fatalf("sanitized args leaked secret: %q", text)
	}
	if text != "serve --token <redacted> --api-key=<redacted> --mode safe" {
		t.Fatalf("sanitized args = %q", text)
	}
	fields := upstreamServerTraceFields(Server{ID: "stdio", Transport: "stdio", Command: "node", Args: []string{"server.js"}, Env: map[string]string{"API_TOKEN": "env-secret"}})
	if strings.Contains(fmt.Sprint(fields), "env-secret") {
		t.Fatalf("upstream server trace fields leaked env value: %#v", fields)
	}
}

func TestRemoteErrorsAreBoundedAndCredentialSafeInTraces(t *testing.T) {
	secretHeader := "header-super-secret"
	secretEnv := "env-super-secret"
	querySecret := "query-super-secret"
	remoteCause := errors.New(
		"remote https://example.test/mcp?token=" + querySecret +
			" failed token=" + secretHeader + " env=" + secretEnv + " " +
			strings.Repeat("x", maxRemoteErrorBytes),
	)
	client := &traceTestClient{toolsErr: remoteCause}
	events := []tracepkg.Event{}
	observer := func(event tracepkg.Event) { events = append(events, event) }
	manager := NewManagerWithClient(nil, client).SetTraceObserver(observer)
	if err := manager.Add(Server{
		ID: "demo", Enabled: true, Transport: "http",
		URL:     "https://example.test/mcp?token=" + querySecret,
		Headers: map[string]string{"Authorization": "Bearer " + secretHeader},
		Env:     map[string]string{"PASSWORD": secretEnv},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := tracepkg.WithObserver(t.Context(), observer)
	_, err := manager.Tools(ctx, "demo", false)
	if err == nil {
		t.Fatal("expected remote discovery error")
	}
	if !errors.Is(err, remoteCause) {
		t.Fatalf("remote cause identity lost: %v", err)
	}
	if len(err.Error()) > maxRemoteErrorBytes {
		t.Fatalf("remote error not bounded: %d", len(err.Error()))
	}
	text := fmt.Sprint(events)
	for _, secret := range []string{secretHeader, secretEnv, querySecret} {
		if strings.Contains(err.Error(), secret) || strings.Contains(text, secret) {
			t.Fatalf("remote credential leaked %q: err=%q trace=%s", secret, err.Error(), text)
		}
	}
	if !strings.Contains(err.Error(), "token=%3Credacted%3E") {
		t.Fatalf("remote URL query was not sanitized: %q", err.Error())
	}
}

func traceEventHasField(events []tracepkg.Event, name, key string, expected any) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		for _, field := range event.Fields {
			if field.Key == key && fmt.Sprint(field.Value) == fmt.Sprint(expected) {
				return true
			}
		}
	}
	return false
}
