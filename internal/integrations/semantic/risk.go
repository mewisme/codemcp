package semantic

import "context"

type RiskClass string

const (
	RiskLow      RiskClass = "low"
	RiskMedium   RiskClass = "medium"
	RiskHigh     RiskClass = "high"
	RiskCritical RiskClass = "critical"
)

type CanonicalInvocation struct {
	Operation string `json:"operation"`
	Tool      string `json:"tool,omitempty"`
	Arguments any    `json:"arguments,omitempty"`
}

type RiskInput struct {
	Consumer    Consumer            `json:"consumer"`
	WorkspaceID string              `json:"workspace_id,omitempty"`
	CallerID    string              `json:"caller_id,omitempty"`
	Invocation  CanonicalInvocation `json:"invocation"`
}

type RiskAssessment struct {
	Class      RiskClass        `json:"class"`
	Confidence float64          `json:"confidence"`
	Category   string           `json:"category,omitempty"`
	Reason     string           `json:"reason,omitempty"`
	Provider   ProviderMetadata `json:"provider"`
}

// RiskClassifier only returns evidence. Approval policy, challenge issuance,
// grants, retries, and side effects remain outside this interface.
type RiskClassifier interface {
	ClassifyRisk(context.Context, RiskInput) (RiskAssessment, error)
}

type RiskClassifierFunc func(context.Context, RiskInput) (RiskAssessment, error)

func (fn RiskClassifierFunc) ClassifyRisk(ctx context.Context, input RiskInput) (RiskAssessment, error) {
	return fn(ctx, input)
}
