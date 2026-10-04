package approval

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mewis.me/codemcp/internal/controlguard"
)

const (
	DefaultChallengeTTL          = 30 * time.Second
	DefaultRequestTTL            = 60 * time.Second
	DefaultRetryTTL              = 30 * time.Second
	DefaultCLICapabilityTTL      = 15 * time.Second
	DefaultRuntimeGrantTTL       = time.Hour
	MaxRuntimeGrantTTL           = 24 * time.Hour
	DefaultPendingLimit          = 32
	DefaultWorkspacePendingLimit = 8
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusApproved  Status = "approved"
	StatusDenied    Status = "denied"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
	StatusConsumed  Status = "consumed"
)

var (
	ErrChallengeNotFound    = errors.New("approval challenge not found")
	ErrChallengeExpired     = errors.New("approval challenge expired")
	ErrChallengeMismatch    = errors.New("approval challenge binding mismatch")
	ErrChallengeCaller      = errors.New("approval challenge caller mismatch")
	ErrChallengeSession     = errors.New("approval challenge session mismatch")
	ErrChallengeWorkspace   = errors.New("approval challenge workspace mismatch")
	ErrChallengeSource      = errors.New("approval challenge source mismatch")
	ErrChallengeTool        = errors.New("approval challenge target tool mismatch")
	ErrChallengeArguments   = errors.New("approval challenge arguments mismatch")
	ErrRequestNotFound      = errors.New("approval request not found")
	ErrRequestAmbiguous     = errors.New("approval request prefix is ambiguous")
	ErrRequestResolved      = errors.New("approval request is already resolved")
	ErrRequestNotApproved   = errors.New("approval request is not approved")
	ErrSessionRequestActive = errors.New("MCP session already has an active approval request")
	ErrPendingLimit         = errors.New("approval pending request limit reached")
	ErrCapabilityNotFound   = errors.New("control approval capability not found")
	ErrCapabilityExpired    = errors.New("control approval capability expired")
	ErrRuntimeGrantNotFound = errors.New("runtime session grant not found")
)

type ChallengeInput struct {
	CallerID              string
	RequestCorrelationID  string
	SessionHash           string
	WorkspaceID           string
	Source                string
	TargetTool            string
	Arguments             map[string]any
	GuardCode             controlguard.Code
	GuardReason           string
	Title                 string
	Command               string
	SimilarCommandPattern string
	SuppressNotifications bool
}

type Challenge struct {
	ID                    string            `json:"id"`
	SessionHash           string            `json:"session_hash,omitempty"`
	WorkspaceID           string            `json:"workspace_id"`
	Source                string            `json:"source,omitempty"`
	TargetTool            string            `json:"target_tool"`
	Arguments             json.RawMessage   `json:"arguments"`
	Digest                string            `json:"-"`
	GuardCode             controlguard.Code `json:"guard_code"`
	GuardReason           string            `json:"guard_reason"`
	Title                 string            `json:"title"`
	Command               string            `json:"command,omitempty"`
	SimilarCommandPattern string            `json:"similar_command_pattern,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	ExpiresAt             time.Time         `json:"expires_at"`
	callerID              string
	requestCorrelationID  string
	requestID             string
	suppressNotifications bool
}

type ChallengeRequestInput struct {
	ChallengeID string
	CallerID    string
	SessionHash string
	WorkspaceID string
	Source      string
	TargetTool  string
	Arguments   map[string]any
	Title       string
}

type Request struct {
	ID                    string            `json:"id"`
	Status                Status            `json:"status"`
	WorkspaceID           string            `json:"workspace_id"`
	SessionHash           string            `json:"session_hash,omitempty"`
	Source                string            `json:"source,omitempty"`
	TargetTool            string            `json:"target_tool"`
	Arguments             json.RawMessage   `json:"arguments"`
	Digest                string            `json:"-"`
	GuardCode             controlguard.Code `json:"guard_code"`
	GuardReason           string            `json:"guard_reason"`
	Title                 string            `json:"title"`
	Command               string            `json:"command,omitempty"`
	SimilarCommandPattern string            `json:"similar_command_pattern,omitempty"`
	RuntimeSessionGrant   bool              `json:"runtime_session_grant,omitempty"`
	GrantExpiresAt        time.Time         `json:"grant_expires_at,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	ExpiresAt             time.Time         `json:"expires_at"`
	ResolvedAt            time.Time         `json:"resolved_at,omitempty"`
	ResolvedBy            string            `json:"resolved_by,omitempty"`
	Reason                string            `json:"reason,omitempty"`
	RetryUntil            time.Time         `json:"retry_until,omitempty"`
	ConsumedAt            time.Time         `json:"consumed_at,omitempty"`
	callerID              string
	challengeID           string
	suppressNotifications bool
}

type RetryInput struct {
	CallerID    string
	RequestID   string
	WorkspaceID string
	Source      string
	TargetTool  string
	Arguments   map[string]any
	Command     string
}

type CLIInvocation struct {
	Program string
	Args    []string
}

type CapabilityMismatchError struct {
	Expected []string
	Actual   []string
}

func (e *CapabilityMismatchError) Error() string {
	return "control approval capability does not match CLI arguments"
}

type Filter struct {
	WorkspaceID string
	Status      Status
}

type MismatchError struct {
	RequestID  string
	TargetTool string
	Expected   json.RawMessage
	Actual     json.RawMessage
}

func (e *MismatchError) Error() string {
	if e == nil {
		return "approval retry does not match approved arguments"
	}
	return fmt.Sprintf("approval retry does not match approved arguments for request %s", e.RequestID)
}
