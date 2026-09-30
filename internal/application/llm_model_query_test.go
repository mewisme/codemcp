package application

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/llm"
)

type richModelBackend struct {
	models     []llm.Model
	enrichment llm.ModelEnrichmentResult
	discover   atomic.Int32
	enrich     atomic.Int32
}

func (backend *richModelBackend) Infer(_ context.Context, provider llm.Provider, _ llm.Request) (llm.Result, error) {
	return llm.Result{ProviderID: provider.ID, Model: provider.Model, Text: "OK"}, nil
}

func (backend *richModelBackend) DiscoverModels(context.Context, llm.Provider) ([]llm.Model, error) {
	backend.discover.Add(1)
	return cloneLLMModels(backend.models), nil
}

func (backend *richModelBackend) EnrichModels(context.Context, llm.Provider, llm.ModelEnrichmentRequest) (llm.ModelEnrichmentResult, error) {
	backend.enrich.Add(1)
	return cloneModelEnrichment(backend.enrichment), nil
}

func TestLLMModelQueryComposesFiltersSortsAndPaginationWithoutMutatingProviderState(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	backend := &richModelBackend{models: []llm.Model{
		{ID: "acme/alpha", Name: "Alpha", Author: "acme", ContextLength: 128000, ContextLengthKnown: true, PromptPrice: "0.000001", CompletionPrice: "0.000003", PricingKnown: true, FreeKnown: true, SupportedParameters: []string{"tools", "reasoning"}, Capabilities: []string{"tools", "reasoning"}, CapabilitiesKnown: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, ModalitiesKnown: true, CreatedAt: &created},
		{ID: "acme/beta", Name: "Beta", Author: "acme", ContextLength: 64000, ContextLengthKnown: true, PromptPrice: "0.0000005", CompletionPrice: "0.000002", PricingKnown: true, FreeKnown: true, SupportedParameters: []string{"tools"}, Capabilities: []string{"tools"}, CapabilitiesKnown: true, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, ModalitiesKnown: true, CreatedAt: &created},
		{ID: "other/gamma", Name: "Gamma", Author: "other", ContextLength: 128000, ContextLengthKnown: true, PromptPrice: "0", CompletionPrice: "0", PricingKnown: true, Free: true, FreeKnown: true, SupportedParameters: []string{"tools"}, Capabilities: []string{"tools"}, CapabilitiesKnown: true, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, ModalitiesKnown: true, CreatedAt: &created},
		{ID: "unknown/delta", Name: "Delta"},
	}}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.AddCustomProvider(t.Context(), "rich", CustomLLMProviderConfig{Protocol: llm.ProtocolOpenAI, BaseURL: "https://rich.example/v1", Model: "acme/alpha", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels}); err != nil {
		t.Fatal(err)
	}
	before, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	minimum := 60000
	page, err := service.ModelCatalog(t.Context(), "rich", LLMModelQuery{
		Authors:         []string{"ACME", "other"},
		MinContext:      &minimum,
		MaxPromptPrice:  "0.000001",
		Capabilities:    []string{"tools", "web-search"},
		InputModalities: []string{"image", "text"},
		Sort:            []LLMModelSort{{Field: "context", Direction: "desc"}, {Field: "prompt-price", Direction: "asc"}},
		Limit:           2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCatalog != 4 || page.Matched != 3 || page.Returned != 2 || !page.HasMore || page.Offset != 0 || page.Limit != 2 {
		t.Fatalf("page metadata=%#v", page)
	}
	if got := []string{page.Models[0].ID, page.Models[1].ID}; !reflect.DeepEqual(got, []string{"other/gamma", "acme/alpha"}) {
		t.Fatalf("sorted models=%v", got)
	}
	after, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("model query mutated provider state: before=%#v after=%#v", before, after)
	}
}

func TestLLMModelQueryStableTieBreakAndWindowModes(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &richModelBackend{models: []llm.Model{{ID: "z/model", Name: "Same"}, {ID: "a/model", Name: "Same"}, {ID: "m/model", Name: "Same"}}}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.AddCustomProvider(t.Context(), "simple", CustomLLMProviderConfig{Protocol: llm.ProtocolOpenAI, BaseURL: "https://simple.example/v1", Model: "a/model", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels}); err != nil {
		t.Fatal(err)
	}

	page, err := service.ModelCatalog(t.Context(), "simple", LLMModelQuery{Sort: []LLMModelSort{{Field: "name", Direction: "asc"}}, Range: &LLMModelRange{Start: 2, End: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{page.Models[0].ID, page.Models[1].ID}; !reflect.DeepEqual(got, []string{"m/model", "z/model"}) || page.Offset != 1 || page.Limit != 2 || page.HasMore {
		t.Fatalf("range page=%#v ids=%v", page, got)
	}
	count, err := service.ModelCatalog(t.Context(), "simple", LLMModelQuery{Search: "model", CountOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if count.Matched != 3 || count.Returned != 0 || len(count.Models) != 0 {
		t.Fatalf("count page=%#v", count)
	}
	all, err := service.ModelCatalog(t.Context(), "simple", LLMModelQuery{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if all.Returned != 3 || all.Limit != 3 || all.HasMore {
		t.Fatalf("all page=%#v", all)
	}

	invalid := []LLMModelQuery{
		{All: true, Limit: 1},
		{Range: &LLMModelRange{Start: 1, End: 2}, Offset: 1},
		{CountOnly: true, Sort: []LLMModelSort{{Field: "id", Direction: "asc"}}},
		{Rank: "usage", RecommendFor: "code-generation"},
		{Range: &LLMModelRange{Start: 0, End: 1}},
	}
	for index, query := range invalid {
		if _, err := service.ModelCatalog(t.Context(), "simple", query); !llm.IsCategory(err, llm.ErrorInvalidRequest) {
			t.Fatalf("invalid query %d err=%v", index, err)
		}
	}
}

func TestLLMModelQueryRejectsUnsupportedDimensionsInsteadOfGuessing(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &richModelBackend{models: []llm.Model{{ID: "plain/model", Name: "Plain"}}}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.AddCustomProvider(t.Context(), "plain", CustomLLMProviderConfig{Protocol: llm.ProtocolOpenAI, BaseURL: "https://plain.example/v1", Model: "plain/model", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels}); err != nil {
		t.Fatal(err)
	}
	free := true
	for _, query := range []LLMModelQuery{
		{Free: &free},
		{MinPromptPrice: "0"},
		{Capabilities: []string{"tools"}},
		{Sort: []LLMModelSort{{Field: "context", Direction: "desc"}}},
		{Rank: "usage"},
		{RecommendFor: "code-generation"},
	} {
		if _, err := service.ModelCatalog(t.Context(), "plain", query); !llm.IsCategory(err, llm.ErrorUnsupported) {
			t.Fatalf("unsupported query %#v err=%v", query, err)
		}
	}
}

func TestLLMModelQueryOpenRouterRankAndRecommendationUseSeparateBoundedCache(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &richModelBackend{
		models: []llm.Model{{ID: "vendor/a", Name: "A"}, {ID: "vendor/b", Name: "B"}, {ID: "vendor/c", Name: "C"}},
		enrichment: llm.ModelEnrichmentResult{
			Ranks:      map[string]llm.ModelRankMetadata{"vendor/b": {Position: 1, Kind: "usage", Source: "OpenRouter rankings", Basis: "adoption by tokens", Window: "week", Freshness: "2026-09-29"}, "vendor/a": {Position: 2, Kind: "usage", Source: "OpenRouter rankings", Basis: "adoption by tokens", Window: "week", Freshness: "2026-09-29"}},
			RankSource: "OpenRouter rankings", RankWindow: "week", RankBasis: "adoption by tokens", RankFreshness: "2026-09-29",
		},
	}
	service := NewLLMServiceWithBackend(root, backend)
	if err := service.SetCredential(t.Context(), string(llm.OpenRouterID), "test-openrouter-key"); err != nil {
		t.Fatal(err)
	}
	first, err := service.ModelCatalog(t.Context(), string(llm.OpenRouterID), LLMModelQuery{Rank: "usage", RankWindow: "week", All: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ModelCatalog(t.Context(), string(llm.OpenRouterID), LLMModelQuery{Rank: "usage", RankWindow: "week", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if backend.discover.Load() != 1 || backend.enrich.Load() != 1 {
		t.Fatalf("cache calls discover=%d enrich=%d", backend.discover.Load(), backend.enrich.Load())
	}
	if first.RankBasis != "adoption by tokens" || strings.Contains(strings.ToLower(first.RankBasis), "quality") || first.Models[0].ID != "vendor/b" || second.Models[0].ID != "vendor/b" {
		t.Fatalf("rank pages first=%#v second=%#v", first, second)
	}

	backend.enrichment = llm.ModelEnrichmentResult{
		Recommendations:      map[string]llm.ModelRecommendation{"vendor/a": {Position: 1, Task: "code-generation", Source: "OpenRouter task classifications", Basis: "request share for Code Generation", Freshness: "2026-09-29", Share: 0.42}},
		RecommendationSource: "OpenRouter task classifications", RecommendationBasis: "request share for Code Generation", RecommendationFreshness: "2026-09-29",
	}
	recommended, err := service.ModelCatalog(t.Context(), string(llm.OpenRouterID), LLMModelQuery{RecommendFor: "Code Generation", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if backend.enrich.Load() != 2 || recommended.Models[0].Recommendation == nil || recommended.Models[0].Recommendation.Task != "code-generation" || recommended.RecommendationSource == "" || recommended.RecommendationBasis == "" {
		t.Fatalf("recommendation page=%#v enrich=%d", recommended, backend.enrich.Load())
	}
}

func TestLLMModelQueryOllamaNativeFiltersAndSorts(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	oneB, twoB, sizeA, sizeB := int64(1_000_000_000), int64(2_000_000_000), int64(3_000), int64(1_000)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(24 * time.Hour)
	backend := &richModelBackend{models: []llm.Model{
		{ID: "zeta:latest", Name: "Zeta", ModifiedAt: &newer, Ollama: &llm.OllamaModelMetadata{Family: "llama", Families: []string{"llama", "bert"}, Format: "gguf", QuantizationLevel: "Q4_K_M", ParameterSize: "2B", ParameterCount: &twoB, SizeBytes: &sizeB}},
		{ID: "alpha:latest", Name: "Alpha", ModifiedAt: &old, Ollama: &llm.OllamaModelMetadata{Family: "llama", Format: "gguf", QuantizationLevel: "Q8_0", ParameterSize: "1B", ParameterCount: &oneB, SizeBytes: &sizeA}},
	}}
	service := NewLLMServiceWithBackend(root, backend)
	page, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{
		Ollama: LLMOllamaModelQuery{Families: []string{"llama"}, Formats: []string{"GGUF"}, MinParameterCount: &oneB},
		Sort:   []LLMModelSort{{Field: "parameter-size", Direction: "desc"}, {Field: "size", Direction: "asc"}}, All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Matched != 2 || len(page.Models) != 2 || page.Models[0].ID != "zeta:latest" || page.Models[0].Ollama == nil || page.Models[0].Ollama.QuantizationLevel != "Q4_K_M" {
		t.Fatalf("Ollama page=%#v", page)
	}

	maxSize := int64(1500)
	quantized, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{
		Ollama: LLMOllamaModelQuery{Quantizations: []string{"q4_k_m", "q8_0"}, MaxSizeBytes: &maxSize}, All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if quantized.Matched != 1 || len(quantized.Models) != 1 || quantized.Models[0].ID != "zeta:latest" {
		t.Fatalf("Ollama quantization/size filter=%#v", quantized)
	}

	modifiedAfter := old.Add(12 * time.Hour)
	modified, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{
		ModifiedAfter: &modifiedAfter,
		Sort:          []LLMModelSort{{Field: "modified", Direction: "desc"}}, All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if modified.Matched != 1 || len(modified.Models) != 1 || modified.Models[0].ID != "zeta:latest" {
		t.Fatalf("Ollama modified filter=%#v", modified)
	}

	bySize, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{Sort: []LLMModelSort{{Field: "size", Direction: "asc"}}, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bySize.Models) != 2 || bySize.Models[0].ID != "zeta:latest" || bySize.Models[1].ID != "alpha:latest" {
		t.Fatalf("Ollama byte-size sort=%#v", bySize)
	}
}
