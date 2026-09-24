package shell

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/workspace"
)

func TestExecutionHubSnapshotsAndStreamsOutput(t *testing.T) {
	hub := NewExecutionHub()
	run := hub.Begin(ExecutionInput{
		WorkspaceID: "ws_test", Tool: "run_command", Command: "demo", CWD: "/tmp", Source: "mcp", CallID: "call_test",
		SessionHash: "session-hash", ReceivedByInstanceID: "instance-received", ExecutedByInstanceID: "instance-executed",
	})
	executionHex := strings.TrimPrefix(run.ID(), "exec_")
	if len(executionHex) != 16 {
		t.Fatalf("execution id=%q", run.ID())
	}
	if _, err := hex.DecodeString(executionHex); err != nil {
		t.Fatalf("execution id is not hex: %q", run.ID())
	}
	sub, snapshot, err := hub.Subscribe("ws_test", run.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Unsubscribe(sub)
	if snapshot.Execution.Status != ExecutionStatusRunning || snapshot.Execution.Source != "mcp" || snapshot.Execution.CallID != "call_test" || snapshot.Execution.SessionHash != "session-hash" || snapshot.Execution.ReceivedByInstanceID != "instance-received" || snapshot.Execution.ExecutedByInstanceID != "instance-executed" || snapshot.LatestSequence != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	_, _ = run.Writer("stdout").Write([]byte("hello\n"))
	event := <-sub.Events
	if event.Type != ExecutionEventOutput || event.Stream != "stdout" || event.Data != "hello\n" || event.Sequence != 1 {
		t.Fatalf("output event = %#v", event)
	}
	code := 0
	run.Finish(ExecutionStatusSuccess, &code, false)
	completed := <-sub.Events
	if completed.Type != ExecutionEventCompleted || completed.Status != ExecutionStatusSuccess || completed.Sequence != 2 {
		t.Fatalf("completed = %#v", completed)
	}
	final, err := hub.Get("ws_test", run.ID())
	if err != nil {
		t.Fatal(err)
	}
	if final.Stdout != "hello\n" || final.Execution.Status != ExecutionStatusSuccess || final.Execution.ExitCode == nil || *final.Execution.ExitCode != 0 {
		t.Fatalf("final = %#v", final)
	}
	if _, err := hub.Get("ws_other", run.ID()); !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("cross-workspace get err = %v", err)
	}
}

func TestExecutionHubRedactsCredentialCommandsFromHistoryAndFeed(t *testing.T) {
	const secret = "execution-secret-marker"
	command := "cm config set tunnel.api_key " + secret
	hub := NewExecutionHub()
	run := hub.Begin(ExecutionInput{
		WorkspaceID:      "ws_test",
		Tool:             "run_command",
		Command:          command,
		RequestedCommand: command,
		EffectiveCommand: command,
		SecurityCommand:  command,
	})
	snapshot, err := hub.Get("ws_test", run.ID())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join([]string{
		snapshot.Execution.Command,
		snapshot.Execution.RequestedCommand,
		snapshot.Execution.EffectiveCommand,
		snapshot.Execution.SecurityCommand,
	}, " ")
	if strings.Contains(joined, secret) || !strings.Contains(joined, "<redacted>") {
		t.Fatalf("execution metadata leaked credential command: %#v", snapshot.Execution)
	}
	feed, feedSnapshot := hub.SubscribeFeed("ws_test")
	defer hub.UnsubscribeFeed(feed)
	if len(feedSnapshot.Events) != 1 || feedSnapshot.Events[0].Execution == nil {
		t.Fatalf("feed snapshot=%#v", feedSnapshot)
	}
	if strings.Contains(feedSnapshot.Events[0].Execution.Command, secret) {
		t.Fatalf("execution feed leaked credential command: %#v", feedSnapshot.Events[0])
	}
}

func TestExecutionHubFeedRetainsLatestEvents(t *testing.T) {
	hub := NewExecutionHub()
	for i := 0; i < MaxExecutionFeedEvents+17; i++ {
		hub.publishFeed(ExecutionFeedEvent{ExecutionID: "exec_limit", WorkspaceID: "ws_limit", Type: ExecutionEventOutput, Data: "x"})
	}
	sub, snapshot := hub.SubscribeFeed("")
	defer hub.UnsubscribeFeed(sub)
	if len(snapshot.Events) != MaxExecutionFeedEvents {
		t.Fatalf("events=%d want=%d", len(snapshot.Events), MaxExecutionFeedEvents)
	}
	if snapshot.Events[0].Sequence != 18 || snapshot.Events[len(snapshot.Events)-1].Sequence != uint64(MaxExecutionFeedEvents+17) {
		t.Fatalf("sequence range=%d..%d", snapshot.Events[0].Sequence, snapshot.Events[len(snapshot.Events)-1].Sequence)
	}
}

