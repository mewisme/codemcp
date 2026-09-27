package typesafe

import "time"

const (
	DefaultBaseURL          = "https://api.typesafe.ai"
	SystemOnePath           = "/v1/systemone"
	ModelsPath              = "/v1/models"
	DefaultModel            = "jev-latest"
	VerifiedStableModel     = "jev-1.13.0"
	MaxChoiceOptions        = 255
	MaxScoreLevels          = 10
	MaxContextTokens        = 64_000
	MaxStateLongestQTokens  = 32_000
	VerifiedRequestsPerMin  = 1_200
	VerifiedTokensPerSecond = 250_000
)

const VerifiedAt = "2026-09-27"

var SDKDefaultTimeout = 10 * time.Second

type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type SystemOneRequest struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

type SystemOneResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

type ModelCard struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`
}

type ModelsResponse struct {
	Models []ModelCard `json:"models"`
}

// RiskClassifierAvailable reports that CodeMCP has a tested System One Choice
// adapter for the provider-neutral command/process risk contract.
const RiskClassifierAvailable = true
