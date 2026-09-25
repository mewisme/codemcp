package completion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type Status string

const (
	StatusCompleted Status = "completed"
	StatusPartial   Status = "partial"
	StatusBlocked   Status = "blocked"
	StatusCancelled Status = "cancelled"

	MaxTitleRunes   = 120
	MaxSummaryBytes = 2000
	MaxSourceRunes  = 64

	EventAccepted = "completion.accepted"
)

type Input struct {
	WorkspaceID string
	Status      Status
	Title       string
	Summary     string
}

type Identity struct {
	AgentID string
	Source  string
}

type Record struct {
	ID           string    `json:"id"`
	Sequence     uint64    `json:"sequence"`
	AgentID      string    `json:"agent_id"`
	WorkspaceID  string    `json:"workspace_id"`
	Status       Status    `json:"status"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary,omitempty"`
	Source       string    `json:"source,omitempty"`
	SupersedesID string    `json:"supersedes_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Event struct {
	ID        string    `json:"id"`
	Sequence  uint64    `json:"sequence"`
	Name      string    `json:"name"`
	Record    Record    `json:"record"`
	Timestamp time.Time `json:"timestamp"`
}

type Snapshot struct {
	LatestSequence uint64   `json:"latest_sequence"`
	Records        []Record `json:"records"`
}

func DeriveAgentID(callerID, generationID string) string {
	callerID = strings.TrimSpace(callerID)
	generationID = strings.TrimSpace(generationID)
	if callerID == "" || generationID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(callerID + "\x00" + generationID))
	return hex.EncodeToString(sum[:])[:16]
}

func normalizeInput(identity Identity, input Input) (Identity, Input, error) {
	if !utf8.ValidString(identity.AgentID) || !utf8.ValidString(identity.Source) || !utf8.ValidString(input.WorkspaceID) || !utf8.ValidString(input.Title) || !utf8.ValidString(input.Summary) {
		return Identity{}, Input{}, errors.New("completion metadata must be valid UTF-8")
	}
	identity.AgentID = strings.TrimSpace(identity.AgentID)
	identity.Source = normalizeText(identity.Source)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Title = normalizeText(input.Title)
	input.Summary = normalizeText(input.Summary)
	if len(identity.AgentID) != 16 {
		return Identity{}, Input{}, errors.New("completion agent identity must be a 16-character hash")
	}
	if _, err := hex.DecodeString(identity.AgentID); err != nil || identity.AgentID != strings.ToLower(identity.AgentID) {
		return Identity{}, Input{}, errors.New("completion agent identity must be lowercase hexadecimal")
	}
	if input.WorkspaceID == "" {
		return Identity{}, Input{}, errors.New("completion workspace is required")
	}
	switch input.Status {
	case StatusCompleted, StatusPartial, StatusBlocked, StatusCancelled:
	default:
		return Identity{}, Input{}, fmt.Errorf("unsupported completion status: %s", input.Status)
	}
	if input.Title == "" {
		return Identity{}, Input{}, errors.New("completion title is required")
	}
	if utf8.RuneCountInString(input.Title) > MaxTitleRunes {
		return Identity{}, Input{}, fmt.Errorf("completion title exceeds %d characters", MaxTitleRunes)
	}
	if len([]byte(input.Summary)) > MaxSummaryBytes {
		return Identity{}, Input{}, fmt.Errorf("completion summary exceeds %d bytes", MaxSummaryBytes)
	}
	if utf8.RuneCountInString(identity.Source) > MaxSourceRunes {
		return Identity{}, Input{}, fmt.Errorf("completion source exceeds %d characters", MaxSourceRunes)
	}
	return identity, input, nil
}

func normalizeText(value string) string {
	value = tracepkg.SanitizeText(value)
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func sameTerminalMeaning(record Record, input Input) bool {
	return record.Status == input.Status && record.Title == input.Title && record.Summary == input.Summary
}

func eventFor(record Record) Event {
	return Event{
		ID:        "completion-event:" + record.ID,
		Sequence:  record.Sequence,
		Name:      EventAccepted,
		Record:    record,
		Timestamp: record.CreatedAt,
	}
}
