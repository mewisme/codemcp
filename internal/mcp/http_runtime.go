package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/runtime/activity"
	"go.mewis.me/codemcp/internal/tools"
)

type HTTPRuntime struct {
	Server          *Runtime
	Activity        *activity.Stream
	Subscriptions   *subscriptionHub
	ApprovalCallers *approval.CallerRegistry
}

func NewHTTPRuntime() *HTTPRuntime { return NewHTTPRuntimeWithTools(tools.NewRuntime()) }

func NewHTTPRuntimeWithTools(toolRuntime *tools.Runtime) *HTTPRuntime {
	return NewHTTPRuntimeWithProfile(toolRuntime, BaseProfile())
}

func NewHTTPRuntimeWithProfile(toolRuntime *tools.Runtime, profile Profile) *HTTPRuntime {
	return &HTTPRuntime{Server: NewRuntimeWithProfile(toolRuntime, profile), Activity: activity.NewStream(), Subscriptions: newSubscriptionHub(), ApprovalCallers: approval.NewCallerRegistry()}
}

func (h *HTTPRuntime) SetAuthRequirements(requirements ...AuthRequirement) {
	if h != nil && h.Server != nil {
		h.Server.SetAuthRequirements(requirements...)
	}
}

func (h *HTTPRuntime) CloseSubscriptions() {
	if h != nil && h.Subscriptions != nil {
		h.Subscriptions.closeAll()
	}
}

func (h *HTTPRuntime) Handler() http.Handler { return h }

func (h HTTPRuntime) emitActivity(method string, params map[string]any, status, message string, duration time.Duration) {
	if method == "tools/call" && h.Server != nil && h.Server.Tools != nil && h.Server.Tools.HasCallObserver() {
		return
	}
	h.EmitActivity(requestActivity(method, params, status, message, duration))
}

func (h HTTPRuntime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req Request
	if err := decodeHTTPRequest(w, r, &req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		writeError(w, ErrParse, err.Error())
		return
	}
	if err := ValidateRequest(req); err != nil {
		protocolErr := err.(*Error)
		writeErrorID(w, req.ID, protocolErr.Code, protocolErr.Message)
		return
	}
	if !IsSupportedMethod(req.Method) {
		writeErrorStatusID(w, http.StatusNotFound, req.ID, ErrMethodNotFound, "method not found")
		return
	}

	params, err := DecodeParams(req.Params)
	if err != nil {
		protocolErr := err.(*Error)
		writeErrorID(w, req.ID, protocolErr.Code, protocolErr.Message)
		return
	}
	if headerErr := ValidateHTTPRoutingHeaders(r, req, params, h.Server.Tools.Registry); headerErr != nil {
		writeProtocolErrorStatusID(w, http.StatusBadRequest, req.ID, headerErr)
		return
	}
	if err := ValidateParams(req.Method, params); err != nil {
		protocolErr := err.(*Error)
		writeErrorID(w, req.ID, protocolErr.Code, protocolErr.Message)
		return
	}
	canonicalRequest, err := requestContextFromParams(params)
	if err != nil {
		writeErrorID(w, req.ID, ErrInvalidParams, err.Error())
		return
	}
	if req.Method == "subscriptions/listen" {
		started := time.Now()
		h.serveSubscription(w, r, req, params)
		h.emitActivity(req.Method, params, "ok", "", time.Since(started))
		return
	}

	started := time.Now()
	requestCtx := WithRequestContext(r.Context(), canonicalRequest)
	if req.Method == "tools/call" {
		if h.ApprovalCallers != nil {
			requestCtx = tools.WithApprovalCorrelation(requestCtx, h.ApprovalCallers.Caller("modern:http"), idgen.Must("apr", 8))
		}
		requestCtx = tools.WithCallRequest(requestCtx, map[string]any{"jsonrpc": req.JSONRPC, "id": req.ID, "method": req.Method, "params": params})
	}
	result, err := h.Server.Handle(requestCtx, req.Method, params)
	duration := time.Since(started)
	if contextErr := r.Context().Err(); contextErr != nil {
		h.emitActivity(req.Method, params, "cancelled", contextErr.Error(), duration)
		return
	}
	if err != nil {
		h.emitActivity(req.Method, params, "error", err.Error(), duration)
		writeProtocolErrorStatusID(w, http.StatusOK, req.ID, ProtocolError(err))
		return
	}

	status, message := "ok", ""
	if toolResult, ok := result.(tools.Result); ok && toolResult.IsError {
		status = "error"
		if len(toolResult.Content) > 0 {
			message = toolResult.Content[0].Text
		}
	}
	h.emitActivity(req.Method, params, status, message, duration)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: result})
}
