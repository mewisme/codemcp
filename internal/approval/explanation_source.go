package approval

import (
	"errors"
	"time"
)

// ExplanationSource is the minimal private request projection used to explain
// an exact command. It deliberately excludes agent-authored titles, summaries,
// reasons, arguments, digest material, and reviewer state.
type ExplanationSource struct {
	RequestID   string
	WorkspaceID string
	TargetTool  string
	Command     string
	Status      Status
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

func (m *Manager) ExplanationSource(reference string) (ExplanationSource, error) {
	if m == nil {
		return ExplanationSource{}, errors.New("approval manager is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	record, err := m.resolveRecordLocked(reference)
	if err != nil {
		return ExplanationSource{}, err
	}
	value := record.value
	return ExplanationSource{
		RequestID: value.ID, WorkspaceID: value.WorkspaceID, TargetTool: value.TargetTool,
		Command: value.Command, Status: value.Status, CreatedAt: value.CreatedAt, ExpiresAt: value.ExpiresAt,
	}, nil
}

func (m *Manager) PublishExplanationEvent(source ExplanationSource, name string, attempt uint64) {
	if m == nil || m.events == nil || source.RequestID == "" || name == "" {
		return
	}
	m.events.Publish(Event{
		Name: name, Subject: EventSubjectRequest, RequestID: source.RequestID,
		WorkspaceID: source.WorkspaceID, TargetTool: source.TargetTool, Status: source.Status,
		CreatedAt: source.CreatedAt, ExpiresAt: source.ExpiresAt, ExplanationAttempt: attempt,
		Timestamp: m.now().UTC(),
	})
}
