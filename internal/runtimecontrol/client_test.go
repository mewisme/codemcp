package runtimecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestRequestUsesAuthenticatedLoopbackState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/test" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input["value"] != "ok" {
			t.Fatalf("input=%#v", input)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "done"})
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	var output map[string]string
	state, err := Request(t.Context(), http.MethodPost, "/test", map[string]string{"value": "ok"}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if state.PID != os.Getpid() || output["result"] != "done" {
		t.Fatalf("state=%#v output=%#v", state, output)
	}
}

func TestRequestPropagatesStructuredRuntimeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "request already resolved"})
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	if _, err := Request(t.Context(), http.MethodGet, "/test", nil, nil); err == nil || err.Error() != "request already resolved" {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadRejectsMissingOrNonLoopbackState(t *testing.T) {
	root := setupRuntimeControlRoot(t)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "no running server") {
		t.Fatalf("missing err=%v", err)
	}
	state := State{PID: os.Getpid(), Address: "8.8.8.8:1234", Token: "token", ConfigRoot: root}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("non-loopback err=%v", err)
	}
}

func TestRequestRejectsRelativePath(t *testing.T) {
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, "http://127.0.0.1:1", "runtime-secret")
	if _, err := Request(context.Background(), http.MethodGet, "requests", nil, nil); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("err=%v", err)
	}
}

func TestRequestEmitsDeepTraceWithoutControlToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"pid": os.Getpid()})
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) { events = append(events, event) })
	var output map[string]any
	if _, err := Request(ctx, http.MethodGet, "/status", nil, &output); err != nil {
		t.Fatal(err)
	}
	text, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "runtime-secret") {
		t.Fatalf("trace leaked runtime-control token: %s", text)
	}
	for _, name := range []string{"runtime.control.state.read.completed", "runtime.control.state.decode.completed", "runtime.control.request.completed", "http.request.completed"} {
		found := false
		for _, event := range events {
			if event.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
}

func TestValidatePIDAndStatusWaitEmitLifecycleTrace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/wait" || r.URL.Query().Get("lifecycle") != "bootstrapping" {
			t.Fatalf("request=%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(RuntimeStatus{PID: os.Getpid(), RunID: "run_wait_trace", Lifecycle: "ready"})
	}))
	defer server.Close()
	root := setupRuntimeControlRoot(t)
	writeRuntimeControlState(t, root, server.URL, "runtime-secret")
	events := []tracepkg.Event{}
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) { events = append(events, event) })
	status, err := WaitStatusChange(ctx, "bootstrapping")
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != "ready" || status.RunID != "run_wait_trace" {
		t.Fatalf("status=%#v", status)
	}
	if !runtimeControlTraceFields(events, "runtime.control.pid.validate.completed", map[string]any{"operation": "status-wait", "pid": os.Getpid()}) {
		t.Fatalf("missing PID validation trace: %#v", events)
	}
	if !runtimeControlTraceFields(events, "runtime.control.status-wait.completed", map[string]any{"previous_lifecycle": "bootstrapping", "current_lifecycle": "ready", "changed": true, "pid": os.Getpid()}) {
		t.Fatalf("missing status wait transition trace: %#v", events)
	}
	if err := ValidatePID(ctx, 10, 11, "test"); err == nil {
		t.Fatal("PID mismatch unexpectedly succeeded")
	}
	if !runtimeControlTraceFields(events, "runtime.control.pid.validate.failed", map[string]any{"operation": "test", "expected_pid": 10, "actual_pid": 11}) {
		t.Fatalf("missing PID mismatch trace: %#v", events)
	}
}

func runtimeControlTraceFields(events []tracepkg.Event, name string, expected map[string]any) bool {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		values := map[string]any{}
		for _, field := range event.Fields {
			values[field.Key] = field.Value
		}
		matched := true
		for key, want := range expected {
			if fmt.Sprint(values[key]) != fmt.Sprint(want) {
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

func setupRuntimeControlRoot(t *testing.T) string {
	t.Helper()
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeRuntimeControlState(t *testing.T, root, rawURL, token string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	state := State{PID: os.Getpid(), Address: parsed.Host, Token: token, StartedAt: time.Now().UTC(), ConfigRoot: root}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}
