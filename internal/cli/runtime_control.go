package cli

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/controlguard"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtime/activity"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type runtimeControlState = runtimecontrol.State

type runtimeReloadResult = runtimecontrol.ReloadResult
type runtimeStatusResult = runtimecontrol.RuntimeStatus
type workspaceReloadResult = runtimecontrol.WorkspaceReloadResult
type upstreamReloadResult = runtimecontrol.UpstreamReloadResult

type runtimeControlOptions struct {
	RunID            string
	Managed          bool
	ServiceID        string
	ServiceScope     string
	StartedAt        time.Time
	Events           *runtimeevent.Stream
	Activity         *activity.Stream
	Reload           func(context.Context) (runtimeReloadResult, error)
	ReloadWorkspaces func() (workspaceReloadResult, error)
	ReloadUpstreams  func(context.Context) (upstreamReloadResult, error)
	Status           func() runtimeStatusResult
	StatusWait       func(context.Context, string) runtimeStatusResult
	Shutdown         func()
	Restart          func()
	ClearLogs        func() error
	Approvals        *approval.Manager
	Completions      *agentcompletion.Service
	Executions       *shellruntime.ExecutionHub
	Log              *logger.Logger
}

type runtimeControl struct {
	state    runtimeControlState
	listener net.Listener
	server   *http.Server
	path     string
	trace    tracepkg.Observer
}

func runtimeControlPath() string { return runtimecontrol.Path() }

func reloadResult(cfg config.Config, networkRestarted bool) runtimeReloadResult {
	return runtimeReloadResult{PID: os.Getpid(), NetworkRestarted: networkRestarted, ServerEnabled: cfg.Server.Enabled, ServerPort: cfg.Server.Port, AdminEnabled: cfg.Admin.Enabled, AdminPort: cfg.Admin.Port, Exposure: cfg.Server.Expose.Mode}
}

func startRuntimeControl(options runtimeControlOptions) (*runtimeControl, error) {
	return startRuntimeControlContext(context.Background(), options)
}

