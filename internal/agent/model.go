package agent

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/idgen"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	MaxPromptBytes          = 64 * 1024
	MaxResultBytes          = 256 * 1024
	MaxErrorBytes           = 4 * 1024
	MaxModelBytes           = 128
	MaxReasoningEffortBytes = 64
	InitialMaxDepth         = 1
	MaxParallelLimit        = 128
)

var managedIDPattern = regexp.MustCompile(`^agent_[0-9a-f]{16}$`)

type ID string

func NewID() (ID, error) {
	value, err := idgen.New("agent", 8)
	if err != nil {
		return "", fmt.Errorf("generate managed agent id: %w", err)
	}
	return ID(value), nil
}

func ValidateID(id ID) error {
	if !managedIDPattern.MatchString(string(id)) {
		return errors.New("managed agent id must use agent_<16 lowercase hex characters>")
	}
	return nil
}

type State string

const (
	StateStarting          State = "starting"
	StateWorking           State = "working"
	StateIdle              State = "idle"
	StateCompletionPending State = "completion_pending"
	StateCompleted         State = "completed"
	StatePartial           State = "partial"
	StateBlocked           State = "blocked"
	StateCancelled         State = "cancelled"
	StateFailed            State = "failed"
	StateExpired           State = "expired"
)

func (state State) Valid() bool {
	switch state {
	case StateStarting, StateWorking, StateIdle, StateCompletionPending,
		StateCompleted, StatePartial, StateBlocked, StateCancelled,
		StateFailed, StateExpired:
		return true
	default:
		return false
	}
}

func (state State) Terminal() bool {
	switch state {
	case StateCompleted, StatePartial, StateBlocked, StateCancelled, StateFailed, StateExpired:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case StateStarting:
		return to == StateWorking || to == StateFailed || to == StateCancelled
	case StateWorking:
		return to == StateIdle || to == StateCompletionPending || to == StateFailed || to == StateCancelled
	case StateIdle:
		return to == StateWorking || to == StateExpired || to == StateFailed || to == StateCancelled
	case StateCompletionPending:
		return to == StateCompleted || to == StatePartial || to == StateBlocked || to == StateCancelled || to == StateFailed || to == StateExpired
	default:
		return false
	}
}

func ValidateTransition(from, to State) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid managed agent state transition %q -> %q", from, to)
	}
	return nil
}

type SpawnInput struct {
	WorkspaceID     string    `json:"workspace_id"`
	Prompt          string    `json:"prompt"`
	Backend         BackendID `json:"backend,omitempty"`
	Model           string    `json:"model,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
}

func NormalizeSpawnInput(input SpawnInput) (SpawnInput, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if err := workspacestate.ValidateIdentityID(input.WorkspaceID); err != nil {
		return SpawnInput{}, fmt.Errorf("workspace_id: %w", err)
	}
	if err := validateRequiredText("prompt", input.Prompt, MaxPromptBytes); err != nil {
		return SpawnInput{}, err
	}
	if input.Backend != "" {
		backend, err := NormalizeBackendID(string(input.Backend))
		if err != nil {
			return SpawnInput{}, err
		}
		input.Backend = backend
	}
	var err error
	if input.Model, err = normalizeOptionalText("model", input.Model, MaxModelBytes); err != nil {
		return SpawnInput{}, err
	}
	if input.ReasoningEffort, err = normalizeOptionalText("reasoning_effort", input.ReasoningEffort, MaxReasoningEffortBytes); err != nil {
		return SpawnInput{}, err
	}
	return input, nil
}

type Record struct {
	ID              ID         `json:"id"`
	Backend         BackendID  `json:"backend"`
	WorkspaceID     string     `json:"workspace_id"`
	ParentID        ID         `json:"parent_id,omitempty"`
	Depth           int        `json:"depth"`
	Model           string     `json:"model,omitempty"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	State           State      `json:"state"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Turn            uint64     `json:"turn"`
	Revision        uint64     `json:"revision"`
	Result          string     `json:"result,omitempty"`
	Error           string     `json:"error,omitempty"`
	Owner           Controller `json:"-"`
}

type Snapshot struct {
	ID              ID        `json:"id"`
	Backend         BackendID `json:"backend"`
	WorkspaceID     string    `json:"workspace_id"`
	ParentID        ID        `json:"parent_id,omitempty"`
	Depth           int       `json:"depth"`
	Model           string    `json:"model,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	State           State     `json:"state"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Turn            uint64    `json:"turn"`
	Revision        uint64    `json:"revision"`
	Result          string    `json:"result,omitempty"`
	Error           string    `json:"error,omitempty"`
}

func (record Record) Snapshot() Snapshot {
	return Snapshot{
		ID: record.ID, Backend: record.Backend, WorkspaceID: record.WorkspaceID,
		ParentID: record.ParentID, Depth: record.Depth, Model: record.Model,
		ReasoningEffort: record.ReasoningEffort, State: record.State,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, Turn: record.Turn, Revision: record.Revision,
		Result: BoundResult(record.Result), Error: BoundErrorText(record.Error),
	}
}

func BoundResult(value string) string {
	return boundUTF8(value, MaxResultBytes)
}

func BoundError(err error) string {
	if err == nil {
		return ""
	}
	return BoundErrorText(err.Error())
}

func BoundErrorText(value string) string {
	return boundUTF8(strings.TrimSpace(value), MaxErrorBytes)
}

func ValidateDelegation(parentID ID, depth int) error {
	if depth < 1 || depth > InitialMaxDepth {
		return fmt.Errorf("managed agent depth must be between 1 and %d", InitialMaxDepth)
	}
	if parentID != "" {
		if err := ValidateID(parentID); err != nil {
			return fmt.Errorf("parent_id: %w", err)
		}
		if depth <= 1 {
			return errors.New("managed parent requires nested delegation depth")
		}
	}
	return nil
}

func validateRequiredText(field, value string, limit int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if len(value) > limit {
		return fmt.Errorf("%s exceeds %d-byte limit", field, limit)
	}
	return nil
}

func normalizeOptionalText(field, value string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be valid UTF-8", field)
	}
	if len(value) > limit {
		return "", fmt.Errorf("%s exceeds %d-byte limit", field, limit)
	}
	return value, nil
}

func boundUTF8(value string, limit int) string {
	if limit <= 0 || value == "" {
		return ""
	}
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "")
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
