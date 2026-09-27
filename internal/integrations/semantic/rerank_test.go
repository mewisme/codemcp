package semantic

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestRerankMemoryUsesStableBoundedSemanticOrder(t *testing.T) {
	var request Request
	provider := ProviderFunc(func(_ context.Context, value Request) (Result, error) {
		request = value
		return Result{
			Answers: map[string]Answer{
				"relevance_0": {Type: PrimitiveNoul, Noul: &NoulAnswer{ProbabilityYes: 0.2}},
				"relevance_1": {Type: PrimitiveNoul, Noul: &NoulAnswer{ProbabilityYes: 0.9}},
				"relevance_2": {Type: PrimitiveNoul, Noul: &NoulAnswer{ProbabilityYes: 0.9}},
			},
			ProviderMetadata: ProviderMetadata{Provider: "fake", Model: "fixture", Usage: &Usage{InputTokens: 12, OutputTokens: 3}},
		}, nil
	})
	order, meta := RerankMemory(t.Context(), provider, "memory_search", strings.Repeat("q", MaxQueryBytes+10), []MemoryCandidate{
		{Scope: "one", Note: "api_key=PRIVATE_VALUE " + strings.Repeat("a", MaxCandidateNoteBytes+10)},
		{Scope: "two", Key: "child", Note: "second"},
		{Scope: "three", Note: "third"},
	})
	if !meta.Used || meta.Fallback || !reflect.DeepEqual(order, []int{1, 2, 0}) || meta.Provider != "fake" || meta.Model != "fixture" {
		t.Fatalf("order=%v meta=%#v", order, meta)
	}
	state := request.State.(map[string]any)
	if query := state["query"].(string); len(query) > MaxQueryBytes {
		t.Fatalf("query bytes=%d", len(query))
	}
	values := state["candidates"].([]map[string]any)
	if len(values[0]["note"].(string)) > MaxCandidateNoteBytes || strings.Contains(values[0]["note"].(string), "PRIVATE_VALUE") {
		t.Fatalf("unsafe candidate=%q", values[0]["note"])
	}
}

func TestRerankMemoryFailsOpenToNativeOrder(t *testing.T) {
	candidates := []MemoryCandidate{{Scope: "one", Note: "first"}, {Scope: "two", Note: "second"}}
	provider := ProviderFunc(func(context.Context, Request) (Result, error) {
		return Result{}, NewError(ErrorUnavailable, "")
	})
	order, meta := RerankMemory(t.Context(), provider, "memory_search", "query", candidates)
	if meta.Used || !meta.Fallback || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatalf("order=%v meta=%#v", order, meta)
	}
}
