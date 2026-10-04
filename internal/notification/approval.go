package notification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/approval"
)

type ApprovalPolicy struct {
	Enabled   bool
	Pending   bool
	Resolved  bool
	Providers map[string]bool
}

func (p ApprovalPolicy) Allows(event approval.Event) bool {
	if event.SuppressNotifications || !p.Enabled || strings.TrimSpace(event.RequestID) == "" {
		return false
	}
	switch event.Name {
	case approval.EventPending:
		return p.Pending && event.Subject == approval.EventSubjectRequest
	case approval.EventApproved, approval.EventDenied, approval.EventExpired, approval.EventCancelled:
		return p.Resolved && event.Subject == approval.EventSubjectRequest
	case approval.EventExplanationReady, approval.EventExplanationFailed:
		return p.Pending && event.Subject == approval.EventSubjectRequest
	case approval.EventRevoked:
		return p.Resolved && event.Subject == approval.EventSubjectGrant
	default:
		return false
	}
}

type ApprovalBridgeOptions struct {
	Policy func() ApprovalPolicy
}

type ApprovalBridge struct {
	events      *approval.EventStream
	coordinator *Coordinator
	policy      func() ApprovalPolicy

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	sub         *approval.EventSubscription
	wg          sync.WaitGroup
}

func NewApprovalBridge(events *approval.EventStream, coordinator *Coordinator, options ApprovalBridgeOptions) *ApprovalBridge {
	policy := options.Policy
	if policy == nil {
		policy = func() ApprovalPolicy { return ApprovalPolicy{} }
	}
	return &ApprovalBridge{events: events, coordinator: coordinator, policy: policy}
}

func (b *ApprovalBridge) Start(parent context.Context) error {
	if b == nil {
		return errors.New("approval notification bridge is unavailable")
	}
	if b.events == nil {
		return errors.New("approval event stream is unavailable")
	}
	if b.coordinator == nil {
		return errors.New("notification coordinator is unavailable")
	}
	if parent == nil {
		parent = context.Background()
	}
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	if b.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	sub := b.events.Subscribe()
	b.cancel, b.sub = cancel, sub
	b.wg.Add(1)
	go b.run(ctx, sub)
	return nil
}

func (b *ApprovalBridge) Stop() {
	if b == nil {
		return
	}
	b.lifecycleMu.Lock()
	cancel, sub := b.cancel, b.sub
	b.cancel, b.sub = nil, nil
	b.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if sub != nil && b.events != nil {
		b.events.Unsubscribe(sub)
	}
	b.wg.Wait()
}

func (b *ApprovalBridge) run(ctx context.Context, sub *approval.EventSubscription) {
	defer b.wg.Done()
	if sub == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			b.consume(ctx, event)
		case _, ok := <-sub.Overflow:
			if !ok {
				return
			}
			b.events.AcknowledgeOverflow(sub)
		}
	}
}

func (b *ApprovalBridge) consume(ctx context.Context, event approval.Event) {
	policy := b.policy()
	if !policy.Allows(event) {
		return
	}
	message, ok := approvalMessage(event)
	if !ok {
		return
	}
	providers := policy.Providers
	if message.Kind == KindApprovalUpdated {
		providers = map[string]bool{ProviderTelegram: policy.Providers[ProviderTelegram]}
	}
	_ = b.coordinator.Dispatch(ctx, message, providers)
}

func approvalMessage(event approval.Event) (Message, bool) {
	message := Message{
		ID: event.Name + ":" + event.RequestID, RequestID: event.RequestID, WorkspaceID: event.WorkspaceID,
		TargetTool: event.TargetTool, Timestamp: event.Timestamp.UTC(),
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now().UTC()
	}
	target := strings.TrimSpace(event.TargetTool)
	workspace := strings.TrimSpace(event.WorkspaceID)
	contextText := target
	if contextText == "" {
		contextText = "CodeMCP operation"
	}
	if workspace != "" {
		contextText += " in " + workspace
	}
	switch event.Name {
	case approval.EventPending:
		message.Kind = KindApprovalPending
		message.Title = "Approval requested"
		message.Body = contextText + " requires review"
		message.Actions = []Action{{ID: "view", Label: "Review"}, {ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}}
		return message, true
	case approval.EventApproved:
		message.Kind, message.Title, message.Body = KindApprovalResolved, "Approval resolved", contextText+" was approved"
	case approval.EventDenied:
		message.Kind, message.Title, message.Body = KindApprovalResolved, "Approval resolved", contextText+" was denied"
	case approval.EventExpired:
		message.Kind, message.Title, message.Body = KindApprovalResolved, "Approval expired", contextText+" expired"
	case approval.EventCancelled:
		message.Kind, message.Title, message.Body = KindApprovalResolved, "Approval cancelled", contextText+" was cancelled"
	case approval.EventExplanationReady:
		message.Kind, message.Title, message.Body = KindApprovalUpdated, "Approval explanation ready", "The approval review changed"
	case approval.EventExplanationFailed:
		message.Kind, message.Title, message.Body = KindApprovalUpdated, "Approval explanation failed", "The approval review changed"
	case approval.EventRevoked:
		message.Kind, message.Title, message.Body = KindApprovalResolved, "Approval grant revoked", contextText+" grant was revoked"
	default:
		return Message{}, false
	}
	return message, true
}
