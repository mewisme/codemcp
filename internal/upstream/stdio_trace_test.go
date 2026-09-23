package upstream

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestStdioCloseTraceIncludesExitCodeAndDuration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	events := []tracepkg.Event{}
	observer := func(event tracepkg.Event) { events = append(events, event) }
	ctx := tracepkg.WithObserver(context.Background(), observer)
	transport, err := startStdio(ctx, Server{ID: "stdio-trace", Transport: "stdio", Command: "sh", Args: []string{"-c", "trap '' INT; sleep 2"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	closeCtx, cancel := context.WithTimeout(tracepkg.WithObserver(context.Background(), observer), 2*time.Second)
	defer cancel()
	if err := transport.close(closeCtx); err != nil {
		t.Fatal(err)
	}
	var terminal *tracepkg.Event
	for index := range events {
		if events[index].Name == "upstream.stdio.close.completed" {
			terminal = &events[index]
			break
		}
	}
	if terminal == nil {
		t.Fatalf("missing stdio close completion trace: %#v", events)
	}
	for key, want := range map[string]any{"forced": true} {
		if got, ok := upstreamTraceField(*terminal, key); !ok || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("stdio close %s=%v, want %v: %#v", key, got, want, *terminal)
		}
	}
	for _, key := range []string{"exit_code", "duration_ms"} {
		if _, ok := upstreamTraceField(*terminal, key); !ok {
			t.Fatalf("stdio close trace missing %q: %#v", key, *terminal)
		}
	}
}

func TestStdioStartFailureTraceIncludesExitCodeAndDuration(t *testing.T) {
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) { events = append(events, event) })
	_, err := startStdio(ctx, Server{ID: "stdio-missing", Transport: "stdio", Command: "codemcp-definitely-missing-executable"})
	if err == nil {
		t.Fatal("missing stdio executable unexpectedly started")
	}
	for _, event := range events {
		if event.Name != "upstream.stdio.spawn.failed" {
			continue
		}
		if exitCode, ok := upstreamTraceField(event, "exit_code"); !ok || fmt.Sprint(exitCode) != "-1" {
			t.Fatalf("stdio spawn failure exit_code=%v: %#v", exitCode, event)
		}
		if _, ok := upstreamTraceField(event, "duration_ms"); !ok {
			t.Fatalf("stdio spawn failure missing duration_ms: %#v", event)
		}
		return
	}
	t.Fatalf("missing stdio spawn failure trace: %#v", events)
}

func upstreamTraceField(event tracepkg.Event, key string) (any, bool) {
	for _, field := range event.Fields {
		if field.Key == key {
			return field.Value, true
		}
	}
	return nil, false
}
