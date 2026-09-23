package telemetry

import (
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtime/activity"
)

func AttachApprovals(manager *approval.Manager, stream *activity.Stream, log *logger.Logger) {
	if manager == nil {
		return
	}
	manager.SetEventObserver(func(event approval.Event) {
		fields := []logger.Field{
			logger.With("request", event.RequestID), logger.With("workspace", event.WorkspaceID), logger.With("tool", event.TargetTool), logger.With("status", event.Status),
		}
		if event.Source != "" {
			fields = append(fields, logger.With("source", event.Source))
		}
		if event.SessionHash != "" {
			fields = append(fields, logger.WithVerbose("session", event.SessionHash))
		}
		if log != nil {
			if event.Name == approval.EventRequested {
				log.Notice("APPROVAL", event.Name, "Control approval requested", fields...)
			} else {
				log.Verbose("APPROVAL", event.Name, "Control approval updated", fields...)
			}
		}
		if stream != nil {
			stream.Publish(activity.Event{
				Kind: string(activity.EventApproval), Source: event.Source, Tool: event.TargetTool, WorkspaceID: event.WorkspaceID, SessionHash: event.SessionHash, Status: string(event.Status), Message: event.Title,
				Raw: map[string]any{"event": event.Name, "request_id": event.RequestID, "title": event.Title, "expires_at": event.ExpiresAt, "retry_until": event.RetryUntil},
			})
		}
	})
}
