package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

const completionFeedHeartbeatInterval = 15 * time.Second

func registerRuntimeCompletionRoutes(mux *http.ServeMux, token string, service *agentcompletion.Service) {
	mux.HandleFunc("/completions", authenticatedControl(token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeControlJSON(w, nil, errors.New("agent completion service is unavailable"))
			return
		}
		limit := completionQueryLimit(r, 50)
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		var (
			records []agentcompletion.Record
			err     error
		)
		if workspaceID == "" {
			records, err = service.Recent(limit)
		} else {
			records, err = service.RecentWorkspace(workspaceID, limit)
		}
		writeControlJSON(w, records, err)
	}))
	mux.HandleFunc("/completions/current", authenticatedControl(token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeControlJSON(w, nil, errors.New("agent completion service is unavailable"))
			return
		}
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			writeControlJSON(w, nil, errors.New("completion workspace is required"))
			return
		}
		record, found, err := service.CurrentWorkspace(workspaceID)
		if err == nil && !found {
			err = errors.New("no agent completion found for workspace")
		}
		writeControlJSON(w, record, err)
	}))
	mux.HandleFunc("/completions/view", authenticatedControl(token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeControlJSON(w, nil, errors.New("agent completion service is unavailable"))
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeControlJSON(w, nil, errors.New("completion id is required"))
			return
		}
		record, found, err := service.Get(id)
		if err == nil && !found {
			err = errors.New("agent completion not found")
		}
		writeControlJSON(w, record, err)
	}))
	mux.HandleFunc("/completions/stream", authenticatedControl(token, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		serveRuntimeCompletionFeed(w, r, service, completionFeedHeartbeatInterval)
	}))
}

func completionQueryLimit(r *http.Request, fallback int) int {
	if r == nil {
		return fallback
	}
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	if value > 100 {
		return 100
	}
	return value
}

func serveRuntimeCompletionFeed(w http.ResponseWriter, r *http.Request, service *agentcompletion.Service, heartbeatInterval time.Duration) {
	if service == nil {
		http.Error(w, "agent completion service is unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	limit := completionQueryLimit(r, 50)
	sub, snapshot := service.SubscribeSnapshot(0)
	if sub == nil {
		http.Error(w, "agent completion stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer service.Unsubscribe(sub)

	var (
		records []agentcompletion.Record
		err     error
	)
	if workspaceID == "" {
		records, err = service.Recent(limit)
	} else {
		records, err = service.RecentWorkspace(workspaceID, limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	records = completionRecordsThrough(records, snapshot.LatestSequence)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ready, err := json.Marshal(agentcompletion.Snapshot{LatestSequence: snapshot.LatestSequence, Records: records})
	if err != nil {
		http.Error(w, "encode completion stream snapshot", http.StatusInternalServerError)
		return
	}
	if _, err := fmt.Fprintf(w, "event: ready\ndata: %s\n\n", ready); err != nil {
		return
	}
	flusher.Flush()

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
			_, _ = fmt.Fprintf(w, "event: overflow\ndata: {\"dropped_sequence\":%d}\n\n", overflow.DroppedSequence)
			flusher.Flush()
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, "event: heartbeat\ndata: {\"latest_sequence\":%d}\n\n", service.LatestSequence()); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			if workspaceID != "" && event.Record.WorkspaceID != workspaceID {
				continue
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Name, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func completionRecordsThrough(records []agentcompletion.Record, sequence uint64) []agentcompletion.Record {
	filtered := make([]agentcompletion.Record, 0, len(records))
	for _, record := range records {
		if record.Sequence <= sequence {
			filtered = append(filtered, record)
		}
	}
	return filtered
}
