package event

import (
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/logger"
)

func TestReadFiltersStructuredEventsAndAppliesTailAfterFiltering(t *testing.T) {
	root := t.TempDir()
	journal, err := NewJournal(root, Options{MaxBytes: 1 << 20, MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	events := []logger.Event{
		{Time: base, Level: logger.Info, Kind: logger.KindSuccess, Name: "tool.call.completed", Component: "TOOL", Message: "first", Fields: []logger.Field{logger.With("workspace", "ws_a"), logger.With("tool", "read_files"), logger.With("source", "mcp"), logger.WithDebug("status", "ok")}},
		{Time: base.Add(time.Minute), Level: logger.Warn, Kind: logger.KindWarning, Name: "tool.call.failed", Component: "TOOL", Message: "timeout one", Fields: []logger.Field{logger.With("workspace", "ws_b"), logger.With("tool", "run_command"), logger.With("source", "tunnel"), logger.WithDebug("status", "error")}},
		{Time: base.Add(2 * time.Minute), Level: logger.Error, Kind: logger.KindError, Name: "tool.call.failed", Component: "TOOL", Message: "timeout two", Fields: []logger.Field{logger.With("workspace", "ws_b"), logger.With("tool", "run_command"), logger.With("source", "tunnel"), logger.WithDebug("status", "error")}},
	}
	for _, event := range events {
		if err := journal.WriteEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	since := base.Add(30 * time.Second)
	got, err := Read(root, Query{Tail: 1, Since: &since, MinLevel: "warn", Components: []string{"tool"}, WorkspaceID: "ws_b", Tool: "run_command", Status: "error", Source: "tunnel", EventGlob: "tool.call.*", Grep: "timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "timeout two" {
		t.Fatalf("events = %#v", got)
	}
}

func TestFilesOldestFirstDiscoversRotations(t *testing.T) {
	root := t.TempDir()
	journal, err := NewJournal(root, Options{MaxBytes: 500, MaxFiles: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := journal.WriteEvent(logger.Event{Time: time.Now(), Level: logger.Info, Kind: logger.KindInfo, Name: "test.event", Component: "TEST", Message: "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"}); err != nil {
			t.Fatal(err)
		}
	}
	files, err := FilesOldestFirst(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 || files[len(files)-1] != Path(root) {
		t.Fatalf("files = %#v", files)
	}
	events, err := Read(root, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("rotated journal returned no events")
	}
}

func TestQueryFiltersSessionByDisplayedPrefix(t *testing.T) {
	query := Query{RunID: "run_abcdef123456"}
	if !query.Match(Event{RunID: "run_abcdef1234567890", Time: time.Now(), Level: "info"}) {
		t.Fatal("session prefix did not match run id")
	}
	if query.Match(Event{RunID: "run_other", Time: time.Now(), Level: "info"}) {
		t.Fatal("different session matched run id filter")
	}
}
