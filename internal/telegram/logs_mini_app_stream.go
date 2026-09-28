package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/logger"
	runtimeactivity "go.mewis.me/codemcp/internal/runtime/activity"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	miniAppFeedRuntime    = "runtime"
	miniAppFeedExecutions = "executions"
	miniAppFeedTools      = "tools"
	miniAppStreamTail     = 500
	miniAppWriteTimeout   = 5 * time.Second
	miniAppHeartbeat      = 15 * time.Second
)

type miniAppStreamRegistry struct {
	mu          sync.Mutex
	connections map[*websocket.Conn]context.CancelFunc
}

func newMiniAppStreamRegistry() *miniAppStreamRegistry {
	return &miniAppStreamRegistry{connections: map[*websocket.Conn]context.CancelFunc{}}
}

func (registry *miniAppStreamRegistry) add(conn *websocket.Conn, cancel context.CancelFunc) {
	if registry == nil || conn == nil {
		return
	}
	registry.mu.Lock()
	registry.connections[conn] = cancel
	registry.mu.Unlock()
}

func (registry *miniAppStreamRegistry) remove(conn *websocket.Conn) {
	if registry == nil || conn == nil {
		return
	}
	registry.mu.Lock()
	delete(registry.connections, conn)
	registry.mu.Unlock()
}

func (registry *miniAppStreamRegistry) closeAll() {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	type activeStream struct {
		conn   *websocket.Conn
		cancel context.CancelFunc
	}
	connections := make([]activeStream, 0, len(registry.connections))
	for conn, cancel := range registry.connections {
		connections = append(connections, activeStream{conn: conn, cancel: cancel})
	}
	registry.connections = map[*websocket.Conn]context.CancelFunc{}
	registry.mu.Unlock()
	for _, stream := range connections {
		if stream.cancel != nil {
			stream.cancel()
		}
		_ = stream.conn.Close()
	}
}

func (runtime *LogsMiniAppRuntime) streamRegistry() *miniAppStreamRegistry {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	if runtime.streams == nil {
		runtime.streams = newMiniAppStreamRegistry()
	}
	registry := runtime.streams
	runtime.mu.Unlock()
	return registry
}

func (runtime *LogsMiniAppRuntime) closeStreams() {
	if runtime == nil {
		return
	}
	runtime.mu.RLock()
	registry := runtime.streams
	runtime.mu.RUnlock()
	if registry != nil {
		registry.closeAll()
	}
}

