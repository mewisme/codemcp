package semantic

import (
	"context"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type fallbackReason string

const (
	fallbackProviderError   fallbackReason = "provider_error"
	fallbackInvalidRequest  fallbackReason = "invalid_request"
	fallbackInvalidResponse fallbackReason = "invalid_response"
)

type primitiveCounts struct {
	Noul   int
	Choice int
	Score  int
}

func emitEvaluation(ctx context.Context, observer tracepkg.Observer, request Request, provider string, result Result, err error, duration time.Duration) {
	counts := countPrimitives(request)
	fields := []tracepkg.Field{
		tracepkg.String("consumer", request.Consumer.ID),
		tracepkg.String("provider", provider),
		tracepkg.Int("questions", len(request.Questions)),
		tracepkg.Int("noul_questions", counts.Noul),
		tracepkg.Int("choice_questions", counts.Choice),
		tracepkg.Int("score_questions", counts.Score),
		tracepkg.DurationMS("duration_ms", duration),
		tracepkg.Bool("cache_hit", err == nil && result.Runtime.CacheHit),
		tracepkg.Int("attempts", result.Runtime.Attempts),
	}
	if category := semanticErrorCategory(err); category != "" {
		fields = append(fields, tracepkg.String("error_category", string(category)))
	}
	if result.Usage != nil {
		fields = append(fields,
			tracepkg.Int("input_tokens", result.Usage.InputTokens),
			tracepkg.Int("output_tokens", result.Usage.OutputTokens),
		)
	}
	safeTraceEmit(ctx, observer, "SEMANTIC", "semantic.evaluation.completed", "Semantic evaluation completed", fields...)
}

func emitConsumerOutcome(ctx context.Context, observer tracepkg.Observer, consumerID string, fallback bool, reason fallbackReason, category ErrorCategory) {
	fields := []tracepkg.Field{
		tracepkg.String("consumer", consumerID),
		tracepkg.Bool("fallback", fallback),
	}
	if reason != "" {
		fields = append(fields, tracepkg.String("fallback_reason", string(reason)))
	}
	if category != "" {
		fields = append(fields, tracepkg.String("error_category", string(category)))
	}
	safeTraceEmit(ctx, observer, "SEMANTIC", "semantic.consumer.completed", "Semantic consumer completed", fields...)
}

func emitRiskClassification(ctx context.Context, observer tracepkg.Observer, consumerID, provider string, assessment RiskAssessment, err error, duration time.Duration) {
	fields := []tracepkg.Field{
		tracepkg.String("consumer", consumerID),
		tracepkg.String("provider", provider),
		tracepkg.DurationMS("duration_ms", duration),
	}
	if err == nil {
		fields = append(fields,
			tracepkg.String("risk_class", string(assessment.Class)),
			tracepkg.String("risk_category", assessment.Category),
		)
	}
	if category := semanticErrorCategory(err); category != "" {
		fields = append(fields, tracepkg.String("error_category", string(category)))
	}
	safeTraceEmit(ctx, observer, "SEMANTIC", "semantic.risk.completed", "Semantic risk classification completed", fields...)
}

func fallbackReasonForError(err error) fallbackReason {
	switch semanticErrorCategory(err) {
	case ErrorInvalidRequest:
		return fallbackInvalidRequest
	case ErrorInvalidResponse:
		return fallbackInvalidResponse
	default:
		return fallbackProviderError
	}
}

func countPrimitives(request Request) primitiveCounts {
	var counts primitiveCounts
	for _, question := range request.Questions {
		switch question.Type {
		case PrimitiveNoul:
			counts.Noul++
		case PrimitiveChoice:
			counts.Choice++
		case PrimitiveScore:
			counts.Score++
		}
	}
	return counts
}

func semanticErrorCategory(err error) ErrorCategory {
	if err == nil {
		return ""
	}
	if value, ok := AsError(err); ok {
		return value.Category
	}
	return ErrorProvider
}

func safeTraceEmit(ctx context.Context, observer tracepkg.Observer, component, name, message string, fields ...tracepkg.Field) {
	defer func() { _ = recover() }()
	if contextual := tracepkg.ObserverFromContext(ctx); contextual != nil {
		tracepkg.EmitObserver(contextual, component, name, message, fields...)
		return
	}
	tracepkg.EmitObserver(observer, component, name, message, fields...)
}
