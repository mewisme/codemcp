package upstream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManagerListIsDeterministic(t *testing.T) {
	manager := NewManager(nil)
	if err := manager.Add(Server{ID: "b", Name: "B", Transport: "http", URL: "https://b.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(Server{ID: "a", Name: "A", Transport: "http", URL: "https://a.invalid"}); err != nil {
		t.Fatal(err)
	}
	servers := manager.List()
	if len(servers) != 2 || servers[0].ID != "a" || servers[1].ID != "b" {
		t.Fatalf("unexpected order: %+v", servers)
	}
}

func TestManagerRollsBackFailedPersist(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(NewStore(filepath.Join(file, "upstreams.json")))
	if err := manager.Add(Server{ID: "a", Name: "A", Transport: "http", URL: "https://a.invalid"}); err == nil {
		t.Fatal("expected persistence error")
	}
	if len(manager.List()) != 0 {
		t.Fatalf("failed add must roll back: %+v", manager.List())
	}
}

type managerLifecycleClient struct {
	closes   []string
	clears   []string
	clearErr error
}

func (*managerLifecycleClient) Connect(context.Context, Server) error { return nil }
func (c *managerLifecycleClient) Close(_ context.Context, id string) error {
	c.closes = append(c.closes, id)
	return nil
}
func (*managerLifecycleClient) Tools(context.Context, string) ([]Tool, error) { return nil, nil }
func (*managerLifecycleClient) Call(context.Context, string, string, map[string]any) (CallResult, error) {
	return CallResult{}, nil
}
func (*managerLifecycleClient) PID(string) int { return 0 }
func (c *managerLifecycleClient) ClearOAuthCredential(id string) error {
	c.clears = append(c.clears, id)
	return c.clearErr
}

func TestManagerReconfigureDisconnectsLiveRuntime(t *testing.T) {
	client := &managerLifecycleClient{}
	manager := NewManagerWithClient(nil, client)
	server := Server{ID: "alpha", Name: "Alpha", Transport: "http", URL: "https://one.example/mcp", Auth: AuthConfig{Type: "auto", Scope: "read"}}
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	server.Name = "Renamed"
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	if len(client.closes) != 1 || client.closes[0] != "alpha" {
		t.Fatalf("closes = %#v", client.closes)
	}
	if len(client.clears) != 0 {
		t.Fatalf("cosmetic update cleared OAuth credential: %#v", client.clears)
	}
}

func TestManagerInvalidatesOAuthCredentialOnBindingChanges(t *testing.T) {
	base := Server{ID: "alpha", Name: "Alpha", Transport: "http", URL: "https://one.example/mcp", Auth: AuthConfig{Type: "auto", Scope: "read"}}
	cases := map[string]func(Server) Server{
		"url": func(value Server) Server {
			value.URL = "https://two.example/mcp"
			return value
		},
		"scope": func(value Server) Server {
			value.Auth.Scope = "read write"
			return value
		},
		"auth-none": func(value Server) Server {
			value.Auth.Type = "none"
			return value
		},
		"static-header": func(value Server) Server {
			value.Headers = map[string]string{"Authorization": "Bearer static"}
			return value
		},
		"bearer-env": func(value Server) Server {
			value.BearerTokenEnvVar = "MCP_TOKEN"
			return value
		},
		"transport": func(value Server) Server {
			value.Transport = "stdio"
			value.Command = "example"
			value.URL = ""
			value.Auth.Type = "none"
			return value
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			client := &managerLifecycleClient{}
			manager := NewManagerWithClient(nil, client)
			if err := manager.Add(base); err != nil {
				t.Fatal(err)
			}
			if err := manager.Add(mutate(base)); err != nil {
				t.Fatal(err)
			}
			if len(client.closes) != 1 || len(client.clears) != 1 || client.clears[0] != "alpha" {
				t.Fatalf("closes=%#v clears=%#v", client.closes, client.clears)
			}
		})
	}
}

func TestManagerPreservesOAuthCredentialAcrossCompatibleManagedUpdate(t *testing.T) {
	client := &managerLifecycleClient{}
	manager := NewManagerWithClient(nil, client)
	server := Server{ID: "alpha", Name: "Alpha", Transport: "http", URL: "https://one.example/mcp", Auth: AuthConfig{Type: "auto", Scope: "read"}}
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	server.Auth.Type = "oauth"
	server.Expose = "none"
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	if len(client.clears) != 0 {
		t.Fatalf("compatible OAuth update cleared credential: %#v", client.clears)
	}
}

