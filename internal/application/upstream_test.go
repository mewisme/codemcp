package application

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestUpstreamServiceOwnsValidationReconciliationAndCanonicalTrace(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	var events []tracepkg.Event
	observer := func(event tracepkg.Event) { events = append(events, event) }
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json"))).SetTraceObserver(observer)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	var reconciles atomic.Int32
	service := NewUpstreamService(manager, func(context.Context) error {
		reconciles.Add(1)
		return nil
	})
	ctx := tracepkg.WithObserver(t.Context(), observer)

	if _, err := service.Create(ctx, upstream.Server{ID: "private", Enabled: true, Transport: "http", URL: "http://127.0.0.1:9999/mcp"}); ErrorCodeOf(err) != ErrorInvalidArgument {
		t.Fatalf("private HTTP validation error=%v code=%s", err, ErrorCodeOf(err))
	}
	if _, err := service.Create(ctx, upstream.Server{ID: "stdio-missing", Enabled: true, Transport: "stdio"}); ErrorCodeOf(err) != ErrorInvalidArgument {
		t.Fatalf("stdio validation error=%v code=%s", err, ErrorCodeOf(err))
	}
	if reconciles.Load() != 0 || len(manager.List()) != 0 {
		t.Fatalf("invalid mutations changed state: reconciles=%d servers=%#v", reconciles.Load(), manager.List())
	}

	created, err := service.Create(ctx, upstream.Server{
		ID: "local", Name: "Local", Enabled: true, Transport: "stdio", Command: "node",
		Args: []string{"server.js"}, Env: map[string]string{"MODE": "test"}, CWD: t.TempDir(), Expose: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Value.IdleTimeoutSec != 600 || reconciles.Load() != 1 {
		t.Fatalf("created=%#v reconciles=%d", created.Value, reconciles.Load())
	}

	updated := created.Value
	updated.Name = "Updated"
	if _, err := service.Update(ctx, created.Value.ID, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Disable(ctx, created.Value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enable(ctx, created.Value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Remove(ctx, created.Value.ID); err != nil {
		t.Fatal(err)
	}
	if reconciles.Load() != 5 {
		t.Fatalf("mutation reconcile count=%d want=5", reconciles.Load())
	}
	if len(manager.List()) != 0 {
		t.Fatalf("remove left state: %#v", manager.List())
	}

	for _, operation := range []capability.ID{
		capability.UpstreamServerAdd,
		capability.UpstreamServerConfigure,
		capability.UpstreamServerDisable,
		capability.UpstreamServerEnable,
		capability.UpstreamServerRemove,
	} {
		if !hasCanonicalOperationEvent(events, operation, "completed") {
			t.Fatalf("canonical trace missing for %s: %#v", operation, events)
		}
	}
}

func hasCanonicalOperationEvent(events []tracepkg.Event, operation capability.ID, phase string) bool {
	name := string(operation) + "." + phase
	for _, event := range events {
		if event.Name != name {
			continue
		}
		for _, field := range event.Fields {
			if field.Key == "operation_id" && field.Value == string(operation) {
				return true
			}
		}
	}
	return false
}

func TestUpstreamServiceDispatcherAndLookupErrors(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := upstream.NewManager(upstream.NewStore(filepath.Join(t.TempDir(), "upstreams.json")))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	service := NewUpstreamService(manager)
	dispatcher := NewDispatcher()
	if err := BindUpstreamOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []capability.ID{
		capability.UpstreamServerList,
		capability.UpstreamServerShow,
		capability.UpstreamServerAdd,
		capability.UpstreamServerConfigure,
		capability.UpstreamServerRemove,
		capability.UpstreamServerEnable,
		capability.UpstreamServerDisable,
		capability.UpstreamServerStatus,
		capability.UpstreamServerTools,
	} {
		if _, ok := dispatcher.handlers[operation]; !ok {
			t.Fatalf("missing canonical upstream binding %s", operation)
		}
	}
	if _, err := service.Get(t.Context(), "missing"); ErrorCodeOf(err) != ErrorNotFound {
		t.Fatalf("missing lookup error=%v code=%s", err, ErrorCodeOf(err))
	}
	if _, err := service.Create(t.Context(), upstream.Server{ID: "bad", Enabled: true, Transport: "http", URL: "http://10.0.0.1/mcp"}); ErrorCodeOf(err) != ErrorInvalidArgument || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid upstream error=%v code=%s", err, ErrorCodeOf(err))
	}
}
