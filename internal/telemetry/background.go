package telemetry

import (
	"sync"

	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtime/activity"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func AttachBackground(processes *shellruntime.ProcessManager, stream *activity.Stream, log *logger.Logger) func() {
	if processes == nil {
		return func() {}
	}
	sub := processes.SubscribeTerminal()
	go func() {
		for event := range sub.Events {
			publishBackgroundTelemetry(event, stream, log)
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { processes.UnsubscribeTerminal(sub) })
	}
}

func publishBackgroundTelemetry(event shellruntime.BackgroundWorkTerminalEvent, stream *activity.Stream, log *logger.Logger) {
	fields := []logger.Field{
		logger.With("workspace", event.WorkspaceID),
		logger.With("process", event.ProcessID),
		logger.With("status", event.Status),
		logger.With("reason", event.Reason),
	}
	if event.ExecutionID != "" {
		fields = append(fields, logger.With("execution", event.ExecutionID))
	}
	if log != nil {
		log.Verbose("PROCESS", "background.process.finished", "Background process finished", fields...)
	}
	if stream != nil {
		stream.Publish(activity.Event{
			CallID:      event.CallID,
			Kind:        string(activity.EventBackground),
			Phase:       "finish",
			Tool:        event.Tool,
			WorkspaceID: event.WorkspaceID,
			SessionHash: event.SessionHash,
			Status:      event.Status,
			Message:     "Background process finished",
			Raw: map[string]any{
				"process_id":   event.ProcessID,
				"execution_id": event.ExecutionID,
				"reason":       event.Reason,
				"timed_out":    event.TimedOut,
			},
		})
	}
}
