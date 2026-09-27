package semantic

import (
	"context"
	"math"
	"time"
)

const (
	MaxStateBytes        = 64 << 10
	MaxQuestions         = 50
	MaxQuestionIDBytes   = 128
	MaxCriteriaEntries   = 255
	MaxScoreLevels       = 10
	MaxConsumerIDBytes   = 128
	MaxPurposeBytes      = 128
	MaxProviderIDBytes   = 128
	MaxModelIDBytes      = 128
	MaxRiskCategoryBytes = 128
	MaxRiskReasonBytes   = 512
	MaxRiskInputBytes    = 64 << 10
	ProbabilityEpsilon   = 0.001
)

type Primitive string

const (
	PrimitiveNoul   Primitive = "noul"
	PrimitiveChoice Primitive = "choice"
	PrimitiveScore  Primitive = "score"
)

type Consumer struct {
	ID      string `json:"id"`
	Purpose string `json:"purpose"`
}

type NoulCriteria struct {
	True  any `json:"true,omitempty"`
	False any `json:"false,omitempty"`
}

type ScoreLevel struct {
	Key         string  `json:"key"`
	Value       float64 `json:"value"`
	Description any     `json:"description"`
}

type Question struct {
	Type         Primitive      `json:"type"`
	Instructions any            `json:"instructions"`
	Noul         *NoulCriteria  `json:"noul,omitempty"`
	Choice       map[string]any `json:"choice,omitempty"`
	Score        []ScoreLevel   `json:"score,omitempty"`
}

// Batch is a set of independent semantic questions evaluated against the same
// state. Providers may execute the questions together when their protocol
// supports batching, but answers remain independent and keyed by question ID.
type Batch struct {
	Consumer  Consumer            `json:"consumer"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Request is the canonical provider-neutral semantic request.
type Request = Batch

type NoulDecision string

const (
	NoulNo        NoulDecision = "no"
	NoulYes       NoulDecision = "yes"
	NoulUncertain NoulDecision = "uncertain"
)

type NoulAnswer struct {
	ProbabilityYes float64 `json:"probability_yes"`
}

// Confidence reports distance from an even yes/no split on a 0..1 scale.
func (answer NoulAnswer) Confidence() float64 {
	if answer.ProbabilityYes < 0 || answer.ProbabilityYes > 1 || math.IsNaN(answer.ProbabilityYes) || math.IsInf(answer.ProbabilityYes, 0) {
		return 0
	}
	return math.Abs(answer.ProbabilityYes*2 - 1)
}

// Decision converts the probability into yes/no/uncertain using a caller-owned
// minimum confidence. Exact 0.5 is always uncertain.
func (answer NoulAnswer) Decision(minConfidence float64) NoulDecision {
	if answer.ProbabilityYes == 0.5 || answer.Confidence() < minConfidence {
		return NoulUncertain
	}
	if answer.ProbabilityYes > 0.5 {
		return NoulYes
	}
	return NoulNo
}

type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type ScoreAnswer struct {
	Score         float64            `json:"score"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type Answer struct {
	Type   Primitive     `json:"type"`
	Noul   *NoulAnswer   `json:"noul,omitempty"`
	Choice *ChoiceAnswer `json:"choice,omitempty"`
	Score  *ScoreAnswer  `json:"score,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ProviderMetadata struct {
	Provider string        `json:"provider"`
	Model    string        `json:"model,omitempty"`
	Duration time.Duration `json:"duration"`
	Usage    *Usage        `json:"usage,omitempty"`
}

type Result struct {
	Answers map[string]Answer `json:"answers"`
	ProviderMetadata
}

// Provider is the only capability semantic consumers need. Concrete remote,
// native, and fake providers all implement the same bounded typed contract.
type Provider interface {
	Evaluate(context.Context, Request) (Result, error)
}

type ProviderFunc func(context.Context, Request) (Result, error)

func (fn ProviderFunc) Evaluate(ctx context.Context, request Request) (Result, error) {
	return fn(ctx, request)
}
