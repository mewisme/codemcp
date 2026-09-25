package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/auth"
)

const approvalHeartbeatInterval = 15 * time.Second

type approvalResolutionRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (api API) handleRequests(w http.ResponseWriter, r *http.Request) {
	if !api.authorizeApprovalRequest(w, r) {
		return
	}
	if api.Approvals == nil {
		http.Error(w, "control approval manager unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	status, err := parseApprovalStatus(r.URL.Query().Get("status"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, api.Approvals.List(approval.Filter{WorkspaceID: strings.TrimSpace(r.URL.Query().Get("workspace_id")), Status: status}))
}

func (api API) handleRequest(w http.ResponseWriter, r *http.Request) {
	if !api.authorizeApprovalRequest(w, r) {
		return
	}
	if api.Approvals == nil {
		http.Error(w, "control approval manager unavailable", http.StatusServiceUnavailable)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/requests/"), "/")
	if path == "stream" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		serveApprovalEvents(w, r, api.Approvals.Events(), approvalHeartbeatInterval)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	request, err := api.Approvals.Resolve(parts[0])
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, request)
		return
	}
	if len(parts) != 2 || (parts[1] != "approve" && parts[1] != "deny") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input approvalResolutionRequest
	if err := decodeJSONBody(w, r, &input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if parts[1] == "approve" {
		request, err = api.Approvals.Approve(request.ID, "admin", input.Reason)
	} else {
		request, err = api.Approvals.Deny(request.ID, "admin", input.Reason)
	}
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, request)
}

func (api API) authorizeApprovalRequest(w http.ResponseWriter, r *http.Request) bool {
	if approvalRequestLoopback(r) {
		return true
	}
	if api.Config == nil {
		http.Error(w, "remote approval access requires admin authentication", http.StatusForbidden)
		return false
	}
	cfg := api.Config.Snapshot()
	if !cfg.Auth.AdminEnabled {
		http.Error(w, "remote approval access requires admin authentication to be enabled", http.StatusForbidden)
		return false
	}
	if strings.TrimSpace(cfg.Auth.AdminTokenHash) == "" || !auth.ValidateRequestHash(r, cfg.Auth.AdminTokenHash) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="codemcp"`)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return false
	}
	return true
}

func approvalRequestLoopback(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func parseApprovalStatus(raw string) (approval.Status, error) {
	status := approval.Status(strings.ToLower(strings.TrimSpace(raw)))
	switch status {
	case "", approval.StatusPending, approval.StatusApproved, approval.StatusDenied, approval.StatusExpired, approval.StatusCancelled, approval.StatusConsumed:
		return status, nil
	default:
		return "", fmt.Errorf("invalid approval status: %s", raw)
	}
}

func writeApprovalError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	switch {
	case errors.Is(err, approval.ErrRequestNotFound):
		status = http.StatusNotFound
	case errors.Is(err, approval.ErrRequestAmbiguous), errors.Is(err, approval.ErrRequestResolved):
		status = http.StatusConflict
	}
	http.Error(w, err.Error(), status)
}

func serveApprovalEvents(w http.ResponseWriter, r *http.Request, stream *approval.EventStream, heartbeatInterval time.Duration) {
	if stream == nil {
		http.Error(w, "control approval event stream unavailable", http.StatusServiceUnavailable)
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
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	sub, snapshot := stream.SubscribeWorkspaceSnapshot(workspaceID, 0)
	defer stream.Unsubscribe(sub)
	if _, err := fmt.Fprintf(w, "event: ready\ndata: {\"latest_sequence\":%d}\n\n", snapshot.LatestSequence); err != nil {
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
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Name, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
