package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

const executionHeartbeatInterval = 15 * time.Second

func (api API) handleWorkspaceExecutions(w http.ResponseWriter, r *http.Request, workspaceID string, parts []string) {
	hub := api.executionHub()
	if hub == nil {
		http.Error(w, "execution stream unavailable", http.StatusServiceUnavailable)
		return
	}
	if len(parts) == 0 {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, shellruntime.PublicExecutionInfos(hub.List(workspaceID, queryInt(r, "limit", 50, 1, 100))))
		return
	}
	if len(parts) == 1 && parts[0] == "stream" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		serveWorkspaceExecutionFeed(w, r, hub, workspaceID, executionHeartbeatInterval)
		return
	}
	id := strings.TrimSpace(parts[0])
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "stream" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		serveExecutionEvents(w, r, hub, workspaceID, id, executionHeartbeatInterval)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := hub.Get(workspaceID, id)
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, shellruntime.PublicExecutionSnapshot(snapshot))
}

func serveWorkspaceExecutionFeed(w http.ResponseWriter, r *http.Request, hub *shellruntime.ExecutionHub, workspaceID string, heartbeatInterval time.Duration) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub, snapshot := hub.SubscribeFeed(workspaceID)
	if sub == nil {
		http.Error(w, "execution stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := fmt.Fprintf(w, "event: ready\ndata: {\"latest_sequence\":%d,\"replay_count\":%d}\n\n", snapshot.LatestSequence, len(snapshot.Events)); err != nil {
		return
	}
	for _, event := range snapshot.Events {
		event = shellruntime.PublicExecutionFeedEvent(event)
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
	heartbeat := time.NewTicker(heartbeatInterval)
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
			event = shellruntime.PublicExecutionFeedEvent(event)
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

func (api API) executionHub() *shellruntime.ExecutionHub {
	if api.Executions != nil {
		return api.Executions
	}
	if api.Tools != nil {
		return api.Tools.Executions
	}
	return nil
}

func serveExecutionEvents(w http.ResponseWriter, r *http.Request, hub *shellruntime.ExecutionHub, workspaceID, id string, heartbeatInterval time.Duration) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub, snapshot, err := hub.Subscribe(workspaceID, id)
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	data, err := json.Marshal(shellruntime.PublicExecutionSnapshot(snapshot))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := fmt.Fprintf(w, "event: ready\ndata: %s\n\n", data); err != nil {
		return
	}
	flusher.Flush()
	if snapshot.Execution.Status != shellruntime.ExecutionStatusRunning {
		return
	}
	latestSequence := snapshot.LatestSequence
	heartbeat := time.NewTicker(heartbeatInterval)
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
			event = shellruntime.PublicExecutionEvent(event)
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data); err != nil {
				return
			}
			flusher.Flush()
			if event.Type == shellruntime.ExecutionEventCompleted {
				return
			}
		}
	}
}

func writeExecutionError(w http.ResponseWriter, err error) {
	if errors.Is(err, shellruntime.ErrExecutionNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