func TestExecutionWriterChunksLargeUTF8Output(t *testing.T) {
	hub := NewExecutionHub()
	run := hub.Begin(ExecutionInput{WorkspaceID: "ws_chunk", Tool: "run_command"})
	data := strings.Repeat("x", maxExecutionEventBytes-1) + "你" + strings.Repeat("y", maxExecutionEventBytes)
	if _, err := run.Writer("stdout").Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	sub, snapshot := hub.SubscribeFeed("ws_chunk")
	defer hub.UnsubscribeFeed(sub)
	outputs := make([]ExecutionFeedEvent, 0, 3)
	for _, event := range snapshot.Events {
		if event.Type == ExecutionEventOutput {
			outputs = append(outputs, event)
		}
	}
	if len(outputs) != 3 {
		t.Fatalf("output chunks=%d", len(outputs))
	}
	var joined strings.Builder
	for _, event := range outputs {
		if len(event.Data) > maxExecutionEventBytes || !utf8.ValidString(event.Data) {
			t.Fatalf("invalid chunk bytes=%d valid=%t", len(event.Data), utf8.ValidString(event.Data))
		}
		joined.WriteString(event.Data)
	}
	if joined.String() != data {
		t.Fatal("chunked output changed content")
	}
}

func TestExecutionHubWorkspaceFeedReplaysAndFilters(t *testing.T) {
	hub := NewExecutionHub()
	first := hub.Begin(ExecutionInput{WorkspaceID: "ws_first", Tool: "run_command", Command: "first", CWD: "/tmp", Source: "mcp"})
	_, _ = first.Writer("stdout").Write([]byte("before\n"))
	second := hub.Begin(ExecutionInput{WorkspaceID: "ws_second", Tool: "run_command", Command: "second", CWD: "/tmp", Source: "mcp"})
	_, _ = second.Writer("stdout").Write([]byte("hidden\n"))

	sub, snapshot := hub.SubscribeFeed("ws_first")
	defer hub.UnsubscribeFeed(sub)
	if len(snapshot.Events) != 2 || snapshot.Events[0].Type != ExecutionEventStarted || snapshot.Events[0].ExecutionID != first.ID() || snapshot.Events[1].Data != "before\n" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, event := range snapshot.Events {
		if event.WorkspaceID != "ws_first" {
			t.Fatalf("cross-workspace feed event = %#v", event)
		}
	}

	_, _ = first.Writer("stderr").Write([]byte("live\n"))
	event := <-sub.Events
	if event.Type != ExecutionEventOutput || event.ExecutionID != first.ID() || event.Stream != "stderr" || event.Data != "live\n" || event.Execution == nil || event.Execution.Command != "first" {
		t.Fatalf("live event = %#v", event)
	}
	_, _ = second.Writer("stdout").Write([]byte("still hidden\n"))
	select {
	case event := <-sub.Events:
		t.Fatalf("received cross-workspace event = %#v", event)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestRunCommandStreamsBeforeReturningAndPreservesFinalResult(t *testing.T) {
	if os.PathSeparator != '\\' && os.Getenv("SHELL") == "" {
		t.Setenv("SHELL", "/bin/sh")
	}
	root := t.TempDir()
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	hub := NewExecutionHub()
	manager := NewManagerWithExecutions(workspaces, filepath.Join(t.TempDir(), "state"), hub)
	ctx := WithExecutionMetadata(context.Background(), ExecutionMetadata{Source: "mcp", CallID: "call_stream", SessionHash: "safe-hash", ReceivedByInstanceID: "instance-a", ExecutedByInstanceID: "instance-b"})
	resultCh := make(chan ExecResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := manager.Exec(ctx, item.ID, streamingTestCommand())
		resultCh <- result
		errCh <- err
	}()

	var info ExecutionInfo
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		values := hub.List(item.ID, 10)
		if len(values) > 0 {
			info = values[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if info.ID == "" {
		t.Fatal("execution was not registered while command was running")
	}
	if info.Source != "mcp" || info.CallID != "call_stream" || info.SessionHash != "safe-hash" || info.ReceivedByInstanceID != "instance-a" || info.ExecutedByInstanceID != "instance-b" {
		t.Fatalf("execution attribution = %#v", info)
	}
	sub, snapshot, err := hub.Subscribe(item.ID, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Unsubscribe(sub)
	streamed := snapshot.Stdout + snapshot.Stderr
	for !strings.Contains(streamed, "second") && time.Now().Before(deadline) {
		select {
		case event := <-sub.Events:
			streamed += event.Data
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !strings.Contains(streamed, "second") {
		t.Fatalf("streamed output = %q", streamed)
	}
	result := <-resultCh
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Stdout, "first") || !strings.Contains(result.Stdout, "third") || !strings.Contains(result.Stderr, "second") || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
	final, err := hub.Get(item.ID, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Execution.Status != ExecutionStatusSuccess || !strings.Contains(final.Stdout, "third") || !strings.Contains(final.Stderr, "second") {
		t.Fatalf("final = %#v", final)
	}
}

func streamingTestCommand() string {
	if os.PathSeparator == '\\' {
		return `Write-Output first; Start-Sleep -Milliseconds 150; [Console]::Error.WriteLine("second"); Start-Sleep -Milliseconds 150; Write-Output third`
	}
	return `printf 'first\n'; sleep 0.15; printf 'second\n' >&2; sleep 0.15; printf 'third\n'`
}
