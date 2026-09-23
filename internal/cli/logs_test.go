package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestLogsTailAppliesAfterVisibilityFiltering(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	for _, event := range []runtimeevent.Event{
		{Sequence: 1, Time: base, RunID: "run", Level: "info", Kind: "success", Name: "server.first", Component: "SERVER", Message: "First visible"},
		{Sequence: 2, Time: base.Add(time.Second), RunID: "run", Level: "info", Visibility: logger.VisibilityVerbose, Kind: "info", Name: "server.verbose", Component: "SERVER", Message: "Verbose hidden"},
		{Sequence: 3, Time: base.Add(2 * time.Second), RunID: "run", Level: "info", Kind: "success", Name: "server.last", Component: "SERVER", Message: "Last visible"},
	} {
		if err := journal.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	output := executeLogsCommand(t, root, []string{"logs", "-n", "2"})
	if !strings.Contains(output, "First visible") || !strings.Contains(output, "Last visible") || strings.Contains(output, "Verbose hidden") {
		t.Fatalf("default logs output = %q", output)
	}
	firstTimestamp := base.Local().Format("15:04:05")
	lastTimestamp := base.Add(2 * time.Second).Local().Format("15:04:05")
	if !strings.Contains(output, firstTimestamp) || !strings.Contains(output, "── session run") {
		t.Fatalf("default logs output missing replay timestamp/session header: %q", output)
	}
	withoutTime := executeLogsCommand(t, root, []string{"logs", "-n", "1", "--no-time"})
	if strings.Contains(withoutTime, lastTimestamp) {
		t.Fatalf("--no-time output = %q", withoutTime)
	}
	debugOutput := executeLogsCommand(t, root, []string{"--debug", "logs", "-n", "3"})
	if !strings.Contains(debugOutput, "Verbose hidden") {
		t.Fatalf("debug logs did not include hidden event: %q", debugOutput)
	}
}

func TestLogsSessionFilterUsesDisplayedPrefix(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []runtimeevent.Event{
		{Sequence: 1, Time: time.Now(), RunID: "run_abcdef1234567890", PID: 10, Level: "info", Kind: "success", Name: "test.one", Message: "Wanted session"},
		{Sequence: 1, Time: time.Now(), RunID: "run_other1234567890", PID: 20, Level: "info", Kind: "success", Name: "test.two", Message: "Other session"},
	} {
		if err := journal.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	output := executeLogsCommand(t, root, []string{"logs", "--session", "run_abcdef123456"})
	if !strings.Contains(output, "Wanted session") || strings.Contains(output, "Other session") {
		t.Fatalf("session-filtered output = %q", output)
	}
}

func TestShortSessionIDKeepsCompactHexRunID(t *testing.T) {
	const value = "run_0123456789abcdef"
	if got := shortSessionID(value); got != value {
		t.Fatalf("short session id=%q want=%q", got, value)
	}
}

func TestLogsDefaultsToLatestSessionAndAllRestoresHistory(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for _, event := range []runtimeevent.Event{
		{Sequence: 1, Time: base, RunID: "run_old", Level: "info", Kind: "success", Name: "test.old", Message: "Old session"},
		{Sequence: 1, Time: base.Add(time.Second), RunID: "run_latest", Level: "info", Kind: "success", Name: "test.latest", Message: "Latest session"},
	} {
		if err := journal.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	output := executeLogsCommand(t, root, []string{"logs"})
	if !strings.Contains(output, "Latest session") || strings.Contains(output, "Old session") {
		t.Fatalf("latest-session output = %q", output)
	}
	allOutput := executeLogsCommand(t, root, []string{"logs", "--all"})
	if !strings.Contains(allOutput, "Latest session") || !strings.Contains(allOutput, "Old session") {
		t.Fatalf("all-session output = %q", allOutput)
	}
}

func TestLogsSelectsLatestSessionBeforeOtherFilters(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for _, event := range []runtimeevent.Event{
		{Time: base, RunID: "run_old", Level: "error", Kind: "error", Name: "test.old", Message: "Old matching error"},
		{Time: base.Add(time.Second), RunID: "run_latest", Level: "info", Kind: "success", Name: "test.latest", Message: "Latest success"},
	} {
		if err := journal.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	output := executeLogsCommand(t, root, []string{"logs", "--level", "error"})
	if strings.Contains(output, "Old matching error") {
		t.Fatalf("filter jumped to an older session: %q", output)
	}
}

func TestLogsRejectsAllWithSession(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "logs", "--all", "--session", "run_test"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("logs accepted --all with --session")
	}
}

func TestLogsJSONKeepsSessionStructuredWithoutTextHeader(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Sequence: 1, Time: time.Now(), RunID: "run_json_session", PID: 42, Level: "info", Kind: "success", Name: "server.ready", Component: "SERVER", Message: "Server ready"}); err != nil {
		t.Fatal(err)
	}
	output := executeLogsCommand(t, root, []string{"--log-format=json", "logs"})
	if strings.Contains(output, "── session") {
		t.Fatalf("json output leaked text session header: %q", output)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &value); err != nil {
		t.Fatalf("json output = %q: %v", output, err)
	}
	if value["event"] != "server.ready" {
		t.Fatalf("json event = %#v", value)
	}
	if value["run_id"] != "run_json_session" || value["pid"] != float64(42) {
		t.Fatalf("json session metadata = %#v", value)
	}
}

func TestLogsWorkspacePathFilter(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item, err := manager.Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []runtimeevent.Event{
		{Time: time.Now(), Level: "info", Kind: "success", Name: "tool.call.completed", Component: "TOOL", Message: "Wanted", WorkspaceID: item.ID, Tool: "run_command"},
		{Time: time.Now(), Level: "info", Kind: "success", Name: "tool.call.completed", Component: "TOOL", Message: "Other", WorkspaceID: "ws_other", Tool: "run_command"},
	} {
		if err := journal.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	output := executeLogsCommand(t, root, []string{"logs", "--workspace", workspaceRoot})
	if !strings.Contains(output, "Wanted") || strings.Contains(output, "Other") {
		t.Fatalf("workspace logs output = %q", output)
	}
}

func TestLogsFollowStreamsRuntimeEvents(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	stream := runtimeevent.NewStream(runtimeevent.Metadata{RunID: "run_follow", PID: os.Getpid()})
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_follow", Events: stream, Reload: func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil }, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_follow", ConfigRoot: root}
	}, Shutdown: func() {}, ClearLogs: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(ctx)
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "logs", "-f", "-n", "0"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	time.Sleep(100 * time.Millisecond)
	if err := stream.WriteEvent(logger.Event{Time: time.Now(), Level: logger.Info, Kind: logger.KindSuccess, Name: "server.follow", Component: "SERVER", Message: "Followed live event"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logs follow did not stop after context cancellation")
	}
	if !strings.Contains(output.String(), "Followed live event") {
		t.Fatalf("follow output = %q", output.String())
	}
}

func TestLogsPathAndClearWhileStopped(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Time: time.Now(), Level: "info", Kind: "info", Name: "test.event", Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	pathOutput := executeLogsCommand(t, root, []string{"logs", "path"})
	if !strings.Contains(pathOutput, runtimeevent.Path(root)) {
		t.Fatalf("logs path output = %q", pathOutput)
	}
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "logs", "clear"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("logs clear without force = %v", err)
	}
	cmd = newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "logs", "clear", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runtimeevent.Path(root)); !os.IsNotExist(err) {
		t.Fatalf("runtime journal still exists after clear: %v", err)
	}
}

func TestLogsClearUsesRuntimeControlWhenRunning(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	clearCalls := 0
	control, err := startRuntimeControl(runtimeControlOptions{RunID: "run_clear", Events: runtimeevent.NewStream(runtimeevent.Metadata{}), Reload: func(context.Context) (runtimeReloadResult, error) {
		return runtimeReloadResult{PID: os.Getpid()}, nil
	}, Status: func() runtimeStatusResult {
		return runtimeStatusResult{PID: os.Getpid(), RunID: "run_clear", ConfigRoot: root}
	}, Shutdown: func() {}, ClearLogs: func() error {
		clearCalls++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	output := executeLogsCommand(t, root, []string{"logs", "clear", "--force"})
	if clearCalls != 1 || !strings.Contains(output, "Runtime logs cleared") {
		t.Fatalf("clear calls=%d output=%q", clearCalls, output)
	}
}

func executeLogsCommand(t *testing.T, root string, args []string) string {
	t.Helper()
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetContext(context.Background())
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(append([]string{"--config-dir", root}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

var _ = filepath.Separator
