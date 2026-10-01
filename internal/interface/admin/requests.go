package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/capability"
)

const approvalHeartbeatInterval = 15 * time.Second

type approvalResolutionRequest struct {
	Reason       string `json:"reason,omitempty"`
	AllowSimilar bool   `json:"allow_similar,omitempty"`
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
	requests, err := approval.NewReviewService(api.Approvals).List(approval.Filter{WorkspaceID: strings.TrimSpace(r.URL.Query().Get("workspace_id")), Status: status})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, requests)
}

func (api API) handleRequest(w http.ResponseWriter, r *http.Request) {
	if !api.authorizeApprovalRequest(w, r) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/requests/"), "/")
	if path == "explain/status" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		api.dispatch(w, r, capability.RequestExplainStatus, nil)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "explanation" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		api.dispatch(w, r, capability.RequestExplanationView, application.ApprovalExplanationReadInput{ID: parts[0]})
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "explain" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var input struct {
			Retry bool `json:"retry,omitempty"`
		}
		if err := decodeJSONBody(w, r, &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		api.dispatch(w, r, capability.RequestExplain, application.ApprovalExplainInput{ID: parts[0], Retry: input.Retry})
		return
	}
	if api.Approvals == nil {
		http.Error(w, "control approval manager unavailable", http.StatusServiceUnavailable)
		return
	}
	if path == "stream" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		serveApprovalEvents(w, r, api.Approvals, approvalHeartbeatInterval)
		return
	}
	if path == "grants" || strings.HasPrefix(path, "grants/") {
		handleApprovalGrants(w, r, approval.NewReviewService(api.Approvals), path)
		return
	}
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	reviews := approval.NewReviewService(api.Approvals)
	request, err := reviews.View(parts[0])
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
	decision := approval.ReviewDeny
	if parts[1] == "approve" {
		decision = approval.ReviewApprove
	}
	request, err = reviews.Resolve(approval.ReviewInput{Request: request.ID, Decision: decision, ResolvedBy: "admin", Reason: input.Reason, AllowSimilar: input.AllowSimilar})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, request)
}

func handleApprovalGrants(w http.ResponseWriter, r *http.Request, reviews *approval.ReviewService, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && parts[0] == "grants" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		grants, err := reviews.ListGrants(strings.TrimSpace(r.URL.Query().Get("workspace_id")))
		if err != nil {
			writeApprovalError(w, err)
			return
		}
		writeJSON(w, grants)
		return
	}
	if len(parts) == 3 && parts[0] == "grants" && parts[2] == "revoke" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		request, err := reviews.RevokeGrant(parts[1])
		if err != nil {
			writeApprovalError(w, err)
			return
		}
		writeJSON(w, request)
		return
	}
	http.NotFound(w, r)
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
	if !cfg.HTTP.Admin.Auth.Enabled {
		http.Error(w, "remote approval access requires admin authentication to be enabled", http.StatusForbidden)
		return false
	}
	if strings.TrimSpace(cfg.HTTP.Admin.Auth.TokenHash) == "" || !auth.ValidateRequestHash(r, cfg.HTTP.Admin.Auth.TokenHash) {
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

func serveApprovalEvents(w http.ResponseWriter, r *http.Request, manager *approval.Manager, heartbeatInterval time.Duration) {
	if manager == nil || manager.Events() == nil {
		http.Error(w, "control approval event stream unavailable", http.StatusServiceUnavailable)
		return
	}
	stream := manager.Events()
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
	defer sub.Close()
	requests, err := approval.NewReviewService(manager).List(approval.Filter{WorkspaceID: workspaceID, Status: approval.StatusPending})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ready, err := json.Marshal(map[string]any{"requests": requests, "latest_sequence": snapshot.LatestSequence})
	if err != nil {
		http.Error(w, "encode approval stream snapshot", http.StatusInternalServerError)
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
			event = approval.PublicEvent(event)
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
