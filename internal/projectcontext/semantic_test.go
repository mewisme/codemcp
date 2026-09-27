package projectcontext

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
)

func TestRankOptionalContextBoundsPayloadAndReturnsSafeUsage(t *testing.T) {
	var captured semantic.Request
	provider := semantic.ProviderFunc(func(_ context.Context, request semantic.Request) (semantic.Result, error) {
		captured = request
		answers := make(map[string]semantic.Answer, len(request.Questions))
		for id := range request.Questions {
			answers[id] = semantic.Answer{Type: semantic.PrimitiveNoul, Noul: &semantic.NoulAnswer{ProbabilityYes: 0.7}}
		}
		return semantic.Result{
			Answers: answers,
			ProviderMetadata: semantic.ProviderMetadata{
				Provider: "fake", Model: "fixture", Duration: time.Millisecond,
				Usage: &semantic.Usage{InputTokens: 17, OutputTokens: 6},
			},
		}, nil
	})
	value := instructioncontext.InstructionContext{
		Git:           instructioncontext.GitSnapshot{Branch: "main", StatusShort: strings.Repeat("g", 8000)},
		AutoMemory:    instructioncontext.AutoMemorySnapshot{Loaded: true, Content: "api_key=PRIVATE_SEMANTIC_KEY " + strings.Repeat("m", 8000)},
		GlobalContext: strings.Repeat("c", 8000),
		ProjectMemory: instructioncontext.ProjectMemoryBundle{Sections: []instructioncontext.Section{
			{Kind: instructioncontext.SectionUser, Content: strings.Repeat("u", 8000)},
			{Kind: instructioncontext.SectionProject, Content: strings.Repeat("p", 8000)},
		}},
		Skills: []skills.Skill{{Name: "skill", Description: strings.Repeat("s", 8000)}},
	}
	priority, summary := rankOptionalContext(t.Context(), provider, strings.Repeat("q", 8000), value)
	if !summary.Used || summary.Fallback || summary.Provider != "fake" || summary.Model != "fixture" ||
		summary.Candidates != maxSemanticCandidates || summary.InputTokens != 17 || summary.OutputTokens != 6 {
		t.Fatalf("summary=%#v priority=%v", summary, priority)
	}
	state, ok := captured.State.(map[string]any)
	if !ok {
		t.Fatalf("state=%T", captured.State)
	}
	if query, _ := state["query"].(string); len(query) > maxSemanticQueryBytes {
		t.Fatalf("query bytes=%d", len(query))
	}
	candidates, ok := state["candidates"].([]map[string]any)
	if !ok || len(candidates) != maxSemanticCandidates {
		t.Fatalf("candidates=%T %#v", state["candidates"], state["candidates"])
	}
	for _, candidate := range candidates {
		if content, _ := candidate["content"].(string); len(content) > maxSemanticCandidateBytes {
			t.Fatalf("candidate bytes=%d", len(content))
		} else if strings.Contains(content, "PRIVATE_SEMANTIC_KEY") {
			t.Fatalf("candidate leaked credential: %q", content)
		}
	}
}

func TestSemanticPriorityCannotDisplaceRulesOrRequiredContext(t *testing.T) {
	value := instructioncontext.InstructionContext{
		ToolProfile:   instructioncontext.ToolProfile{Name: "full", Count: 2},
		AgentWorkflow: "MANDATORY_WORKFLOW",
		Environment: instructioncontext.EnvironmentSnapshot{
			Platform: "linux", OS: "linux", Arch: "amd64", Go: "go", PID: 1,
			WorkspaceID: "ws", WorkspaceRoot: "/workspace", CWD: "/workspace",
			EffectiveRoots: []string{"/workspace"},
		},
		Git: instructioncontext.GitSnapshot{IsRepo: true, Root: "/workspace", Branch: "main", StatusShort: strings.Repeat("G", 900)},
		ProjectMemory: instructioncontext.ProjectMemoryBundle{Sections: []instructioncontext.Section{{
			Path: "/workspace/AGENTS.md", Kind: instructioncontext.SectionProject, Source: "agents",
			Content: strings.Repeat("P", 900),
		}}},
		GlobalRules:             []rules.Rule{{Path: "managed://rule", Source: "CodeMCP", AlwaysApply: true, Content: "SECURITY_RULE_REQUIRED"}},
		IntegrationInstructions: []instructioncontext.IntegrationInstruction{{ID: "Guard", Source: "test", Content: "INTEGRATION_REQUIRED"}},
	}
	if err := instructioncontext.ApplyFormattedInstructionsLimitWithPriority(&value, 3000, []string{"Project instructions", "Git"}); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"MANDATORY_WORKFLOW", "SECURITY_RULE_REQUIRED", "INTEGRATION_REQUIRED", "## Quick pointers"} {
		if !strings.Contains(value.InstructionsText, required) {
			t.Fatalf("required context %q missing:\n%s", required, value.InstructionsText)
		}
	}
	if value.InstructionBytes > 3000 {
		t.Fatalf("instruction bytes=%d", value.InstructionBytes)
	}
}

