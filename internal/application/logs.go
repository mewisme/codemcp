package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtimecontrol"
	"go.mewis.me/codemcp/internal/runtimeevent"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/workspace"
)

type LogsQueryOptions struct {
	Tail       int
	All        bool
	Since      string
	Until      string
	Session    string
	Level      string
	Components string
	Workspace  string
	Tool       string
	Status     string
	Source     string
	Event      string
	Grep       string
}

type LogsSnapshot struct {
	Events         []runtimeevent.Event
	Query          runtimeevent.Query
	Session        string
	Total          int
	Truncated      bool
	LatestSequence map[string]uint64
}

type LogsInfo struct {
	Path  string
	Files int
	Bytes int64
}

func BuildLogsQuery(options LogsQueryOptions, now time.Time) (runtimeevent.Query, error) {
	return BuildLogsQueryContext(context.Background(), options, now)
}

func BuildLogsQueryContext(ctx context.Context, options LogsQueryOptions, now time.Time) (query runtimeevent.Query, err error) {
	span := tracepkg.Start(ctx, "LOGS", "logs.query.resolve", "Resolving runtime log query", tracepkg.Int("tail", options.Tail), tracepkg.Bool("all_sessions", options.All), tracepkg.Bool("session_configured", strings.TrimSpace(options.Session) != ""), tracepkg.Bool("workspace_configured", strings.TrimSpace(options.Workspace) != ""), tracepkg.Bool("grep_configured", strings.TrimSpace(options.Grep) != ""), tracepkg.Bool("event_filter_configured", strings.TrimSpace(options.Event) != ""))
	defer func() {
		if err != nil {
			span.FailMessage("Runtime log query resolution failed", err)
			return
		}
		span.EndMessage("Runtime log query resolved", tracepkg.String("session", query.RunID), tracepkg.String("workspace_id", query.WorkspaceID), tracepkg.String("minimum_level", query.MinLevel), tracepkg.Int("component_count", len(query.Components)), tracepkg.Bool("since_configured", query.Since != nil), tracepkg.Bool("until_configured", query.Until != nil))
	}()
	if options.Tail < 0 {
		err = errors.New("tail must be zero or greater")
		return runtimeevent.Query{}, err
	}
	query = runtimeevent.Query{RunID: strings.TrimSpace(options.Session), MinLevel: strings.ToLower(strings.TrimSpace(options.Level)), Components: splitLogCSV(options.Components), Tool: strings.TrimSpace(options.Tool), Status: strings.TrimSpace(options.Status), Source: strings.TrimSpace(options.Source), EventGlob: strings.TrimSpace(options.Event), Grep: strings.TrimSpace(options.Grep)}
	if query.MinLevel != "" {
		switch query.MinLevel {
		case "debug", "info", "warn", "warning", "error":
		default:
			return runtimeevent.Query{}, errors.New("level must be debug, info, warn, or error")
		}
	}
	if query.EventGlob != "" {
		if _, err := path.Match(query.EventGlob, "test"); err != nil {
			return runtimeevent.Query{}, fmt.Errorf("invalid event glob: %w", err)
		}
	}
	if strings.TrimSpace(options.Since) != "" {
		value, err := ParseLogsSince(options.Since, now)
		if err != nil {
			return runtimeevent.Query{}, err
		}
		query.Since = &value
	}
	if strings.TrimSpace(options.Until) != "" {
		value, err := ParseLogTimestamp(options.Until)
		if err != nil {
			return runtimeevent.Query{}, fmt.Errorf("invalid --until: %w", err)
		}
		query.Until = &value
	}
	if strings.TrimSpace(options.Workspace) != "" {
		workspaceID, resolveErr := ResolveLogWorkspaceContext(ctx, options.Workspace)
		if resolveErr != nil {
			err = resolveErr
			return runtimeevent.Query{}, err
		}
		query.WorkspaceID = workspaceID
	}
	return query, nil
}

func LoadLogs(options LogsQueryOptions, visibility logger.Visibility, bufferCap int, now time.Time) (LogsSnapshot, error) {
	return LoadLogsContext(context.Background(), options, visibility, bufferCap, now)
}

