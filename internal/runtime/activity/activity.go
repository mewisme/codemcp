package activity

import (
	"time"
)

type Event struct {
	Sequence              uint64         `json:"sequence,omitempty"`
	CallID                string         `json:"call_id,omitempty"`
	Kind                  string         `json:"kind"`
	Phase                 string         `json:"phase,omitempty"`
	Method                string         `json:"method,omitempty"`
	Source                string         `json:"source,omitempty"`
	Tool                  string         `json:"tool,omitempty"`
	WorkspaceID           string         `json:"workspace_id,omitempty"`
	SessionHash           string         `json:"session_hash,omitempty"`
	SessionAccess         string         `json:"session_access,omitempty"`
	SessionWorkspaceCount int            `json:"session_workspace_count,omitempty"`
	ReceivedByInstanceID  string         `json:"received_by_instance_id,omitempty"`
	ExecutedByInstanceID  string         `json:"executed_by_instance_id,omitempty"`
	Status                string         `json:"status,omitempty"`
	DurationMS            int64          `json:"duration_ms,omitempty"`
	Message               string         `json:"message,omitempty"`
	Raw                   map[string]any `json:"raw,omitempty"`
	Timestamp             time.Time      `json:"timestamp"`
}

func normalizeEvent(event Event) Event {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	return event
}
