package semantic

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	MaxQueryBytes         = 4 << 10
	MaxCandidateNoteBytes = 4 << 10
	MaxRerankCandidates   = 50
	NativeOversample      = 3
)

type MemoryCandidate struct {
	Scope string
	Key   string
	Note  string
}

type RerankMetadata struct {
	Used         bool
	Fallback     bool
	Provider     string
	Model        string
	Candidates   int
	InputTokens  int
	OutputTokens int
}

func RerankMemory(ctx context.Context, provider Provider, consumerID, query string, candidates []MemoryCandidate) ([]int, RerankMetadata) {
	native := nativeOrder(len(candidates))
	meta := RerankMetadata{Candidates: len(candidates)}
	consumerID = strings.TrimSpace(consumerID)
	query = strings.TrimSpace(query)
	if provider == nil || consumerID == "" || query == "" || len(candidates) == 0 || len(candidates) > MaxRerankCandidates {
		return native, meta
	}
	stateCandidates := make([]map[string]any, len(candidates))
	questions := make(map[string]Question, len(candidates))
	for index, candidate := range candidates {
		stateCandidates[index] = map[string]any{
			"scope": strings.TrimSpace(candidate.Scope),
			"key":   strings.TrimSpace(candidate.Key),
			"note":  truncateRerankText(tracepkg.SanitizeText(strings.TrimSpace(candidate.Note)), MaxCandidateNoteBytes),
		}
		questions[fmt.Sprintf("relevance_%d", index)] = Question{
			Type:         PrimitiveNoul,
			Instructions: fmt.Sprintf("Is candidates[%d] relevant and useful for answering the memory query?", index),
			Noul: &NoulCriteria{
				True:  "The candidate directly helps satisfy, clarify, or constrain the query.",
				False: "The candidate is unrelated, only shares incidental words, or would not help answer the query.",
			},
		}
	}
	request := Request{
		Consumer: Consumer{ID: consumerID, Purpose: "memory_rerank"},
		State: map[string]any{
			"query":      truncateRerankText(tracepkg.SanitizeText(query), MaxQueryBytes),
			"candidates": stateCandidates,
		},
		Questions: questions,
	}
	if err := ValidateRequest(request); err != nil {
		meta.Fallback = true
		return native, meta
	}
	result, err := provider.Evaluate(ctx, request)
	if err != nil {
		meta.Fallback = true
		return native, meta
	}
	probabilities := make([]float64, len(candidates))
	for index := range candidates {
		answer, ok := result.Answers[fmt.Sprintf("relevance_%d", index)]
		if !ok || answer.Type != PrimitiveNoul || answer.Noul == nil || answer.Noul.ProbabilityYes < 0 || answer.Noul.ProbabilityYes > 1 {
			meta.Fallback = true
			return native, meta
		}
		probabilities[index] = answer.Noul.ProbabilityYes
	}
	order := append([]int(nil), native...)
	sort.SliceStable(order, func(i, j int) bool {
		return probabilities[order[i]] > probabilities[order[j]]
	})
	meta.Used = true
	meta.Provider = result.Provider
	meta.Model = result.Model
	if result.Usage != nil {
		meta.InputTokens = result.Usage.InputTokens
		meta.OutputTokens = result.Usage.OutputTokens
	}
	return order, meta
}

func nativeOrder(count int) []int {
	order := make([]int, count)
	for index := range order {
		order[index] = index
	}
	return order
}

func truncateRerankText(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
