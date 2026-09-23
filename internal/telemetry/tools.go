package telemetry

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/activity"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/tools"
)

func AttachTools(runtime *tools.Runtime, stream *activity.Stream, log *logger.Logger) {
	if runtime == nil {
		return
	}
	runtime.SetCallObserver(func(observation tools.CallObservation) {
		fields := []logger.Field{logger.With("tool", observation.Tool)}
		if observation.CallID != "" {
			fields = append(fields, logger.With("call_id", observation.CallID))
		}
		if observation.Source != "" {
			fields = append(fields, logger.With("source", observation.Source))
		}
		if observation.WorkspaceID != "" {
			fields = append(fields, logger.With("workspace", observation.WorkspaceID))
		}
		if observation.SessionHash != "" {
			fields = append(fields, logger.WithVerbose("session", observation.SessionHash))
		}
		if observation.SessionAccess != "" {
			fields = append(fields, logger.WithVerbose("session_access", string(observation.SessionAccess)))
		}
		if observation.SessionWorkspaceCount > 0 {
			fields = append(fields, logger.WithVerbose("session_workspace_count", observation.SessionWorkspaceCount))
		}
		if observation.ReceivedByInstanceID != "" {
			fields = append(fields, logger.WithVerbose("received_by", observation.ReceivedByInstanceID))
		}
		if observation.ExecutedByInstanceID != "" {
			fields = append(fields, logger.WithVerbose("executed_by", observation.ExecutedByInstanceID))
		}
		if observation.Phase == "start" {
			if log != nil {
				startFields := append(fields, logger.With("status", "running"))
				log.Emit(logger.Event{Level: logger.Info, Name: "tool.call.started", Message: "Tool call started", Fields: startFields, Component: "TOOL", Kind: logger.KindInfo, Visibility: logger.VisibilityVerbose})
			}
			if stream != nil {
				stream.Publish(activity.Event{CallID: observation.CallID, Kind: string(activity.EventToolCall), Phase: "start", Method: "tools/call", Source: observation.Source, Tool: observation.Tool, WorkspaceID: observation.WorkspaceID, SessionHash: observation.SessionHash, SessionAccess: string(observation.SessionAccess), SessionWorkspaceCount: observation.SessionWorkspaceCount, ReceivedByInstanceID: observation.ReceivedByInstanceID, ExecutedByInstanceID: observation.ExecutedByInstanceID, Status: "running", Raw: observation.Raw})
			}
			return
		}
		fields = append(fields, logger.With("duration_ms", observation.DurationMS), logger.WithDebug("status", observation.Status))
		if observation.ResultType != "" {
			fields = append(fields, logger.With("result_type", observation.ResultType))
		}
		if log != nil {
			event := logger.Event{Level: logger.Info, Name: "tool.call.completed", Message: "Tool call completed", Fields: fields, Component: "TOOL", Visibility: logger.VisibilityVerbose}
			switch observation.Status {
			case "cancelled":
				event.Level = logger.Warn
				event.Kind = logger.KindWarning
				event.Name = "tool.call.cancelled"
				event.Message = "Tool call cancelled"
			case "error":
				event.Level = logger.Error
				event.Kind = logger.KindError
				event.Name = "tool.call.failed"
				event.Message = "Tool call failed"
			default:
				event.Kind = logger.KindSuccess
			}
			if strings.TrimSpace(observation.Message) != "" && observation.Status != "ok" {
				event.Err = errors.New(strings.TrimSpace(observation.Message))
			}
			log.Emit(event)
		}
		if stream != nil {
			stream.Publish(activity.Event{CallID: observation.CallID, Kind: string(activity.EventToolCall), Phase: "finish", Method: "tools/call", Source: observation.Source, Tool: observation.Tool, WorkspaceID: observation.WorkspaceID, SessionHash: observation.SessionHash, SessionAccess: string(observation.SessionAccess), SessionWorkspaceCount: observation.SessionWorkspaceCount, ReceivedByInstanceID: observation.ReceivedByInstanceID, ExecutedByInstanceID: observation.ExecutedByInstanceID, Status: observation.Status, DurationMS: observation.DurationMS, Message: strings.TrimSpace(observation.Message), Raw: observation.Raw})
		}
	})
}
