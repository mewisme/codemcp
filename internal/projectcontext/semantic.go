package projectcontext

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	maxSemanticQueryBytes     = 4 << 10
	maxSemanticCandidateBytes = 4 << 10
	maxSemanticCandidates     = 6
)

type optionalContextCandidate struct {
	Title   string
	Content string
}

func rankOptionalContext(ctx context.Context, provider semantic.Provider, query string, value instructioncontext.InstructionContext) ([]string, SemanticSummary) {
	candidates := optionalContextCandidates(value)
	summary := SemanticSummary{Candidates: len(candidates)}
	if provider == nil || strings.TrimSpace(query) == "" || len(candidates) == 0 || len(candidates) > maxSemanticCandidates {
		return nil, summary
	}
	stateCandidates := make([]map[string]any, len(candidates))
	questions := make(map[string]semantic.Question, len(candidates))
	for index, candidate := range candidates {
		stateCandidates[index] = map[string]any{
			"title":   candidate.Title,
			"content": truncateSemanticText(tracepkg.SanitizeText(candidate.Content), maxSemanticCandidateBytes),
		}
		questions[fmt.Sprintf("relevance_%d", index)] = semantic.Question{
			Type:         semantic.PrimitiveNoul,
			Instructions: fmt.Sprintf("Is candidates[%d] useful for the current project-context query?", index),
		}
	}
	request := semantic.Request{
		Consumer: semantic.Consumer{ID: "project_context", Purpose: "context_ranking"},
		State: map[string]any{
			"query":      truncateSemanticText(tracepkg.SanitizeText(strings.TrimSpace(query)), maxSemanticQueryBytes),
			"candidates": stateCandidates,
		},
		Questions: questions,
	}
	if err := semantic.ValidateRequest(request); err != nil {
		summary.Fallback = true
		return nil, summary
	}
	result, err := provider.Evaluate(ctx, request)
	if err != nil {
		summary.Fallback = true
		return nil, summary
	}
	probabilities := make([]float64, len(candidates))
	for index := range candidates {
		answer, ok := result.Answers[fmt.Sprintf("relevance_%d", index)]
		if !ok || answer.Type != semantic.PrimitiveNoul || answer.Noul == nil || answer.Noul.ProbabilityYes < 0 || answer.Noul.ProbabilityYes > 1 {
			summary.Fallback = true
			return nil, summary
		}
		probabilities[index] = answer.Noul.ProbabilityYes
	}
	order := make([]int, len(candidates))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		return probabilities[order[i]] > probabilities[order[j]]
	})
	priority := make([]string, len(order))
	for index, candidateIndex := range order {
		priority[index] = candidates[candidateIndex].Title
	}
	summary.Used = true
	summary.Provider = result.Provider
	summary.Model = result.Model
	if result.Usage != nil {
		summary.InputTokens = result.Usage.InputTokens
		summary.OutputTokens = result.Usage.OutputTokens
	}
	return priority, summary
}

func optionalContextCandidates(value instructioncontext.InstructionContext) []optionalContextCandidate {
	result := make([]optionalContextCandidate, 0, maxSemanticCandidates)
	appendCandidate := func(title, content string) {
		content = strings.TrimSpace(content)
		if content != "" {
			result = append(result, optionalContextCandidate{Title: title, Content: content})
		}
	}
	if !value.Git.Skipped {
		appendCandidate("Git", strings.Join(append([]string{value.Git.Branch, value.Git.StatusShort}, value.Git.RecentCommits...), "\n"))
	}
	if value.AutoMemory.Loaded {
		appendCandidate("Auto memory", value.AutoMemory.Content)
	}
	var user, project strings.Builder
	for _, section := range value.ProjectMemory.Sections {
		switch section.Kind {
		case instructioncontext.SectionUser:
			user.WriteString(section.Content)
			user.WriteByte('\n')
		case instructioncontext.SectionProject:
			project.WriteString(section.Content)
			project.WriteByte('\n')
		}
	}
	appendCandidate("User instructions", user.String())
	appendCandidate("Project instructions", project.String())
	var skills strings.Builder
	for _, skill := range value.Skills {
		skills.WriteString(skill.Name)
		skills.WriteString(": ")
		skills.WriteString(skill.Description)
		skills.WriteByte('\n')
	}
	appendCandidate("Skills", skills.String())
	return result
}

func truncateSemanticText(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