func TestManagerRemoveClearsOAuthCredential(t *testing.T) {
	client := &managerLifecycleClient{}
	manager := NewManagerWithClient(nil, client)
	if err := manager.Add(Server{ID: "alpha", Name: "Alpha", Transport: "http", URL: "https://one.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove("alpha"); err != nil {
		t.Fatal(err)
	}
	if len(client.closes) != 1 || len(client.clears) != 1 || client.clears[0] != "alpha" {
		t.Fatalf("closes=%#v clears=%#v", client.closes, client.clears)
	}
}

func TestManagerReturnsOAuthCleanupFailureAfterPersistedMutation(t *testing.T) {
	client := &managerLifecycleClient{clearErr: errors.New("cleanup failed")}
	manager := NewManagerWithClient(nil, client)
	server := Server{ID: "alpha", Name: "Alpha", Transport: "http", URL: "https://one.example/mcp", Auth: AuthConfig{Type: "oauth"}}
	if err := manager.Add(server); err != nil {
		t.Fatal(err)
	}
	server.Auth.Type = "none"
	err := manager.Add(server)
	if err == nil || err.Error() != "cleanup failed" {
		t.Fatalf("error = %v", err)
	}
	stored, ok := manager.Get("alpha")
	if !ok || stored.Auth.Type != "none" {
		t.Fatalf("persisted update was lost: %#v", stored)
	}
}

type managerSubscriptionClient struct {
	mu       sync.Mutex
	tools    []Tool
	toolGets int
	started  chan struct{}
	trigger  chan struct{}
}

func (c *managerSubscriptionClient) Connect(context.Context, Server) error { return nil }
func (c *managerSubscriptionClient) Close(context.Context, string) error   { return nil }
func (c *managerSubscriptionClient) Tools(context.Context, string) ([]Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.toolGets++
	return append([]Tool(nil), c.tools...), nil
}
func (*managerSubscriptionClient) Call(context.Context, string, string, map[string]any) (CallResult, error) {
	return CallResult{}, nil
}
func (*managerSubscriptionClient) PID(string) int { return 0 }
func (c *managerSubscriptionClient) ListenToolsChanged(ctx context.Context, _ string, onChange func()) error {
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

func TestManagerInvalidatesToolsCacheFromSubscription(t *testing.T) {
	client := &managerSubscriptionClient{
		tools:   []Tool{{Name: "one"}},
		started: make(chan struct{}, 1),
		trigger: make(chan struct{}, 1),
	}
	manager := NewManagerWithClient(nil, client)
	manager.SetToolsChangedHandler(func(context.Context, string) error { return nil })
	if err := manager.Add(Server{ID: "demo", Enabled: true, Transport: "http", URL: "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Tools(context.Background(), "demo", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("subscription did not start")
	}
	if _, err := manager.Tools(context.Background(), "demo", false); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	if client.toolGets != 1 {
		t.Fatalf("cached tools fetched %d times", client.toolGets)
	}
	client.mu.Unlock()

	client.trigger <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.RLock()
		_, cached := manager.cache["demo"]
		manager.mu.RUnlock()
		if !cached {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tools cache was not invalidated")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := manager.Tools(context.Background(), "demo", false); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	if client.toolGets != 2 {
		t.Fatalf("tools after invalidation fetched %d times", client.toolGets)
	}
	client.mu.Unlock()
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCreateBatchIsAtomic(t *testing.T) {
	manager := NewManager(nil)
	invalid := []Server{{ID: "good", Transport: "http", URL: "https://good.example/mcp"}, {ID: "bad", Transport: "stdio"}}
	if err := manager.CreateBatch(invalid); err == nil || len(manager.List()) != 0 {
		t.Fatalf("invalid batch err=%v servers=%#v", err, manager.List())
	}
	if err := manager.Add(Server{ID: "existing", Transport: "http", URL: "https://existing.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.CreateBatch([]Server{{ID: "new", Transport: "stdio", Command: "node"}, {ID: "existing", Transport: "stdio", Command: "node"}}); err == nil {
		t.Fatal("existing ID batch was accepted")
	}
	if _, ok := manager.Get("new"); ok {
		t.Fatal("batch partially added server before existing ID failure")
	}
}

func TestManagerCreateBatchPersistsOnceAndRollsBackFailure(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(root, "upstreams.json"))
	manager := NewManager(store)
	servers := []Server{{ID: "a", Transport: "stdio", Command: "node"}, {ID: "b", Transport: "http", URL: "https://b.example/mcp"}}
	if err := manager.CreateBatch(servers); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || len(loaded) != 2 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	failed := NewManager(NewStore(filepath.Join(file, "upstreams.json")))
	if err := failed.CreateBatch(servers); err == nil || len(failed.List()) != 0 {
		t.Fatalf("failed batch err=%v servers=%#v", err, failed.List())
	}
}
