package activity

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultHeartbeatInterval = 15 * time.Second

func Handler(stream *Stream) http.Handler {
	return handlerWithHeartbeat(stream, defaultHeartbeatInterval)
}

func CallHandler(stream *Stream) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		callID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/activity/"), "/")
		if callID == "" || callID == "stream" {
			http.NotFound(w, r)
			return
		}
		event, ok := stream.FindCall(callID)
		if !ok {
			http.Error(w, "tool call not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(event)
	})
}

func handlerWithHeartbeat(stream *Stream, heartbeatInterval time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		sub, recent := stream.SubscribeDetailed(historyLimit(r))
		defer stream.UnsubscribeDetailed(sub)
		lastSent := uint64(0)
		for _, event := range recent {
			if err := writeActivitySSE(w, event); err != nil {
				return
			}
			lastSent = event.Sequence
		}
		if _, err := fmt.Fprintf(w, "event: ready\ndata: {\"latest_sequence\":%d}\n\n", stream.LatestSequence()); err != nil {
			return
		}
		flusher.Flush()
		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case overflow := <-sub.Overflow:
				if overflow.DroppedSequence != 0 {
					_, _ = fmt.Fprintf(w, "event: overflow\ndata: {\"last_sequence\":%d,\"dropped_sequence\":%d}\n\n", lastSent, overflow.DroppedSequence)
					flusher.Flush()
					return
				}
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case overflow := <-sub.Overflow:
				if overflow.DroppedSequence == 0 {
					return
				}
				_, _ = fmt.Fprintf(w, "event: overflow\ndata: {\"last_sequence\":%d,\"dropped_sequence\":%d}\n\n", lastSent, overflow.DroppedSequence)
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
				if err := writeActivitySSE(w, event); err != nil {
					return
				}
				lastSent = event.Sequence
				flusher.Flush()
			}
		}
	})
}

func writeActivitySSE(w http.ResponseWriter, event Event) error {
	_, err := fmt.Fprintf(w, "id: %d\nevent: activity\ndata: %s\n\n", event.Sequence, Encode(event))
	return err
}

func historyLimit(r *http.Request) int {
	const fallback = 100
	raw := r.URL.Query().Get("history")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	if value > defaultRecentLimit {
		return defaultRecentLimit
	}
	return value
}