func LoadLogsContext(ctx context.Context, options LogsQueryOptions, visibility logger.Visibility, bufferCap int, now time.Time) (snapshot LogsSnapshot, err error) {
	span := tracepkg.Start(ctx, "LOGS", "logs.snapshot.load", "Loading runtime log snapshot", tracepkg.Int("tail", options.Tail), tracepkg.Int("buffer_cap", bufferCap), tracepkg.String("visibility", fmt.Sprint(visibility)))
	query, err := BuildLogsQueryContext(ctx, options, now)
	if err != nil {
		span.FailMessage("Runtime log snapshot query resolution failed", err)
		return LogsSnapshot{}, err
	}
	info, infoErr := LoadLogsInfoContext(ctx)
	if infoErr != nil {
		span.FailMessage("Runtime log journal inspection failed", infoErr)
		return LogsSnapshot{}, infoErr
	}
	readSpan := tracepkg.Start(ctx, "LOGS", "logs.journal.read", "Reading runtime log journal", tracepkg.String("path", info.Path), tracepkg.Int("file_count", info.Files), tracepkg.Int64("bytes", info.Bytes))
	allEvents, err := runtimeevent.Read(config.RootPath(), runtimeevent.Query{})
	if err != nil {
		readSpan.FailMessage("Runtime log journal read failed", err)
		span.FailMessage("Runtime log snapshot load failed", err)
		return LogsSnapshot{}, err
	}
	readSpan.EndMessage("Runtime log journal read", tracepkg.Int("events_scanned", len(allEvents)))
	if !options.All && query.RunID == "" {
		query.RunID = LatestRuntimeSession(allEvents)
	}
	latestSequence := map[string]uint64{}
	for _, event := range allEvents {
		if event.RunID != "" && event.Sequence > latestSequence[event.RunID] {
			latestSequence[event.RunID] = event.Sequence
		}
	}
	events := MatchLogs(allEvents, query, visibility)
	total := len(events)
	tailTruncated := false
	if options.Tail > 0 && len(events) > options.Tail {
		tailTruncated = true
		events = append([]runtimeevent.Event(nil), events[len(events)-options.Tail:]...)
	}
	truncated := false
	if bufferCap > 0 && len(events) > bufferCap {
		events = append([]runtimeevent.Event(nil), events[len(events)-bufferCap:]...)
		truncated = true
	}
	snapshot = LogsSnapshot{Events: events, Query: query, Session: query.RunID, Total: total, Truncated: truncated, LatestSequence: latestSequence}
	span.EndMessage("Runtime log snapshot loaded", tracepkg.String("journal_path", info.Path), tracepkg.Int("journal_files", info.Files), tracepkg.Int64("journal_bytes", info.Bytes), tracepkg.Int("events_scanned", len(allEvents)), tracepkg.Int("events_matched", total), tracepkg.Int("events_returned", len(events)), tracepkg.String("selected_session", query.RunID), tracepkg.Bool("tail_truncated", tailTruncated), tracepkg.Bool("buffer_truncated", truncated))
	return snapshot, nil
}

func MatchLogs(events []runtimeevent.Event, query runtimeevent.Query, visibility logger.Visibility) []runtimeevent.Event {
	result := make([]runtimeevent.Event, 0, len(events))
	for _, event := range events {
		if query.Match(event) && event.Visibility <= visibility {
			result = append(result, event)
		}
	}
	return result
}

func LatestRuntimeSession(events []runtimeevent.Event) string {
	for index := len(events) - 1; index >= 0; index-- {
		if runID := strings.TrimSpace(events[index].RunID); runID != "" {
			return runID
		}
	}
	return ""
}

func ParseLogsSince(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if duration, err := time.ParseDuration(raw); err == nil {
		if duration < 0 {
			return time.Time{}, errors.New("--since duration must be positive")
		}
		return now.Add(-duration), nil
	}
	value, err := ParseLogTimestamp(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --since: use a duration such as 30m or RFC3339 timestamp: %w", err)
	}
	return value, nil
}

func ParseLogTimestamp(raw string) (time.Time, error) {
	if value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
		return value, nil
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(raw))
}

func ResolveLogWorkspace(value string) (string, error) {
	return ResolveLogWorkspaceContext(context.Background(), value)
}

