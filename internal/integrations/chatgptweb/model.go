package chatgptweb

import "time"

const (
	TemporaryChatURL      = "https://chatgpt.com/?temporary-chat=true"
	DefaultConnectorName  = "CodeMCP"
	DefaultMaxAgents      = 5
	AuthMarkerVersion     = 1
	DefaultMaxPromptBytes = 256 * 1024
)

type State string

const (
	StateDisabled             State = "disabled"
	StateBrowserUnavailable   State = "browser_unavailable"
	StateNeedsLogin           State = "needs_login"
	StateConnectorUnavailable State = "connector_unavailable"
	StateReady                State = "ready"
	StateDegraded             State = "degraded"
)

type AuthEvidence struct {
	OriginOK      bool `json:"origin_ok"`
	TemporaryChat bool `json:"temporary_chat"`
	Authenticated bool `json:"authenticated"`
	Composer      bool `json:"composer"`
}

func (evidence AuthEvidence) Ready() bool {
	return evidence.OriginOK && evidence.TemporaryChat && evidence.Authenticated && evidence.Composer
}

type AuthMarker struct {
	Version    int       `json:"version"`
	VerifiedAt time.Time `json:"verified_at"`
}

type TurnState string

const (
	TurnPreparing  TurnState = "preparing"
	TurnSubmitting TurnState = "submitting"
	TurnGenerating TurnState = "generating"
	TurnTool       TurnState = "tool"
	TurnFinal      TurnState = "final"
	TurnCancelled  TurnState = "cancelled"
)

type TurnRequest struct {
	Bootstrap        string
	Prompt           string
	Model            string
	ReasoningEffort  string
	ConnectorName    string
	RequireConnector bool
}

type TurnResult struct {
	State        TurnState
	Text         string
	ToolObserved bool
	Model        string
	Effort       string
	Connector    string
}
