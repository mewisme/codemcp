package activity

import tracepkg "go.mewis.me/codemcp/internal/trace"

// PublicEvent preserves stable operator-facing activity identity while removing
// private session, routing, and provider payload data.
func PublicEvent(value Event) Event {
	value.Method = tracepkg.SanitizeText(value.Method)
	value.Source = tracepkg.SanitizeText(value.Source)
	value.Tool = tracepkg.SanitizeText(value.Tool)
	value.WorkspaceID = tracepkg.SanitizeText(value.WorkspaceID)
	value.Status = tracepkg.SanitizeText(value.Status)
	value.Message = tracepkg.SanitizeText(value.Message)
	value.SessionHash = ""
	value.SessionAccess = ""
	value.SessionWorkspaceCount = 0
	value.ReceivedByInstanceID = ""
	value.ExecutedByInstanceID = ""
	value.Raw = nil
	return value
}

func PublicEvents(values []Event) []Event {
	result := make([]Event, len(values))
	for index := range values {
		result[index] = PublicEvent(values[index])
	}
	return result
}

func PublicToolCallRecord(value ToolCallRecord) ToolCallRecord {
	value.First = PublicEvent(value.First)
	value.Latest = PublicEvent(value.Latest)
	return value
}

func PublicToolCallRecords(values []ToolCallRecord) []ToolCallRecord {
	result := make([]ToolCallRecord, len(values))
	for index := range values {
		result[index] = PublicToolCallRecord(values[index])
	}
	return result
}