func ResolveLogWorkspaceContext(ctx context.Context, value string) (resolved string, err error) {
	value = strings.TrimSpace(value)
	inputKind := "path"
	if strings.HasPrefix(value, "ws_") {
		inputKind = "id"
	}
	span := tracepkg.Start(ctx, "LOGS", "logs.workspace.resolve", "Resolving runtime log workspace", tracepkg.String("input_kind", inputKind))
	defer func() {
		if err != nil {
			span.FailMessage("Runtime log workspace resolution failed", err, tracepkg.String("input_kind", inputKind))
		} else {
			span.EndMessage("Runtime log workspace resolved", tracepkg.String("input_kind", inputKind), tracepkg.String("workspace_id", resolved))
		}
	}()
	manager := workspace.NewManager(workspace.DefaultStorePath())
	if strings.HasPrefix(value, "ws_") {
		item, err := manager.Get(value)
		if err != nil {
			return "", err
		}
		resolved = item.ID
		return resolved, nil
	}
	if strings.HasPrefix(value, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, strings.TrimLeft(strings.TrimPrefix(value, "~"), `/\`))
		}
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if canonical, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(canonical)
	}
	items, err := manager.List()
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if filepath.Clean(item.Path) == absolute {
			resolved = item.ID
			return resolved, nil
		}
	}
	err = fmt.Errorf("workspace is not registered: %s", absolute)
	return "", err
}

func LogFields(event runtimeevent.Event, visibility logger.Visibility) []runtimeevent.Field {
	fields := make([]runtimeevent.Field, 0, len(event.Fields))
	for _, field := range event.Fields {
		if field.Visibility <= visibility {
			fields = append(fields, field)
		}
	}
	return fields
}

func LoadLogsInfo() (LogsInfo, error) {
	return LoadLogsInfoContext(context.Background())
}

func LoadLogsInfoContext(ctx context.Context) (LogsInfo, error) {
	span := tracepkg.Start(ctx, "LOGS", "logs.journal.inspect", "Inspecting runtime log journal", tracepkg.String("path", runtimeevent.Path(config.RootPath())))
	files, err := runtimeevent.FilesOldestFirst(config.RootPath())
	if err != nil {
		span.FailMessage("Runtime log journal file discovery failed", err)
		return LogsInfo{}, err
	}
	var bytes int64
	for _, file := range files {
		if info, statErr := os.Stat(file); statErr == nil {
			bytes += info.Size()
		} else if !os.IsNotExist(statErr) {
			span.FailMessage("Runtime log journal file inspection failed", statErr, tracepkg.String("path", file))
			return LogsInfo{}, statErr
		}
	}
	info := LogsInfo{Path: runtimeevent.Path(config.RootPath()), Files: len(files), Bytes: bytes}
	span.EndMessage("Runtime log journal inspected", tracepkg.String("path", info.Path), tracepkg.Int("file_count", info.Files), tracepkg.Int64("bytes", info.Bytes))
	return info, nil
}

func ClearLogs(ctx context.Context) error {
	span := tracepkg.Start(ctx, "LOGS", "logs.clear", "Clearing runtime logs")
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := runtimecontrol.Request(requestCtx, http.MethodPost, "/logs/clear", nil, &map[string]bool{})
	if err == nil {
		span.EndMessage("Runtime logs cleared", tracepkg.String("mode", "runtime"), tracepkg.Bool("fallback", false))
		return nil
	}
	if !runtimecontrol.IsUnavailable(err) {
		span.FailMessage("Runtime log clear request failed", err, tracepkg.String("mode", "runtime"), tracepkg.Bool("fallback", false))
		return err
	}
	tracepkg.Emit(ctx, "LOGS", "logs.clear.fallback", "Falling back to local runtime log clear", tracepkg.String("reason", "runtime_unavailable"), tracepkg.String("mode", "local"))
	journal, journalErr := runtimeevent.NewJournal(config.RootPath(), runtimeevent.Options{})
	if journalErr != nil {
		span.FailMessage("Local runtime log clear setup failed", journalErr, tracepkg.String("mode", "local"), tracepkg.Bool("fallback", true))
		return journalErr
	}
	if journalErr = journal.Clear(); journalErr != nil {
		span.FailMessage("Local runtime log clear failed", journalErr, tracepkg.String("mode", "local"), tracepkg.Bool("fallback", true))
		return journalErr
	}
	span.EndMessage("Runtime logs cleared", tracepkg.String("mode", "local"), tracepkg.Bool("fallback", true))
	return nil
}

func splitLogCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}
