package config

import (
	"errors"
	"sync"
	"testing"
)

func TestRuntimeStoreSnapshotIsIsolated(t *testing.T) {
	cfg := Default()
	cfg.HTTP.Exposure = ExposureConfig{Mode: ExposureInterfaces, Interfaces: []string{"Ethernet"}}
	cfg.Shell.Path = []string{"/trusted/bin"}
	store := NewRuntimeStore(cfg)
	snapshot := store.Snapshot()
	snapshot.HTTP.Exposure.Interfaces[0] = "mutated"
	snapshot.Shell.Path[0] = "/mutated/bin"
	if got := store.Snapshot().HTTP.Exposure.Interfaces[0]; got != "Ethernet" {
		t.Fatalf("stored config mutated through snapshot: %q", got)
	}
	if got := store.Snapshot().Shell.Path[0]; got != "/trusted/bin" {
		t.Fatalf("stored shell path mutated through snapshot: %q", got)
	}
}

func TestRuntimeStoreFailedUpdateDoesNotCommit(t *testing.T) {
	store := NewRuntimeStore(Default())
	expected := errors.New("persist failed")
	if _, err := store.Update(func(next Config) (Config, error) {
		next.HTTP.MCP.Port++
		return next, expected
	}); !errors.Is(err, expected) {
		t.Fatalf("update error = %v", err)
	}
	if got := store.Snapshot().HTTP.MCP.Port; got != Default().HTTP.MCP.Port {
		t.Fatalf("failed update committed port %d", got)
	}
}

func TestRuntimeStoreSerializesConcurrentUpdates(t *testing.T) {
	store := NewRuntimeStore(Default())
	const updates = 64
	var wg sync.WaitGroup
	for range updates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.Update(func(next Config) (Config, error) {
				next.HTTP.MCP.Port++
				return next, nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, want := store.Snapshot().HTTP.MCP.Port, Default().HTTP.MCP.Port+updates; got != want {
		t.Fatalf("port = %d, want %d", got, want)
	}
}
