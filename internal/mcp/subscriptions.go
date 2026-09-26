package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/sequence"
)

const (
	defaultSubscriptionKeepAlive    = 15 * time.Second
	defaultMaxSubscriptions         = 1024
	maxResourceSubscriptionsPerCall = 64
)

type subscriptionHub struct {
	mu      sync.Mutex
	active  int
	closed  bool
	closeCh chan struct{}
}

func newSubscriptionHub() *subscriptionHub {
	return &subscriptionHub{closeCh: make(chan struct{})}
}

func (h *subscriptionHub) acquire() (<-chan struct{}, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("subscription service is closed")
	}
	if h.active >= defaultMaxSubscriptions {
		return nil, errors.New("too many active subscriptions")
	}
	h.active++
	return h.closeCh, nil
}

func (h *subscriptionHub) release() {
	h.mu.Lock()
	if h.active > 0 {
		h.active--
	}
	h.mu.Unlock()
}

func (h *subscriptionHub) closeAll() {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.closeCh)
	}
	h.mu.Unlock()
}

func (h HTTPRuntime) serveSubscription(w http.ResponseWriter, r *http.Request, req Request, params map[string]any) {
	if h.Subscriptions == nil || h.Server == nil || h.Server.Tools == nil || h.Server.Tools.Registry == nil || h.Server.Features == nil || h.Server.Features.Registry == nil {
		writeErrorID(w, req.ID, ErrInternal, "subscription runtime unavailable")
		return
	}
	closeCh, err := h.Subscriptions.acquire()
	if err != nil {
		writeErrorID(w, req.ID, ErrInternal, err.Error())
		return
	}
	defer h.Subscriptions.release()

	notifications, _ := params["notifications"].(map[string]any)
	toolsRequested, _ := notifications["toolsListChanged"].(bool)
	resourcesRequested, _ := notifications["resourcesListChanged"].(bool)
	honoredTools := toolsRequested && DefaultCapabilities().Tools.ListChanged
	honoredResources := resourcesRequested

	resourceSubscriptions, err := h.authorizedResourceSubscriptions(r, notifications)
	if err != nil {
		writeProtocolErrorStatusID(w, http.StatusOK, req.ID, ProtocolError(err))
		return
	}

	var toolChanges <-chan struct{}
	var toolSubscription chan struct{}
	if honoredTools {
		toolSubscription = h.Server.Tools.Registry.SubscribeChanges()
		defer h.Server.Tools.Registry.UnsubscribeChanges(toolSubscription)
		toolChanges = toolSubscription
	}

	var resourceListSubscription *ResourceListChangeSubscription
	var resourceListChanges <-chan ResourceListChange
	var resourceListOverflow <-chan sequence.Overflow
	if honoredResources {
		resourceListSubscription, _ = h.Server.Features.Registry.SubscribeResourceListChanges(0)
		if resourceListSubscription != nil {
			defer h.Server.Features.Registry.UnsubscribeResourceListChanges(resourceListSubscription)
			resourceListChanges = resourceListSubscription.Events
			resourceListOverflow = resourceListSubscription.Overflow
		}
	}

	var instructionSubscription *instructioncontext.ChangeSubscription
	var instructionEvents <-chan instructioncontext.Change
	var instructionOverflow <-chan sequence.Overflow
	if len(resourceSubscriptions) > 0 && h.Server.Tools.InstructionChanges != nil {
		instructionSubscription, _ = h.Server.Tools.InstructionChanges.Subscribe(0)
		if instructionSubscription != nil {
			defer h.Server.Tools.InstructionChanges.Unsubscribe(instructionSubscription)
			instructionEvents = instructionSubscription.Events
			instructionOverflow = instructionSubscription.Overflow
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	meta := map[string]any{"io.modelcontextprotocol/subscriptionId": req.ID}
	honored := map[string]any{}
	if honoredTools {
		honored["toolsListChanged"] = true
	}
	if honoredResources {
		honored["resourcesListChanged"] = true
	}
	if len(resourceSubscriptions) > 0 {
		values := make([]string, 0, len(resourceSubscriptions))
		for _, resource := range resourceSubscriptions {
			values = append(values, resource.URI)
		}
		honored["resourceSubscriptions"] = values
	}
	ack := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/subscriptions/acknowledged",
		"params":  map[string]any{"notifications": honored, "_meta": meta},
	}
	if err := writeSubscriptionFrame(w, ack); err != nil {
		return
	}
	flusher.Flush()

	keepAlive := time.NewTicker(defaultSubscriptionKeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-closeCh:
			result := map[string]any{
				"resultType": "complete",
				"_meta": map[string]any{
					"io.modelcontextprotocol/subscriptionId": req.ID,
					"io.modelcontextprotocol/serverInfo":     serverInfo(),
				},
			}
			if err := writeSubscriptionFrame(w, Response{JSONRPC: "2.0", ID: req.ID, Result: result}); err == nil {
				flusher.Flush()
			}
			return
		case <-toolChanges:
			if !writeSubscriptionNotification(w, flusher, "notifications/tools/list_changed", map[string]any{"_meta": meta}) {
				return
			}
		case <-resourceListChanges:
			if !writeSubscriptionNotification(w, flusher, "notifications/resources/list_changed", map[string]any{"_meta": meta}) {
				return
			}
		case <-resourceListOverflow:
			if !writeSubscriptionNotification(w, flusher, "notifications/resources/list_changed", map[string]any{"_meta": meta}) {
				return
			}
			h.Server.Features.Registry.AcknowledgeResourceListOverflow(resourceListSubscription)
		case change := <-instructionEvents:
			if !writeAffectedResourceUpdates(w, flusher, meta, resourceSubscriptions, change) {
				return
			}
		case <-instructionOverflow:
			for _, resource := range resourceSubscriptions {
				if !writeSubscriptionNotification(w, flusher, "notifications/resources/updated", map[string]any{"uri": resource.URI, "_meta": meta}) {
					return
				}
			}
			h.Server.Tools.InstructionChanges.AcknowledgeOverflow(instructionSubscription)
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h HTTPRuntime) authorizedResourceSubscriptions(r *http.Request, notifications map[string]any) ([]ParsedResourceURI, error) {
	raw, _ := notifications["resourceSubscriptions"].([]any)
	if len(raw) == 0 {
		return nil, nil
	}
	ctx := r.Context()
	seen := map[string]bool{}
	result := make([]ParsedResourceURI, 0, len(raw))
	for _, value := range raw {
		uri, _ := value.(string)
		parsed, err := h.Server.Features.AuthorizeResourceSubscription(ctx, uri)
		if err != nil {
			return nil, err
		}
		if seen[parsed.URI] {
			continue
		}
		seen[parsed.URI] = true
		result = append(result, parsed)
	}
	return result, nil
}

func writeAffectedResourceUpdates(w http.ResponseWriter, flusher http.Flusher, meta map[string]any, resources []ParsedResourceURI, change instructioncontext.Change) bool {
	for _, resource := range resources {
		if !instructionChangeAffectsResource(change, resource) {
			continue
		}
		if !writeSubscriptionNotification(w, flusher, "notifications/resources/updated", map[string]any{"uri": resource.URI, "_meta": meta}) {
			return false
		}
	}
	return true
}

func writeSubscriptionNotification(w http.ResponseWriter, flusher http.Flusher, method string, params map[string]any) bool {
	notification := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if err := writeSubscriptionFrame(w, notification); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func writeSubscriptionFrame(w http.ResponseWriter, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func validateListenNotifications(params map[string]any) error {
	raw, exists := params["notifications"]
	if !exists {
		return NewError(ErrInvalidParams, "notifications is required")
	}
	notifications, ok := raw.(map[string]any)
	if !ok {
		return NewError(ErrInvalidParams, "notifications must be an object")
	}
	for _, key := range []string{"toolsListChanged", "promptsListChanged", "resourcesListChanged"} {
		if value, exists := notifications[key]; exists {
			if _, ok := value.(bool); !ok {
				return NewError(ErrInvalidParams, key+" must be a boolean")
			}
		}
	}
	if value, exists := notifications["resourceSubscriptions"]; exists {
		items, ok := value.([]any)
		if !ok {
			return NewError(ErrInvalidParams, "resourceSubscriptions must be an array")
		}
		if len(items) > maxResourceSubscriptionsPerCall {
			return NewError(ErrInvalidParams, "too many resourceSubscriptions")
		}
		for _, item := range items {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return NewError(ErrInvalidParams, "resourceSubscriptions must contain non-empty strings")
			}
		}
	}
	return nil
}