func (runtime *LogsMiniAppRuntime) handleStream(w http.ResponseWriter, r *http.Request) {
	session, ok := runtime.authorizedSession(r)
	if !ok {
		http.Error(w, "Mini App session is not authorized", http.StatusUnauthorized)
		return
	}
	feed := strings.TrimSpace(r.URL.Query().Get("feed"))
	switch feed {
	case miniAppFeedRuntime, miniAppFeedExecutions, miniAppFeedTools:
	default:
		http.Error(w, "unsupported Mini App feed", http.StatusBadRequest)
		return
	}
	websocket.Handler(func(conn *websocket.Conn) {
		registry := runtime.streamRegistry()
		if registry == nil || !runtime.streamSessionValid(session) {
			_ = conn.Close()
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		registry.add(conn, cancel)
		defer registry.remove(conn)
		switch feed {
		case miniAppFeedRuntime:
			runtime.serveRuntimeStream(ctx, conn, session)
		case miniAppFeedExecutions:
			runtime.serveExecutionStream(ctx, conn, session)
		case miniAppFeedTools:
			runtime.serveToolStream(ctx, conn, session)
		}
	}).ServeHTTP(w, r)
}

func (runtime *LogsMiniAppRuntime) streamSessionValid(session miniAppSession) bool {
	if runtime == nil {
		return false
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.config.LogsMiniApp.Enabled &&
		session.Generation == runtime.health.Generation &&
		containsUserID(runtime.config.AllowedUserIDs, session.UserID) &&
		runtime.now().UTC().Before(session.ExpiresAt)
}

func (runtime *LogsMiniAppRuntime) serveRuntimeStream(ctx context.Context, conn *websocket.Conn, session miniAppSession) {
	stream, _, err := runtimecontrol.OpenEvents(ctx)
	if err != nil {
		_ = writeMiniAppFrame(conn, "error", miniAppFeedRuntime, 0, 0, nil, "feed_unavailable")
		return
	}
	defer stream.Close()
	snapshot, err := application.LoadLogsContext(ctx, application.LogsQueryOptions{Tail: miniAppStreamTail}, logger.VisibilityDefault, miniAppStreamTail, runtime.now().UTC())
	if err != nil {
		_ = writeMiniAppFrame(conn, "error", miniAppFeedRuntime, 0, 0, nil, "snapshot_unavailable")
		return
	}
	payload, cursor := projectRuntimeSnapshot(snapshot)
	if err := writeMiniAppFrame(conn, "snapshot", miniAppFeedRuntime, 0, cursor, payload, ""); err != nil {
		return
	}
	results := make(chan runtimeEventResult, 1)
	go readRuntimeStream(ctx, stream, results)
	heartbeat := time.NewTicker(miniAppHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if !runtime.streamSessionValid(session) || writeMiniAppFrame(conn, "heartbeat", miniAppFeedRuntime, 0, max(cursor, stream.LatestSequence()), nil, "") != nil {
				return
			}
		case result, ok := <-results:
			if !ok {
				return
			}
			if result.err != nil {
				if errors.Is(result.err, runtimecontrol.ErrEventStreamGap) {
					_ = writeMiniAppFrame(conn, "resync", miniAppFeedRuntime, 0, feedOverflowLatest(result.err, max(cursor, stream.LatestSequence())), nil, "overflow")
				}
				return
			}
			if result.event.Sequence <= cursor {
				continue
			}
			cursor = result.event.Sequence
			if err := writeMiniAppFrame(conn, "event", miniAppFeedRuntime, cursor, cursor, projectRuntimeEvent(result.event), ""); err != nil {
				return
			}
		}
	}
}

type runtimeEventResult struct {
	event runtimeevent.Event
	err   error
}

func readRuntimeStream(ctx context.Context, stream *runtimecontrol.EventStream, out chan<- runtimeEventResult) {
	defer close(out)
	for {
		event, err := stream.Next()
		select {
		case out <- runtimeEventResult{event: event, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (runtime *LogsMiniAppRuntime) serveExecutionStream(ctx context.Context, conn *websocket.Conn, session miniAppSession) {
	stream, _, err := runtimecontrol.OpenExecutionFeed(ctx)
	if err != nil {
		_ = writeMiniAppFrame(conn, "error", miniAppFeedExecutions, 0, 0, nil, "feed_unavailable")
		return
	}
	defer stream.Close()
	snapshot := stream.Snapshot()
	payload := projectExecutionSnapshot(snapshot)
	cursor := snapshot.LatestSequence
	if err := writeMiniAppFrame(conn, "snapshot", miniAppFeedExecutions, 0, cursor, payload, ""); err != nil {
		return
	}
	results := make(chan executionEventResult, 1)
	go readExecutionStream(ctx, stream, results)
	heartbeat := time.NewTicker(miniAppHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if !runtime.streamSessionValid(session) || writeMiniAppFrame(conn, "heartbeat", miniAppFeedExecutions, 0, cursor, nil, "") != nil {
				return
			}
		case result, ok := <-results:
			if !ok {
				return
			}
			if result.err != nil {
				if errors.Is(result.err, runtimecontrol.ErrExecutionFeedOverflow) {
					_ = writeMiniAppFrame(conn, "resync", miniAppFeedExecutions, 0, feedOverflowLatest(result.err, cursor), nil, "overflow")
				}
				return
			}
			if result.event.Sequence <= cursor {
				continue
			}
			cursor = result.event.Sequence
			if err := writeMiniAppFrame(conn, "event", miniAppFeedExecutions, cursor, cursor, projectExecutionEvent(result.event), ""); err != nil {
				return
			}
		}
	}
}

type executionEventResult struct {
	event shellruntime.ExecutionFeedEvent
	err   error
}

func readExecutionStream(ctx context.Context, stream *runtimecontrol.ExecutionFeedStream, out chan<- executionEventResult) {
	defer close(out)
	for {
		event, err := stream.Next()
		select {
		case out <- executionEventResult{event: event, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (runtime *LogsMiniAppRuntime) serveToolStream(ctx context.Context, conn *websocket.Conn, session miniAppSession) {
	stream, _, err := runtimecontrol.OpenToolCallFeed(ctx)
	if err != nil {
		_ = writeMiniAppFrame(conn, "error", miniAppFeedTools, 0, 0, nil, "feed_unavailable")
		return
	}
	defer stream.Close()
	snapshot := stream.Snapshot()
	payload := projectToolSnapshot(snapshot)
	cursor := snapshot.LatestSequence
	if err := writeMiniAppFrame(conn, "snapshot", miniAppFeedTools, 0, cursor, payload, ""); err != nil {
		return
	}
	results := make(chan toolEventResult, 1)
	go readToolStream(ctx, stream, results)
	heartbeat := time.NewTicker(miniAppHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if !runtime.streamSessionValid(session) || writeMiniAppFrame(conn, "heartbeat", miniAppFeedTools, 0, cursor, nil, "") != nil {
				return
			}
		case result, ok := <-results:
			if !ok {
				return
			}
			if result.err != nil {
				if errors.Is(result.err, runtimecontrol.ErrToolCallFeedOverflow) {
					_ = writeMiniAppFrame(conn, "resync", miniAppFeedTools, 0, feedOverflowLatest(result.err, cursor), nil, "overflow")
				}
				return
			}
			if result.event.Sequence <= cursor {
				continue
			}
			cursor = result.event.Sequence
			if err := writeMiniAppFrame(conn, "event", miniAppFeedTools, cursor, cursor, projectToolEvent(result.event), ""); err != nil {
				return
			}
		}
	}
}

func feedOverflowLatest(err error, fallback uint64) uint64 {
	overflow, ok := runtimecontrol.FeedOverflowOf(err)
	if !ok || overflow.LatestSequence == 0 {
		return fallback
	}
	return overflow.LatestSequence
}

type toolEventResult struct {
	event runtimeactivity.Event
	err   error
}

func readToolStream(ctx context.Context, stream *runtimecontrol.ToolCallFeedStream, out chan<- toolEventResult) {
	defer close(out)
	for {
		event, err := stream.Next()
		select {
		case out <- toolEventResult{event: event, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func writeMiniAppFrame(conn *websocket.Conn, frameType, feed string, sequence, latest uint64, payload any, reason string) error {
	if conn == nil {
		return errors.New("mini app stream is unavailable")
	}
	frame := map[string]any{"type": frameType, "feed": feed}
	if sequence > 0 {
		frame["sequence"] = sequence
	}
	if latest > 0 {
		frame["latest_sequence"] = latest
	}
	if payload != nil {
		frame["payload"] = payload
	}
	if reason != "" {
		frame["reason"] = reason
	}
	_ = conn.SetWriteDeadline(time.Now().Add(miniAppWriteTimeout))
	return websocket.JSON.Send(conn, frame)
}

func projectRuntimeSnapshot(snapshot application.LogsSnapshot) (map[string]any, uint64) {
	events := make([]map[string]any, 0, len(snapshot.Events))
	latest := uint64(0)
	for _, event := range snapshot.Events {
		events = append(events, projectRuntimeEvent(event))
		latest = max(latest, event.Sequence)
	}
	return map[string]any{
		"events": events, "session": snapshot.Session, "total": snapshot.Total,
		"truncated": snapshot.Truncated, "latest_sequence": latest,
	}, latest
}

func projectRuntimeEvent(event runtimeevent.Event) map[string]any {
	fields := make([]map[string]any, 0, len(event.Fields))
	for _, field := range event.Fields {
		fields = append(fields, map[string]any{
			"key":        field.Key,
			"value":      tracepkg.SanitizeValue(field.Key, field.Value),
			"visibility": field.Visibility,
		})
	}
	return map[string]any{
		"sequence": event.Sequence, "timestamp": event.Time, "run_id": event.RunID, "pid": event.PID,
		"level": event.Level, "kind": event.Kind, "event": event.Name, "component": event.Component,
		"message": tracepkg.SanitizeText(event.Message), "error": tracepkg.SanitizeText(event.Error),
		"workspace_id": event.WorkspaceID, "tool": event.Tool, "method": event.Method, "source": event.Source,
		"status": event.Status, "duration_ms": event.DurationMS, "managed": event.Managed,
		"service_id": event.ServiceID, "service_scope": event.ServiceScope, "fields": fields,
	}
}

func projectExecutionSnapshot(snapshot shellruntime.ExecutionFeedSnapshot) map[string]any {
	events := make([]map[string]any, 0, len(snapshot.Events))
	for _, event := range snapshot.Events {
		events = append(events, projectExecutionEvent(event))
	}
	executions := make([]map[string]any, 0, len(snapshot.Executions))
	for _, execution := range snapshot.Executions {
		executions = append(executions, projectExecutionInfo(execution))
	}
	return map[string]any{"events": events, "executions": executions, "latest_sequence": snapshot.LatestSequence}
}

func projectExecutionEvent(event shellruntime.ExecutionFeedEvent) map[string]any {
	event = shellruntime.PublicExecutionFeedEvent(event)
	result := map[string]any{
		"sequence": event.Sequence, "type": event.Type, "execution_id": event.ExecutionID,
		"workspace_id": event.WorkspaceID, "stream": event.Stream, "data": tracepkg.SanitizeText(event.Data),
		"status": event.Status, "exit_code": event.ExitCode, "timed_out": event.TimedOut, "timestamp": event.Timestamp,
	}
	if event.Execution != nil {
		result["execution"] = projectExecutionInfo(*event.Execution)
	}
	return result
}

func projectExecutionInfo(execution shellruntime.ExecutionInfo) map[string]any {
	execution = shellruntime.PublicExecutionInfo(execution)
	return map[string]any{
		"id": execution.ID, "workspace_id": execution.WorkspaceID, "tool": execution.Tool,
		"command":           execution.Command,
		"requested_command": execution.RequestedCommand,
		"effective_command": execution.EffectiveCommand,
		"cwd":               execution.CWD,
		"shell":             execution.Shell, "source": execution.Source,
		"started_at": execution.StartedAt, "finished_at": execution.FinishedAt, "status": execution.Status,
		"exit_code": execution.ExitCode, "timed_out": execution.TimedOut,
	}
}

func projectToolSnapshot(snapshot runtimecontrol.ToolCallFeedSnapshot) map[string]any {
	events := make([]map[string]any, 0, len(snapshot.Events))
	for _, event := range snapshot.Events {
		events = append(events, projectToolEvent(event))
	}
	records := make([]map[string]any, 0, len(snapshot.Records))
	for _, record := range snapshot.Records {
		records = append(records, map[string]any{
			"call_id": record.CallID, "first": projectToolEvent(record.First), "latest": projectToolEvent(record.Latest),
		})
	}
	return map[string]any{"events": events, "records": records, "latest_sequence": snapshot.LatestSequence}
}

func projectToolEvent(event runtimeactivity.Event) map[string]any {
	event = runtimeactivity.PublicEvent(event)
	return map[string]any{
		"sequence": event.Sequence, "call_id": event.CallID, "kind": event.Kind, "phase": event.Phase,
		"method": event.Method, "source": event.Source, "tool": event.Tool, "workspace_id": event.WorkspaceID,
		"status": event.Status, "duration_ms": event.DurationMS, "message": event.Message,
		"timestamp": event.Timestamp,
	}
}
