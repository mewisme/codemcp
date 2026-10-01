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

// PublicToolCallDetail returns the single canonical operator-facing detail
// projection. Request, response and error remain semantically distinct while
// every diagnostic payload is re-sanitized at the projection boundary.
func PublicToolCallDetail(value ToolCallDetail) ToolCallDetail {
	value.Event = PublicEvent(value.Event)
	meta := value.Diagnostic
	if value.Request != nil {
		value.Request, meta = sanitizePublicDiagnostic(value.Request, meta)
	}
	if value.Response != nil {
		value.Response, meta = sanitizePublicDiagnostic(value.Response, meta)
	}
	if value.Error != nil {
		value.Error, meta = sanitizePublicDiagnostic(value.Error, meta)
	}
	value.Diagnostic = meta
	return value
}

func sanitizePublicDiagnostic(value any, meta DiagnosticMeta) (any, DiagnosticMeta) {
	public, next := SanitizeDiagnostic(value)
	return public, mergeDiagnosticMeta(meta, next)
}
