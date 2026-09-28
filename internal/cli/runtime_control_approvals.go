package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/approval"
)

func serveRuntimeApprovalFeed(w http.ResponseWriter, r *http.Request, manager *approval.Manager) {
	if manager == nil {
		http.Error(w, "control approval manager unavailable", http.StatusServiceUnavailable)
		return
	}
	stream := manager.Events()
	if stream == nil {
		http.Error(w, "control approval event stream unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub, snapshot := stream.SubscribeSnapshot(0)
	defer sub.Close()
	requests, err := approval.NewReviewService(manager).List(approval.Filter{Status: approval.StatusPending})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ready, err := json.Marshal(map[string]any{
		"requests":        requests,
		"latest_sequence": snapshot.LatestSequence,
	})
	if err != nil {
		http.Error(w, "encode approval stream snapshot", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
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
		case overflow, ok := <-sub.Overflow:
			if !ok || overflow.DroppedSequence == 0 {
				return
			}
			_, _ = fmt.Fprintf(w, "event: overflow\ndata: {\"dropped_sequence\":%d,\"latest_sequence\":%d}\n\n", overflow.DroppedSequence, overflow.LatestSequence)
			flusher.Flush()
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, "event: heartbeat\ndata: {\"latest_sequence\":%d}\n\n", stream.LatestSequence()); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			eventName := strings.TrimSpace(event.Name)
			if eventName == "" {
				eventName = "approval"
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, eventName, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