func startRuntimeControlContext(ctx context.Context, options runtimeControlOptions) (*runtimeControl, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path := runtimeControlPath()
	span := tracepkg.Start(ctx, "CONTROL", "runtime.control.start", "Starting runtime control endpoint", tracepkg.String("state_file", path))
	if options.Reload == nil || options.Status == nil || options.Shutdown == nil || options.ClearLogs == nil {
		err := errors.New("runtime control handlers are incomplete")
		span.FailMessage("Runtime control endpoint configuration invalid", err)
		return nil, err
	}
	bindSpan := tracepkg.Start(ctx, "CONTROL", "runtime.control.bind", "Binding runtime control endpoint", tracepkg.String("host", "127.0.0.1"), tracepkg.Int("port", 0))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		bindSpan.FailMessage("Runtime control bind failed", err)
		span.FailMessage("Runtime control endpoint start failed", err)
		return nil, err
	}
	bindSpan.EndMessage("Runtime control endpoint bound", tracepkg.String("address", listener.Addr().String()))
	startedAt := options.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	controlState := runtimeControlState{PID: os.Getpid(), Address: listener.Addr().String(), Token: auth.GenerateToken("runtime"), RunID: options.RunID, Managed: options.Managed, ServiceID: options.ServiceID, ServiceScope: options.ServiceScope, StartedAt: startedAt, ConfigRoot: config.RootPath()}
	data, err := json.Marshal(controlState)
	if err != nil {
		_ = listener.Close()
		span.FailMessage("Runtime control state encoding failed", err)
		return nil, err
	}
	stateData := append(data, '\n')
	writeSpan := tracepkg.Start(ctx, "CONTROL", "runtime.control.state.write", "Writing runtime control state", tracepkg.String("state_file", path), tracepkg.Int64("bytes", int64(len(stateData))), tracepkg.Bool("atomic", true))
	if err := state.WriteFileAtomic(path, stateData, 0600); err != nil {
		_ = listener.Close()
		writeSpan.FailMessage("Runtime control state write failed", err)
		span.FailMessage("Runtime control endpoint start failed", err)
		return nil, err
	}
	writeSpan.EndMessage("Runtime control state written", tracepkg.String("state_file", path), tracepkg.Int64("bytes", int64(len(stateData))), tracepkg.Bool("atomic", true))
	mux := http.NewServeMux()
	mux.HandleFunc("/reload", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		result, err := options.Reload(r.Context())
		writeControlJSON(w, result, err)
	}))
	mux.HandleFunc("/workspaces/reload", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, _ *http.Request) {
		if options.ReloadWorkspaces == nil {
			writeControlJSON(w, nil, errors.New("workspace reload handler is unavailable"))
			return
		}
		result, err := options.ReloadWorkspaces()
		writeControlJSON(w, result, err)
	}))
	mux.HandleFunc("/upstreams/reload", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		if options.ReloadUpstreams == nil {
			writeControlJSON(w, nil, errors.New("upstream reload handler is unavailable"))
			return
		}
		result, err := options.ReloadUpstreams(r.Context())
		writeControlJSON(w, result, err)
	}))
	mux.HandleFunc("/status", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		writeControlJSON(w, options.Status(), nil)
	}))
	mux.HandleFunc("/status/wait", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if options.StatusWait == nil {
			writeControlJSON(w, options.Status(), nil)
			return
		}
		writeControlJSON(w, options.StatusWait(r.Context(), r.URL.Query().Get("lifecycle")), nil)
	}))
	mux.HandleFunc("/shutdown", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, _ *http.Request) {
		writeControlJSON(w, map[string]bool{"ok": true}, nil)
		options.Shutdown()
	}))
	mux.HandleFunc("/restart", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, _ *http.Request) {
		if options.Restart == nil {
			writeControlJSON(w, nil, errors.New("runtime restart handler is unavailable"))
			return
		}
		writeControlJSON(w, map[string]bool{"ok": true}, nil)
		options.Restart()
	}))
	mux.HandleFunc("/logs/clear", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, _ *http.Request) {
		writeControlJSON(w, map[string]bool{"ok": true}, options.ClearLogs())
	}))
	mux.HandleFunc("/requests", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		if options.Approvals == nil {
			writeControlJSON(w, nil, errors.New("control approval manager is unavailable"))
			return
		}
		requests, err := approval.NewReviewService(options.Approvals).List(approval.Filter{})
		writeControlJSON(w, requests, err)
	}))
	registerRuntimeCompletionRoutes(mux, controlState.Token, options.Completions)
	mux.HandleFunc("/requests/stream", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		serveRuntimeApprovalFeed(w, r, options.Approvals)
	}))
	mux.HandleFunc("/requests/view", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if options.Approvals == nil {
			writeControlJSON(w, nil, errors.New("control approval manager is unavailable"))
			return
		}
		request, err := approval.NewReviewService(options.Approvals).View(r.URL.Query().Get("id"))
		writeControlJSON(w, request, err)
	}))
	mux.HandleFunc("/requests/create-dummy", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		if options.Approvals == nil {
			writeControlJSON(w, nil, errors.New("control approval manager is unavailable"))
			return
		}
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			Title       string `json:"title"`
			Command     string `json:"command"`
		}
		if err := decodeControlJSON(r, &input); err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		workspaceID := strings.TrimSpace(input.WorkspaceID)
		if workspaceID == "" {
			workspaceID = "ws_dummy"
		}
		title := strings.TrimSpace(input.Title)
		if title == "" {
			title = "Allow dummy command"
		}
		command := strings.TrimSpace(input.Command)
		if command == "" {
			command = "echo dummy approval"
		}
		sessionID := idgen.Must("dummy", 8)
		challenge, _, err := options.Approvals.CreateChallenge(approval.ChallengeInput{
			CallerID: sessionID, SessionHash: "dummy", WorkspaceID: workspaceID, Source: "cli-dummy", TargetTool: "run_command",
			Arguments: map[string]any{"workspace_id": workspaceID, "command": command, "dummy": true}, GuardCode: controlguard.CodeControlPlaneMutation,
			GuardReason: "dummy approval request created for UI testing", Title: title, Command: command,
		})
		if err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		request, _, err := options.Approvals.CreateRequest(challenge.ID, sessionID, workspaceID)
		writeControlJSON(w, request, err)
	}))
	resolveRequest := func(status approval.Status) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if options.Approvals == nil {
				writeControlJSON(w, nil, errors.New("control approval manager is unavailable"))
				return
			}
			var input struct {
				ID           string `json:"id"`
				Reason       string `json:"reason,omitempty"`
				AllowSimilar bool   `json:"allow_similar,omitempty"`
			}
			if err := decodeControlJSON(r, &input); err != nil {
				writeControlJSON(w, nil, err)
				return
			}
			decision := approval.ReviewDeny
			if status == approval.StatusApproved {
				decision = approval.ReviewApprove
			}
			request, err := approval.NewReviewService(options.Approvals).Resolve(approval.ReviewInput{
				Request: input.ID, Decision: decision, ResolvedBy: "cli", Reason: input.Reason, AllowSimilar: input.AllowSimilar,
			})
			writeControlJSON(w, request, err)
		}
	}
	mux.HandleFunc("/requests/approve", authenticatedControl(controlState.Token, http.MethodPost, resolveRequest(approval.StatusApproved)))
	mux.HandleFunc("/requests/deny", authenticatedControl(controlState.Token, http.MethodPost, resolveRequest(approval.StatusDenied)))
	mux.HandleFunc("/requests/revoke-grant", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		if options.Approvals == nil {
			writeControlJSON(w, nil, errors.New("control approval manager is unavailable"))
			return
		}
		var input struct {
			ID          string `json:"id"`
			WorkspaceID string `json:"workspace_id,omitempty"`
		}
		if err := decodeControlJSON(r, &input); err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		if workspaceID := strings.TrimSpace(input.WorkspaceID); workspaceID != "" && strings.TrimSpace(input.ID) == "" {
			revoked, err := approval.NewReviewService(options.Approvals).RevokeGrants(workspaceID)
			writeControlJSON(w, map[string]any{"revoked": revoked}, err)
			return
		}
		request, err := approval.NewReviewService(options.Approvals).RevokeGrant(input.ID)
		writeControlJSON(w, request, err)
	}))
	mux.HandleFunc("/requests/grants", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if options.Approvals == nil {
			writeControlJSON(w, nil, errors.New("control approval manager is unavailable"))
			return
		}
		grants, err := approval.NewReviewService(options.Approvals).ListGrants(r.URL.Query().Get("workspace_id"))
		writeControlJSON(w, grants, err)
	}))
	mux.HandleFunc("/requests/consume-cli", authenticatedControl(controlState.Token, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		if options.Approvals == nil {
			writeControlJSON(w, nil, errors.New("control approval manager is unavailable"))
			return
		}
		var input struct {
			Capability string   `json:"capability"`
			Args       []string `json:"args"`
		}
		if err := decodeControlJSON(r, &input); err != nil {
			writeControlJSON(w, nil, err)
			return
		}
		requestID, err := options.Approvals.ConsumeCLI(input.Capability, input.Args)
		writeControlJSON(w, map[string]string{"request_id": requestID}, err)
	}))
	mux.HandleFunc("/events", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		serveRuntimeEvents(w, r, options.Events)
	}))
	mux.HandleFunc("/tool-calls/stream", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		serveRuntimeToolCallFeed(w, r, options.Activity)
	}))
	mux.HandleFunc("/executions", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		if options.Executions == nil {
			http.Error(w, "execution stream unavailable", http.StatusServiceUnavailable)
			return
		}
		writeControlJSON(w, options.Executions.List("", shellruntime.MaxRecentExecutions), nil)
	}))
	mux.HandleFunc("/executions/", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if options.Executions == nil {
			http.Error(w, "execution stream unavailable", http.StatusServiceUnavailable)
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/executions/"), "/")
		if id == "" || id == "stream" {
			http.NotFound(w, r)
			return
		}
		snapshot, err := options.Executions.Get("", id)
		writeControlJSON(w, snapshot, err)
	}))
	mux.HandleFunc("/executions/stream", authenticatedControl(controlState.Token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		serveRuntimeExecutionFeed(w, r, options.Executions)
	}))
	server := newHTTPServer(mux)
	control := &runtimeControl{state: controlState, listener: listener, server: server, path: path, trace: tracepkg.ObserverFromContext(ctx)}
	go serveRuntimeControl(server, listener, options.Log)
	span.EndMessage("Runtime control endpoint started", tracepkg.String("address", controlState.Address), tracepkg.String("state_file", path), tracepkg.Int64("state_bytes", int64(len(stateData))))
	return control, nil
}

