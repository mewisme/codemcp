package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	shellruntime "go.mewis.me/codemcp/internal/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestWorkspaceExecutionAPIListSnapshotAndIsolation(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := shellruntime.NewExecutionHub()
	run := hub.Begin(shellruntime.ExecutionInput{WorkspaceID: first.ID, Tool: "run_command", Command: "printf hello", CWD: first.Path, Source: "mcp"})
	_, _ = run.Writer("stdout").Write([]byte("hello\n"))
	code := 0
	run.Finish(shellruntime.ExecutionStatusSuccess, &code, false)
	handler := New(API{Workspaces: manager, Executions: hub})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+first.ID+"/executions?limit=10", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var items []shellruntime.ExecutionInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil || len(items) != 1 || items[0].ID != run.ID() {
		t.Fatalf("list=%#v err=%v", items, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+first.ID+"/executions/"+run.ID(), nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "hello") {
		t.Fatalf("snapshot status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+second.ID+"/executions/"+run.ID(), nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace snapshot status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkspaceExecutionSSEStartsWithSnapshotAndStreamsUntilCompletion(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := shellruntime.NewExecutionHub()
	run := hub.Begin(shellruntime.ExecutionInput{WorkspaceID: item.ID, Tool: "run_command", Command: "demo", CWD: item.Path, Source: "mcp"})
	_, _ = run.Writer("stdout").Write([]byte("before\n"))
	server := httptest.NewServer(New(API{Workspaces: manager, Executions: hub}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/workspaces/"+item.ID+"/executions/"+run.ID()+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	ready := scanEventData(t, scanner, "ready")
	var snapshot shellruntime.ExecutionSnapshot
	if err := json.Unmarshal([]byte(ready), &snapshot); err != nil || snapshot.Stdout != "before\n" || snapshot.Execution.Status != shellruntime.ExecutionStatusRunning {
		t.Fatalf("ready snapshot=%#v err=%v", snapshot, err)
	}
	_, _ = run.Writer("stderr").Write([]byte("during\n"))
	output := scanEventData(t, scanner, shellruntime.ExecutionEventOutput)
	if !strings.Contains(output, `"stream":"stderr"`) || !strings.Contains(output, "during") {
		t.Fatalf("output event=%q", output)
	}
	code := 0
	run.Finish(shellruntime.ExecutionStatusSuccess, &code, false)
	completed := scanEventData(t, scanner, shellruntime.ExecutionEventCompleted)
	if !strings.Contains(completed, `"status":"success"`) {
		t.Fatalf("completed event=%q", completed)
	}
	done := make(chan struct{})
	go func() {
		for scanner.Scan() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("execution SSE did not close after completion")
	}
}

func TestWorkspaceExecutionFeedSSEReplaysAndContinuesAcrossCommands(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := shellruntime.NewExecutionHub()
	first := hub.Begin(shellruntime.ExecutionInput{WorkspaceID: item.ID, Tool: "run_command", Command: "first", CWD: item.Path, Source: "mcp"})
	_, _ = first.Writer("stdout").Write([]byte("before\n"))
	code := 0
	first.Finish(shellruntime.ExecutionStatusSuccess, &code, false)
	server := httptest.NewServer(New(API{Workspaces: manager, Executions: hub}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/workspaces/"+item.ID+"/executions/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	ready := scanEventData(t, scanner, "ready")
	var metadata struct {
		LatestSequence uint64 `json:"latest_sequence"`
		ReplayCount    int    `json:"replay_count"`
	}
	if err := json.Unmarshal([]byte(ready), &metadata); err != nil || metadata.LatestSequence != 3 || metadata.ReplayCount != 3 {
		t.Fatalf("feed metadata=%#v err=%v", metadata, err)
	}
	replayStarted := scanEventData(t, scanner, shellruntime.ExecutionEventStarted)
	replayOutput := scanEventData(t, scanner, shellruntime.ExecutionEventOutput)
	replayCompleted := scanEventData(t, scanner, shellruntime.ExecutionEventCompleted)
	if !strings.Contains(replayStarted, first.ID()) || !strings.Contains(replayOutput, "before") || !strings.Contains(replayCompleted, `"status":"success"`) {
		t.Fatalf("replay started=%q output=%q completed=%q", replayStarted, replayOutput, replayCompleted)
	}

	second := hub.Begin(shellruntime.ExecutionInput{WorkspaceID: item.ID, Tool: "run_command", Command: "second", CWD: item.Path, Source: "mcp"})
	started := scanEventData(t, scanner, shellruntime.ExecutionEventStarted)
	if !strings.Contains(started, second.ID()) || !strings.Contains(started, `"command":"second"`) {
		t.Fatalf("started event=%q", started)
	}
	_, _ = second.Writer("stderr").Write([]byte("live\n"))
	output := scanEventData(t, scanner, shellruntime.ExecutionEventOutput)
	if !strings.Contains(output, `"stream":"stderr"`) || !strings.Contains(output, "live") {
		t.Fatalf("output event=%q", output)
	}
	second.Finish(shellruntime.ExecutionStatusSuccess, &code, false)
	completed := scanEventData(t, scanner, shellruntime.ExecutionEventCompleted)
	if !strings.Contains(completed, second.ID()) || !strings.Contains(completed, `"status":"success"`) {
		t.Fatalf("completed event=%q", completed)
	}
}