func TestSemanticFailureLeavesDeterministicProjectContextSelection(t *testing.T) {
	value := instructioncontext.InstructionContext{
		ToolProfile:   instructioncontext.ToolProfile{Name: "full", Count: 1},
		AgentWorkflow: "workflow",
		Environment: instructioncontext.EnvironmentSnapshot{
			Platform: "linux", OS: "linux", Arch: "amd64", Go: "go", PID: 1,
			WorkspaceID: "ws", WorkspaceRoot: "/workspace", CWD: "/workspace",
			EffectiveRoots: []string{"/workspace"},
		},
		Git:           instructioncontext.GitSnapshot{IsRepo: true, Root: "/workspace", Branch: "main"},
		GlobalContext: "global optional",
		ProjectMemory: instructioncontext.ProjectMemoryBundle{Sections: []instructioncontext.Section{{
			Path: "/workspace/AGENTS.md", Kind: instructioncontext.SectionProject, Content: "project optional",
		}}},
	}
	if err := instructioncontext.ApplyFormattedInstructionsLimit(&value, 100000); err != nil {
		t.Fatal(err)
	}
	before := value.InstructionsText
	provider := semantic.ProviderFunc(func(context.Context, semantic.Request) (semantic.Result, error) {
		return semantic.Result{}, semantic.NewError(semantic.ErrorUnavailable, "private")
	})
	priority, summary := rankOptionalContext(t.Context(), provider, "query", value)
	if len(priority) != 0 || summary.Used || !summary.Fallback {
		t.Fatalf("priority=%v summary=%#v", priority, summary)
	}
	if err := instructioncontext.ApplyFormattedInstructionsLimitWithPriority(&value, 100000, priority); err != nil {
		t.Fatal(err)
	}
	if value.InstructionsText != before {
		t.Fatalf("fallback changed deterministic context:\nBEFORE:\n%s\nAFTER:\n%s", before, value.InstructionsText)
	}
}

func TestSemanticRateLimitAndTimeoutKeepNativeProjectContext(t *testing.T) {
	value := instructioncontext.InstructionContext{GlobalContext: "native global context"}
	for _, category := range []semantic.ErrorCategory{semantic.ErrorRateLimited, semantic.ErrorTimeout} {
		t.Run(string(category), func(t *testing.T) {
			provider := semantic.ProviderFunc(func(context.Context, semantic.Request) (semantic.Result, error) {
				return semantic.Result{}, semantic.NewError(category, "provider-private-detail")
			})
			priority, summary := rankOptionalContext(t.Context(), provider, "query", value)
			if len(priority) != 0 || summary.Used || !summary.Fallback {
				t.Fatalf("priority=%v summary=%#v", priority, summary)
			}
		})
	}
}

func TestRankOptionalContextNeverIncludesRulesAsCandidates(t *testing.T) {
	var captured semantic.Request
	provider := semantic.ProviderFunc(func(_ context.Context, request semantic.Request) (semantic.Result, error) {
		captured = request
		answers := map[string]semantic.Answer{}
		for id := range request.Questions {
			answers[id] = semantic.Answer{Type: semantic.PrimitiveNoul, Noul: &semantic.NoulAnswer{ProbabilityYes: 1}}
		}
		return semantic.Result{Answers: answers, ProviderMetadata: semantic.ProviderMetadata{Provider: "fake"}}, nil
	})
	value := instructioncontext.InstructionContext{
		GlobalContext: "ordinary context",
		GlobalRules:   []rules.Rule{{Path: "managed://security", Content: "SECRET_SECURITY_POLICY"}},
		Rules:         []rules.Rule{{Path: "/workspace/.cm/rules/security.md", Content: "WORKSPACE_SECURITY_POLICY"}},
	}
	if _, summary := rankOptionalContext(t.Context(), provider, "query", value); !summary.Used {
		t.Fatalf("summary=%#v", summary)
	}
	text := toString(captured.State)
	if strings.Contains(text, "SECRET_SECURITY_POLICY") || strings.Contains(text, "WORKSPACE_SECURITY_POLICY") {
		t.Fatalf("rules leaked into semantic candidate state: %s", text)
	}
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		var builder strings.Builder
		for key, child := range typed {
			builder.WriteString(key)
			builder.WriteString(toString(child))
		}
		return builder.String()
	case []map[string]any:
		var builder strings.Builder
		for _, child := range typed {
			builder.WriteString(toString(child))
		}
		return builder.String()
	default:
		return ""
	}
}