func serveRuntimeControl(server *http.Server, listener net.Listener, log *logger.Logger) {
	if server == nil || listener == nil {
		return
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && log != nil {
		log.Failure("CONTROL", "runtime.control.failed", "Runtime control server stopped unexpectedly", err,
			logger.WithVerbose("address", listener.Addr().String()),
		)
	}
}

func serveRuntimeExecutionFeed(w http.ResponseWriter, r *http.Request, hub *shellruntime.ExecutionHub) {
	if hub == nil {
		http.Error(w, "execution stream unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub, snapshot := hub.SubscribeFeed("")
	if sub == nil {
		http.Error(w, "execution stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ready, err := json.Marshal(struct {
		LatestSequence uint64                       `json:"latest_sequence"`
		ReplayCount    int                          `json:"replay_count"`
		Executions     []shellruntime.ExecutionInfo `json:"executions"`
	}{LatestSequence: snapshot.LatestSequence, ReplayCount: len(snapshot.Events), Executions: snapshot.Executions})
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w, "event: ready\ndata: %s\n\n", ready); err != nil {
		return
	}
	for _, event := range snapshot.Events {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data); err != nil {
			return
		}
	}
	flusher.Flush()
	latestSequence := snapshot.LatestSequence
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case overflow := <-sub.Overflow:
			if overflow.DroppedSequence == 0 {
				return
			}
			_, _ = fmt.Fprintf(w, "event: overflow\ndata: {\"dropped_sequence\":%d,\"latest_sequence\":%d}\n\n", overflow.DroppedSequence, overflow.LatestSequence)
			flusher.Flush()
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, "event: heartbeat\ndata: {\"latest_sequence\":%d}\n\n", latestSequence); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			latestSequence = event.Sequence
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func serveRuntimeToolCallFeed(w http.ResponseWriter, r *http.Request, stream *activity.Stream) {
	if stream == nil {
		http.Error(w, "tool call stream unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub, recent, latestSequence := stream.SubscribeToolCallsSnapshot(activity.MaxRecentEvents)
	defer sub.Close()
	records := stream.RecentToolCalls(activity.MaxRecentToolCalls)
	replay := make([]activity.Event, 0, len(recent))
	for _, event := range recent {
		if event.Kind == string(activity.EventToolCall) {
			replay = append(replay, event)
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ready, err := json.Marshal(struct {
		LatestSequence uint64                    `json:"latest_sequence"`
		ReplayCount    int                       `json:"replay_count"`
		Records        []activity.ToolCallRecord `json:"records"`
	}{LatestSequence: latestSequence, ReplayCount: len(replay), Records: records})
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w, "event: ready\ndata: %s\n\n", ready); err != nil {
		return
	}
	for _, event := range replay {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: tool_call\ndata: %s\n\n", event.Sequence, data); err != nil {
			return
		}
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case overflow := <-sub.Overflow:
			if overflow.DroppedSequence == 0 {
				return
			}
			_, _ = fmt.Fprintf(w, "event: overflow\ndata: {\"dropped_sequence\":%d,\"latest_sequence\":%d}\n\n", overflow.DroppedSequence, overflow.LatestSequence)
			flusher.Flush()
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprintf(w, "event: heartbeat\ndata: {\"latest_sequence\":%d}\n\n", stream.LatestSequence())
			flusher.Flush()
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			if event.Kind != string(activity.EventToolCall) {
				continue
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: tool_call\ndata: %s\n\n", event.Sequence, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func decodeControlJSON(r *http.Request, output any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode runtime control request: %w", err)
	}
	return nil
}

func authenticatedControl(token, method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeControlJSON(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func serveRuntimeEvents(w http.ResponseWriter, r *http.Request, stream *runtimeevent.Stream) {
	if stream == nil {
		http.Error(w, "runtime event stream unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	sub, snapshot := stream.SubscribeSnapshot(0)
	defer sub.Close()
	_, _ = fmt.Fprintf(w, "event: ready\ndata: {\"latest_sequence\":%d}\n\n", snapshot.LatestSequence)
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprintf(w, "event: heartbeat\ndata: {\"latest_sequence\":%d}\n\n", stream.LatestSequence())
			flusher.Flush()
		case overflow, ok := <-sub.Overflow:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: gap\ndata: {\"dropped_sequence\":%d,\"latest_sequence\":%d}\n\n", overflow.DroppedSequence, overflow.LatestSequence)
			flusher.Flush()
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: runtime\ndata: %s\n\n", event.Sequence, data)
			flusher.Flush()
		}
	}
}

func (c *runtimeControl) Close() error {
	if c == nil {
		return nil
	}
	span := tracepkg.StartObserver(c.trace, "CONTROL", "runtime.control.cleanup", "Cleaning up runtime control endpoint", tracepkg.String("address", c.state.Address), tracepkg.String("state_file", c.path))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownSpan := tracepkg.StartObserver(c.trace, "CONTROL", "runtime.control.shutdown", "Shutting down runtime control HTTP server", tracepkg.String("address", c.state.Address))
	err := c.server.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if err != nil {
		shutdownSpan.FailMessage("Runtime control HTTP shutdown failed", err)
	} else {
		shutdownSpan.EndMessage("Runtime control HTTP server shut down")
	}
	_ = c.listener.Close()
	removed := false
	cleanupSpan := tracepkg.StartObserver(c.trace, "CONTROL", "runtime.control.state.cleanup", "Cleaning up runtime control state file", tracepkg.String("state_file", c.path))
	if data, readErr := os.ReadFile(c.path); readErr == nil {
		var current runtimeControlState
		if json.Unmarshal(data, &current) == nil && current.Token == c.state.Token {
			if removeErr := os.Remove(c.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				cleanupSpan.FailMessage("Runtime control state file cleanup failed", removeErr)
			} else {
				removed = true
				cleanupSpan.EndMessage("Runtime control state file cleaned up", tracepkg.Bool("removed", true))
			}
		} else {
			cleanupSpan.EndMessage("Runtime control state file preserved", tracepkg.Bool("removed", false), tracepkg.String("reason", "ownership_changed"))
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		cleanupSpan.EndMessage("Runtime control state file already absent", tracepkg.Bool("removed", false))
	} else {
		cleanupSpan.FailMessage("Runtime control state file inspection failed", readErr)
	}
	if err != nil {
		span.FailMessage("Runtime control endpoint cleanup failed", err, tracepkg.Bool("state_file_removed", removed))
		return err
	}
	span.EndMessage("Runtime control endpoint cleaned up", tracepkg.Bool("state_file_removed", removed))
	return nil
}

func runtimeControlRequest(ctx context.Context, method, path string, output any) (runtimeControlState, error) {
	return runtimecontrol.Request(ctx, method, path, nil, output)
}

func runtimeControlJSONRequest(ctx context.Context, method, path string, input, output any) (runtimeControlState, error) {
	return runtimecontrol.Request(ctx, method, path, input, output)
}

func requestRuntimeCLIApproval(ctx context.Context, capability string, args []string) error {
	var result struct {
		RequestID string `json:"request_id"`
	}
	_, err := runtimeControlJSONRequest(ctx, http.MethodPost, "/requests/consume-cli", map[string]any{"capability": capability, "args": args}, &result)
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.RequestID) == "" {
		return errors.New("runtime control approval response is missing request id")
	}
	return nil
}

func requestRuntimeApprovalList(ctx context.Context) ([]approval.Request, error) {
	return application.ListApprovalRequests(ctx)
}

func requestRuntimeApprovalView(ctx context.Context, id string) (approval.Request, error) {
	return application.GetApprovalRequest(ctx, id)
}

func requestRuntimeApprovalResolve(ctx context.Context, action, id, reason string) (approval.Request, error) {
	switch action {
	case "approve":
		return application.ResolveApprovalRequest(ctx, id, true, reason)
	case "deny":
		return application.ResolveApprovalRequest(ctx, id, false, reason)
	default:
		return approval.Request{}, fmt.Errorf("unsupported approval action: %s", action)
	}
}

func requestRuntimeApprovalApprove(ctx context.Context, id, reason string) (approval.Request, error) {
	return requestRuntimeApprovalResolve(ctx, "approve", id, reason)
}

func requestRuntimeApprovalDeny(ctx context.Context, id, reason string) (approval.Request, error) {
	return requestRuntimeApprovalResolve(ctx, "deny", id, reason)
}

func requestRuntimeReload(ctx context.Context) (runtimeReloadResult, error) {
	var result runtimeReloadResult
	control, err := runtimeControlRequest(ctx, http.MethodPost, "/reload", &result)
	if err != nil {
		return runtimeReloadResult{}, err
	}
	if err := runtimecontrol.ValidatePID(ctx, control.PID, result.PID, "reload"); err != nil {
		return runtimeReloadResult{}, err
	}
	return result, nil
}

func requestRuntimeStatus(ctx context.Context) (runtimeStatusResult, error) {
	var result runtimeStatusResult
	control, err := runtimeControlRequest(ctx, http.MethodGet, "/status", &result)
	if err != nil {
		return runtimeStatusResult{}, err
	}
	if err := runtimecontrol.ValidatePID(ctx, control.PID, result.PID, "status"); err != nil {
		return runtimeStatusResult{}, err
	}
	return result, nil
}

func requestRuntimeShutdown(ctx context.Context) error {
	_, err := runtimeControlRequest(ctx, http.MethodPost, "/shutdown", &map[string]bool{})
	return err
}
