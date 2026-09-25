package completion

import "strings"

type HealthStatus string

const (
	HealthHealthy  HealthStatus = "healthy"
	HealthDegraded HealthStatus = "degraded"
	HealthCorrupt  HealthStatus = "corrupt"
	HealthClosed   HealthStatus = "closed"
)

type HookHealth struct {
	Stopped           bool `json:"stopped"`
	Registered        int  `json:"registered"`
	RecentDiagnostics int  `json:"recent_diagnostics"`
	Failures          int  `json:"failures"`
	Timeouts          int  `json:"timeouts"`
	Cancelled         int  `json:"cancelled"`
	Duplicates        int  `json:"duplicates"`
}

type Health struct {
	WorkspaceID      string       `json:"workspace_id"`
	Status           HealthStatus `json:"status"`
	HotRecords       int          `json:"hot_records"`
	ArchivedRecords  int          `json:"archived_records"`
	LatestSequence   uint64       `json:"latest_sequence"`
	ArchiveTailIssue bool         `json:"archive_tail_issue"`
	Hooks            HookHealth   `json:"hooks"`
	Error            string       `json:"error,omitempty"`
}

func (s *Service) Diagnose(workspaceID string) Health {
	result := Health{WorkspaceID: strings.TrimSpace(workspaceID), Status: HealthHealthy}
	if s == nil {
		result.Status = HealthCorrupt
		result.Error = "agent completion service is unavailable"
		return result
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		result.Status = HealthClosed
	}
	local, canonical, err := s.workspaceStore(workspaceID)
	if err != nil {
		result.Status = HealthCorrupt
		result.Error = err.Error()
		result.Hooks = s.hookHealth()
		return result
	}
	result.WorkspaceID = canonical
	history, _, err := loadWorkspaceHistory(local)
	if err != nil {
		result.Status = HealthCorrupt
		result.Error = err.Error()
		result.Hooks = s.hookHealth()
		return result
	}
	result.HotRecords = len(history.Records)
	archive, err := loadWorkspaceArchive(local)
	if err != nil {
		result.Status = HealthCorrupt
		result.Error = err.Error()
		result.Hooks = s.hookHealth()
		return result
	}
	result.ArchivedRecords = len(archive)
	tailIssue, err := archiveTailIssue(local)
	if err != nil {
		result.Status = HealthCorrupt
		result.Error = err.Error()
		result.Hooks = s.hookHealth()
		return result
	}
	result.ArchiveTailIssue = tailIssue
	if tailIssue && result.Status == HealthHealthy {
		result.Status = HealthDegraded
	}
	for _, record := range mergeCompletionRecords(archive, history.Records) {
		if record.Sequence > result.LatestSequence {
			result.LatestSequence = record.Sequence
		}
	}
	result.Hooks = s.hookHealth()
	if !result.Hooks.Stopped && (result.Hooks.Failures > 0 || result.Hooks.Timeouts > 0) && result.Status == HealthHealthy {
		result.Status = HealthDegraded
	}
	if result.Hooks.Stopped && result.Status == HealthHealthy {
		result.Status = HealthClosed
	}
	return result
}

func (s *Service) hookHealth() HookHealth {
	if s == nil || s.hooks == nil {
		return HookHealth{}
	}
	return s.hooks.Health()
}
