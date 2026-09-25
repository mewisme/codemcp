package telemetry

import (
	"sync"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtime/activity"
)

func AttachApprovals(manager *approval.Manager, stream *activity.Stream, log *logger.Logger) func() {
	if manager == nil {
		return func() {}
	}
	events := manager.Events()
	if events == nil {
		return func() {}
	}
	sub := events.Subscribe()
	go func() {
		for event := range sub.Events {
			publishApprovalTelemetry(event, stream, log)
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { events.Unsubscribe(sub) })
	}
}

func publishApprovalTelemetry(event approval.Event, stream *activity.Stream, log *logger.Logger) {
	fields := []logger.Field{
		logger.With("workspace", event.WorkspaceID), logger.With("tool", event.TargetTool), logger.With("status", event.Status), logger.With("subject", event.Subject),
	}
	if event.RequestID != "" {
		fields = append(fields, logger.With("request", event.RequestID))
	}
	if event.ChallengeID != "" {
		fields = append(fields, logger.WithVerbose("challenge", event.ChallengeID))
	}
	if event.Source != "" {
		fields = append(fields, logger.With("source", event.Source))
	}
	if event.SessionHash != "" {
		fields = append(fields, logger.WithVerbose("session", event.SessionHash))
	}
	if log != nil {
		if event.Name == approval.EventPending {
			log.Notice("APPROVAL", event.Name, "Control approval requested", fields...)
		} else {
			log.Verbose("APPROVAL", event.Name, "Control approval updated", fields...)
		}
	}
	if stream != nil {
		stream.Publish(activity.Event{
			Kind: string(activity.EventApproval), Source: event.Source, Tool: event.TargetTool, WorkspaceID: event.WorkspaceID, SessionHash: event.SessionHash, Status: string(event.Status), Message: approvalEventMessage(event),
			Raw: map[string]any{"event": event.Name, "subject": event.Subject, "challenge_id": event.ChallengeID, "request_id": event.RequestID, "expires_at": event.ExpiresAt, "retry_until": event.RetryUntil, "grant_expires_at": event.GrantExpiresAt},
		})
	}
}

func approvalEventMessage(event approval.Event) string {
	switch event.Name {
	case approval.EventCreated:
		return "Approval challenge created"
	case approval.EventPending:
		return "Control approval requested"
	case approval.EventApproved:
		return "Approval approved"
	case approval.EventDenied:
		return "Approval denied"
	case approval.EventExpired:
		return "Approval expired"
	case approval.EventClaimed:
		return "Approval claimed"
	case approval.EventRevoked:
		return "Approval grant revoked"
	case approval.EventCancelled:
		return "Approval cancelled"
	default:
		return "Approval updated"
	}
}
