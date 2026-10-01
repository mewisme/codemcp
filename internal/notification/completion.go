package notification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	KindCompletionAccepted Kind = "completion.accepted"

	defaultCompletionNotificationMaxSeen = 512
)

type CompletionPolicy struct {
	Enabled   bool
	Providers map[string]bool
}

func (p CompletionPolicy) Allows(event agentcompletion.Event) bool {
	if !p.Enabled || event.Name != agentcompletion.EventAccepted {
		return false
	}
	return strings.TrimSpace(event.Record.ID) != ""
}

type CompletionHookOptions struct {
	Policy  func() CompletionPolicy
	MaxSeen int
}

type CompletionHook struct {
	coordinator *Coordinator
	policy      func() CompletionPolicy
	maxSeen     int

	mu        sync.Mutex
	seen      map[string]struct{}
	seenOrder []string
}

func NewCompletionHook(coordinator *Coordinator, options CompletionHookOptions) *CompletionHook {
	policy := options.Policy
	if policy == nil {
		policy = func() CompletionPolicy { return CompletionPolicy{} }
	}
	maxSeen := options.MaxSeen
	if maxSeen <= 0 {
		maxSeen = defaultCompletionNotificationMaxSeen
	}
	return &CompletionHook{
		coordinator: coordinator,
		policy:      policy,
		maxSeen:     maxSeen,
		seen:        map[string]struct{}{},
	}
}

func (h *CompletionHook) Name() string { return "notification" }

func (h *CompletionHook) Handle(ctx context.Context, invocation agentcompletion.HookInvocation) error {
	if h == nil || h.coordinator == nil {
		return errors.New("completion notification hook is unavailable")
	}
	event := invocation.Event
	policy := h.policy()
	if !policy.Allows(event) {
		return nil
	}
	recordID := strings.TrimSpace(event.Record.ID)

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.seen[recordID]; exists {
		return nil
	}
	message, ok := completionMessage(event)
	if !ok {
		return nil
	}
	if err := h.coordinator.Dispatch(context.WithoutCancel(ctx), message, policy.Providers); err != nil {
		return err
	}
	h.rememberLocked(recordID)
	return nil
}

func (h *CompletionHook) rememberLocked(recordID string) {
	h.seen[recordID] = struct{}{}
	h.seenOrder = append(h.seenOrder, recordID)
	if overflow := len(h.seenOrder) - h.maxSeen; overflow > 0 {
		for _, stale := range h.seenOrder[:overflow] {
			delete(h.seen, stale)
		}
		h.seenOrder = append([]string(nil), h.seenOrder[overflow:]...)
	}
}

func completionMessage(event agentcompletion.Event) (Message, bool) {
	record := event.Record
	if event.Name != agentcompletion.EventAccepted || strings.TrimSpace(record.ID) == "" {
		return Message{}, false
	}
	statusTitle := ""
	switch record.Status {
	case agentcompletion.StatusCompleted:
		statusTitle = "Agent completed"
	case agentcompletion.StatusPartial:
		statusTitle = "Agent partially completed"
	case agentcompletion.StatusBlocked:
		statusTitle = "Agent blocked"
	case agentcompletion.StatusCancelled:
		statusTitle = "Agent cancelled"
	default:
		return Message{}, false
	}

	title := notificationText(record.Title)
	summary := notificationText(record.Summary)
	body := title
	if summary != "" {
		if body != "" {
			body += " — "
		}
		body += summary
	}
	if body == "" {
		body = statusTitle
	}
	timestamp := event.Timestamp.UTC()
	if timestamp.IsZero() {
		timestamp = record.CreatedAt.UTC()
	}
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	return Message{
		ID:           "completion:" + strings.TrimSpace(record.ID),
		Kind:         KindCompletionAccepted,
		Title:        statusTitle,
		Subject:      title,
		Body:         body,
		Summary:      summary,
		Status:       string(record.Status),
		CompletionID: strings.TrimSpace(record.ID),
		WorkspaceID:  strings.TrimSpace(record.WorkspaceID),
		Timestamp:    timestamp,
	}, true
}

func notificationText(value string) string {
	value = tracepkg.SanitizeText(value)
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
