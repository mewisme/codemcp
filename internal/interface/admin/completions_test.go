package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/tools"
)

func TestCompletionReadsUseRuntimeHistoryAndStayReadOnly(t *testing.T) {
	root := t.TempDir()
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = configformat.SetRootPath(previous) }()

	runtime := tools.NewRuntime()
	defer runtime.CompletionHooks.Stop()
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentID := agentcompletion.DeriveAgentID("admin-completion-caller", "admin-completion-generation")
	first := acceptAdminCompletion(t, runtime, agentcompletion.Identity{AgentID: agentID, Source: "mcp"}, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusBlocked, Title: "Waiting", Summary: "Needs input",
	})
	second := acceptAdminCompletion(t, runtime, agentcompletion.Identity{AgentID: agentID, Source: "mcp"}, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusCompleted, Title: "Finished", Summary: "Verified",
	})

	handler := New(API{Tools: runtime})

	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/api/completions?workspace_id="+workspace.ID+"&limit=20", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%q", listRecorder.Code, listRecorder.Body.String())
	}
	var records []agentcompletion.Record
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].ID != first.ID || records[1].ID != second.ID {
		t.Fatalf("list=%#v", records)
	}

	currentRecorder := httptest.NewRecorder()
	handler.ServeHTTP(currentRecorder, httptest.NewRequest(http.MethodGet, "/api/completions/current?workspace_id="+workspace.ID, nil))
	if currentRecorder.Code != http.StatusOK {
		t.Fatalf("current status=%d body=%q", currentRecorder.Code, currentRecorder.Body.String())
	}
	var current agentcompletion.Record
	if err := json.Unmarshal(currentRecorder.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.ID != second.ID {
		t.Fatalf("current=%#v want=%s", current, second.ID)
	}

	viewRecorder := httptest.NewRecorder()
	handler.ServeHTTP(viewRecorder, httptest.NewRequest(http.MethodGet, "/api/completions/view/"+second.ID, nil))
	if viewRecorder.Code != http.StatusOK {
		t.Fatalf("view status=%d body=%q", viewRecorder.Code, viewRecorder.Body.String())
	}
	var viewed agentcompletion.Record
	if err := json.Unmarshal(viewRecorder.Body.Bytes(), &viewed); err != nil {
		t.Fatal(err)
	}
	if viewed.ID != second.ID || viewed.Sequence != second.Sequence {
		t.Fatalf("view=%#v want=%#v", viewed, second)
	}

	before, err := runtime.Completions.RecentWorkspace(workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/completions"},
		{http.MethodPut, "/api/completions/current?workspace_id=" + workspace.ID},
		{http.MethodDelete, "/api/completions/view/" + second.ID},
		{http.MethodPatch, "/api/completions/stream"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status=%d body=%q", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}
	after, err := runtime.Completions.RecentWorkspace(workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || after[len(after)-1].ID != before[len(before)-1].ID {
		t.Fatalf("read-only Admin surface changed completion truth: before=%#v after=%#v", before, after)
	}
}

func TestCompletionFeedUsesRuntimeServiceSnapshotAndEvents(t *testing.T) {
	root := t.TempDir()
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = configformat.SetRootPath(previous) }()

	runtime := tools.NewRuntime()
	defer runtime.CompletionHooks.Stop()
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentID := agentcompletion.DeriveAgentID("admin-feed-caller", "admin-feed-generation")
	first := acceptAdminCompletion(t, runtime, agentcompletion.Identity{AgentID: agentID, Source: "mcp"}, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusPartial, Title: "Partial", Summary: "Initial",
	})

	server := httptest.NewServer(New(API{Tools: runtime}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/completions/stream?workspace_id="+workspace.ID+"&limit=20", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	eventType, data := readAdminCompletionSSE(t, reader)
	if eventType != "ready" {
		t.Fatalf("ready event=%q data=%q", eventType, data)
	}
	var snapshot agentcompletion.Snapshot
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 1 || snapshot.Records[0].ID != first.ID {
		t.Fatalf("snapshot=%#v", snapshot)
	}

	second := acceptAdminCompletion(t, runtime, agentcompletion.Identity{AgentID: agentID, Source: "mcp"}, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusCompleted, Title: "Finished", Summary: "Verified",
	})
	type result struct {
		eventType string
		data      string
	}
	resultCh := make(chan result, 1)
	go func() {
		eventType, data := readAdminCompletionSSE(t, reader)
		resultCh <- result{eventType: eventType, data: data}
	}()
	select {
	case got := <-resultCh:
		if got.eventType != string(agentcompletion.EventAccepted) {
			t.Fatalf("event=%q data=%q", got.eventType, got.data)
		}
		var event agentcompletion.Event
		if err := json.Unmarshal([]byte(got.data), &event); err != nil {
			t.Fatal(err)
		}
		if event.Record.ID != second.ID || event.Record.WorkspaceID != workspace.ID {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("completion stream did not publish accepted event")
	}
}

func acceptAdminCompletion(t *testing.T, runtime *tools.Runtime, identity agentcompletion.Identity, input agentcompletion.Input) agentcompletion.Record {
	t.Helper()
	record, created, err := runtime.Completions.Accept(identity, input)
	if err != nil || !created {
		t.Fatalf("accept record=%#v created=%t err=%v", record, created, err)
	}
	return record
}

func readAdminCompletionSSE(t *testing.T, reader *bufio.Reader) (string, string) {
	t.Helper()
	eventType := "message"
	data := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read completion SSE: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return eventType, data
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") {
			data += strings.TrimPrefix(line, "data: ")
		}
	}
}
