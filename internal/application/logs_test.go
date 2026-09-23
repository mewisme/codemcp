package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestLoadLogsAppliesLatestSessionVisibilityTailAndBufferCap(t *testing.T) {
	root := setupLogsRoot(t)
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	for _, event := range []runtimeevent.Event{
		{Sequence: 1, Time: base, RunID: "run_old", Level: "error", Name: "old", Message: "old"},
		{Sequence: 1, Time: base.Add(time.Second), RunID: "run_new", Level: "info", Name: "one", Message: "one"},
		{Sequence: 2, Time: base.Add(2 * time.Second), RunID: "run_new", Level: "info", Visibility: logger.VisibilityVerbose, Name: "hidden", Message: "hidden"},
		{Sequence: 3, Time: base.Add(3 * time.Second), RunID: "run_new", Level: "warn", Name: "two", Message: "two"},
		{Sequence: 4, Time: base.Add(4 * time.Second), RunID: "run_new", Level: "error", Name: "three", Message: "three"},
	} {
		if err := journal.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := LoadLogs(LogsQueryOptions{Tail: 3}, logger.VisibilityDefault, 2, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Session != "run_new" || snapshot.Total != 3 || !snapshot.Truncated || len(snapshot.Events) != 2 || snapshot.Events[0].Name != "two" || snapshot.Events[1].Name != "three" || snapshot.LatestSequence["run_old"] != 1 || snapshot.LatestSequence["run_new"] != 4 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestBuildLogsQueryUsesRuntimeQuerySemantics(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	query, err := BuildLogsQuery(LogsQueryOptions{Tail: 10, Since: "30m", Until: "2026-09-06T12:00:00Z", Session: "run_abc", Level: "warn", Components: "TOOL, SERVER", Tool: "run_command", Status: "error", Source: "tunnel", Event: "tool.call.*", Grep: "timeout"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if query.Since == nil || !query.Since.Equal(now.Add(-30*time.Minute)) || query.Until == nil || query.RunID != "run_abc" || query.MinLevel != "warn" || len(query.Components) != 2 {
		t.Fatalf("query=%#v", query)
	}
	event := runtimeevent.Event{Time: now.Add(-time.Minute), RunID: "run_abcdef", Level: "error", Component: "TOOL", Name: "tool.call.failed", Tool: "run_command", Status: "error", Source: "tunnel", Message: "timeout"}
	if !query.Match(event) {
		t.Fatalf("query did not match %#v", event)
	}
}

func TestLogFieldsRespectsFieldVisibility(t *testing.T) {
	event := runtimeevent.Event{Fields: []runtimeevent.Field{{Key: "visible", Value: "ok"}, {Key: "debug", Value: "hidden", Visibility: logger.VisibilityDebug}}}
	fields := LogFields(event, logger.VisibilityDefault)
	if len(fields) != 1 || fields[0].Key != "visible" {
		t.Fatalf("fields=%#v", fields)
	}
}

func TestClearLogsUsesRuntimeControlThenFallsBackWhenStopped(t *testing.T) {
	root := setupLogsRoot(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs/clear" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		calls++
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	parsed, _ := url.Parse(server.URL)
	writeRuntimeState(t, root, runtimecontrol.State{PID: os.Getpid(), Address: parsed.Host, Token: "token", ConfigRoot: root})
	if err := ClearLogs(t.Context()); err != nil || calls != 1 {
		t.Fatalf("running clear err=%v calls=%d", err, calls)
	}
	server.Close()
	_ = os.Remove(filepath.Join(root, runtimecontrol.FileName))
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Time: time.Now(), Level: "info", Name: "test", Message: "value"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runtimeevent.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("journal still exists: %v", err)
	}
}

func TestResolveLogWorkspaceByIDAndPath(t *testing.T) {
	setupLogsRoot(t)
	root := t.TempDir()
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{item.ID, root} {
		got, err := ResolveLogWorkspace(value)
		if err != nil || got != item.ID {
			t.Fatalf("resolve %q = %q, %v", value, got, err)
		}
	}
	if _, err := ResolveLogWorkspace(t.TempDir()); err == nil {
		t.Fatal("unregistered workspace path resolved")
	}
}

func TestLoadLogsInfoReportsJournalFilesAndBytes(t *testing.T) {
	root := setupLogsRoot(t)
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Sequence: 1, Time: time.Now().UTC(), RunID: "run_info", Level: "info", Name: "one", Message: "one"}); err != nil {
		t.Fatal(err)
	}
	info, err := LoadLogsInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != runtimeevent.Path(root) || info.Files != 1 || info.Bytes <= 0 {
		t.Fatalf("info=%#v", info)
	}
}

func TestBuildLogsQueryRejectsInvalidInputs(t *testing.T) {
	now := time.Now().UTC()
	for _, options := range []LogsQueryOptions{
		{Tail: -1},
		{Level: "trace"},
		{Event: "["},
		{Since: "-1m"},
		{Since: "not-a-time"},
		{Until: "not-a-time"},
	} {
		if _, err := BuildLogsQuery(options, now); err == nil {
			t.Fatalf("invalid options accepted: %#v", options)
		}
	}
}

func TestParseLogsSinceAcceptsAbsoluteTimestamp(t *testing.T) {
	want := time.Date(2026, 9, 6, 12, 34, 56, 123, time.UTC)
	got, err := ParseLogsSince(want.Format(time.RFC3339Nano), time.Now())
	if err != nil || !got.Equal(want) {
		t.Fatalf("got=%s err=%v want=%s", got, err, want)
	}
}

func setupLogsRoot(t *testing.T) string {
	t.Helper()
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeRuntimeState(t *testing.T, root string, state runtimecontrol.State) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runtimecontrol.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}
