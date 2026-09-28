package shell

import tracepkg "go.mewis.me/codemcp/internal/trace"

// PublicExecutionInfo returns the operator-visible execution projection.
// Correlation, security-binding, session, and instance-routing fields remain
// private to the runtime owner.
func PublicExecutionInfo(value ExecutionInfo) ExecutionInfo {
	value.Command = tracepkg.SanitizeText(tracepkg.SanitizeCommand(value.Command))
	value.RequestedCommand = tracepkg.SanitizeText(tracepkg.SanitizeCommand(value.RequestedCommand))
	value.EffectiveCommand = tracepkg.SanitizeText(tracepkg.SanitizeCommand(value.EffectiveCommand))
	value.CWD = tracepkg.SanitizeText(value.CWD)
	value.Shell = tracepkg.SanitizeText(value.Shell)
	value.Source = tracepkg.SanitizeText(value.Source)
	value.SecurityCommand = ""
	value.CallID = ""
	value.SessionHash = ""
	value.ReceivedByInstanceID = ""
	value.ExecutedByInstanceID = ""
	return value
}

func PublicExecutionInfos(values []ExecutionInfo) []ExecutionInfo {
	result := make([]ExecutionInfo, len(values))
	for index := range values {
		result[index] = PublicExecutionInfo(values[index])
	}
	return result
}

func PublicExecutionSnapshot(value ExecutionSnapshot) ExecutionSnapshot {
	value.Execution = PublicExecutionInfo(value.Execution)
	value.Stdout = tracepkg.SanitizeText(value.Stdout)
	value.Stderr = tracepkg.SanitizeText(value.Stderr)
	return value
}

func PublicExecutionEvent(value ExecutionEvent) ExecutionEvent {
	value.Data = tracepkg.SanitizeText(value.Data)
	return value
}

func PublicExecutionFeedEvent(value ExecutionFeedEvent) ExecutionFeedEvent {
	if value.Execution != nil {
		execution := PublicExecutionInfo(*value.Execution)
		value.Execution = &execution
	}
	value.Data = tracepkg.SanitizeText(value.Data)
	return value
}

func PublicExecutionFeedSnapshot(value ExecutionFeedSnapshot) ExecutionFeedSnapshot {
	value.Executions = PublicExecutionInfos(value.Executions)
	events := make([]ExecutionFeedEvent, len(value.Events))
	for index := range value.Events {
		events[index] = PublicExecutionFeedEvent(value.Events[index])
	}
	value.Events = events
	return value
}
