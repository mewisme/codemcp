package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

func (api API) handleCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	service := api.completionService()
	if service == nil {
		http.Error(w, "agent completion service is unavailable", http.StatusServiceUnavailable)
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	limit := queryInt(r, "limit", 50, 1, 100)
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
	writeJSON(w, records)
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

func (api API) handleCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	service := api.completionService()
	if service == nil {
		http.Error(w, "agent completion service is unavailable", http.StatusServiceUnavailable)
		return
	}
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/completions/"), "/")
	switch suffix {
	case "":
		http.NotFound(w, r)
	case "current":
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			http.Error(w, "completion workspace is required", http.StatusBadRequest)
			return
		}
		record, found, err := service.CurrentWorkspace(workspaceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if !found {
			http.Error(w, "agent completion not found", http.StatusNotFound)
			return
		}
		writeJSON(w, record)
	case "stream":
		api.serveCompletionFeed(w, r)
	default:
		if !strings.HasPrefix(suffix, "view/") {
			http.NotFound(w, r)
			return
		}
		id := strings.TrimSpace(strings.TrimPrefix(suffix, "view/"))
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		record, found, err := service.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if !found {
			http.Error(w, "agent completion not found", http.StatusNotFound)
			return
		}
		writeJSON(w, record)
	}
}

func (api API) completionService() *agentcompletion.Service {
	if api.Tools == nil {
		return nil
	}
	return api.Tools.Completions
}

func (api API) serveCompletionFeed(w http.ResponseWriter, r *http.Request) {
	service := api.completionService()
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
	limit := queryInt(r, "limit", 50, 1, 100)
	sub, snapshot := service.SubscribeSnapshot(0)
	if sub == nil {
		http.Error(w, "agent completion stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer sub.Close()

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
			_, _ = fmt.Fprintf(w, "event: heartbeat\ndata: {\"latest_sequence\":%d}\n\n", service.LatestSequence())
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
