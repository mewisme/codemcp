package codegraph

import (
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

type CompletionSyncState string
type CompletionSyncReason string

const (
	CompletionSyncSucceeded CompletionSyncState = "succeeded"
	CompletionSyncSkipped   CompletionSyncState = "skipped"
	CompletionSyncFailed    CompletionSyncState = "failed"

	CompletionReasonNone        CompletionSyncReason = ""
	CompletionReasonDisabled    CompletionSyncReason = "disabled"
	CompletionReasonUnindexed   CompletionSyncReason = "unindexed"
	CompletionReasonUnavailable CompletionSyncReason = "unavailable"
	CompletionReasonSyncFailed  CompletionSyncReason = "sync_failed"
	CompletionReasonTimeout     CompletionSyncReason = "timeout"
	CompletionReasonCancelled   CompletionSyncReason = "cancelled"
)

type CompletionSyncOutcome struct {
	CompletionID string               `json:"completion_id"`
	Sequence     uint64               `json:"sequence"`
	WorkspaceID  string               `json:"workspace_id"`
	State        CompletionSyncState  `json:"state"`
	Reason       CompletionSyncReason `json:"reason,omitempty"`
	UpdatedAt    time.Time            `json:"updated_at"`
}

func eligibleCompletionStatus(status agentcompletion.Status) bool {
	switch status {
	case agentcompletion.StatusCompleted, agentcompletion.StatusPartial, agentcompletion.StatusBlocked, agentcompletion.StatusCancelled:
		return true
	default:
		return false
	}
}
