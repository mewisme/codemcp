package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouterUsageAndTrendingRanksAreLabeledAsAdoption(t *testing.T) {
	const credential = "openrouter-ranking-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/datasets/rankings-daily" || r.Method != http.MethodGet {
			t.Fatalf("request=%s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("authorization=%q", got)
		}
		_, _ = fmt.Fprint(w, `{
			"data":[
				{"date":"2026-09-03","model_permaslug":"vendor/a","total_tokens":"1000000"},
				{"date":"2026-09-10","model_permaslug":"vendor/a","total_tokens":"2000000"},
				{"date":"2026-09-10","model_permaslug":"vendor/b","total_tokens":"500000"},
				{"date":"2026-09-10","model_permaslug":"other","total_tokens":"9000000"}
			],
			"meta":{"as_of":"2026-09-15T02:00:00Z","start_date":"2026-08-16","end_date":"2026-09-14"}
		}`)
	}))
	defer server.Close()

	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	provider := DefaultOpenRouter()
	provider.BaseURL = server.URL + "/api/v1"

	usage, err := client.EnrichModels(t.Context(), provider, ModelEnrichmentRequest{Rank: OpenRouterRankUsage, RankWindow: OpenRouterRankWindowWeek})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Ranks["vendor/a"].Position != 1 || usage.Ranks["vendor/a"].Value != "2000000" || usage.RankWindow != "week" {
		t.Fatalf("usage=%#v", usage)
	}
	if !strings.Contains(strings.ToLower(usage.RankBasis), "adoption") || strings.Contains(strings.ToLower(usage.RankBasis), "quality") {
		t.Fatalf("usage basis=%q", usage.RankBasis)
	}

	trending, err := client.EnrichModels(t.Context(), provider, ModelEnrichmentRequest{Rank: OpenRouterRankTrending})
	if err != nil {
		t.Fatal(err)
	}
	if trending.Ranks["vendor/a"].Position != 1 || trending.Ranks["vendor/a"].Value != "1" {
		t.Fatalf("trending=%#v", trending)
	}
	if _, exists := trending.Ranks["vendor/b"]; exists {
		t.Fatalf("sub-million current-window model should be excluded from trending: %#v", trending.Ranks["vendor/b"])
	}
	if !strings.Contains(strings.ToLower(trending.RankBasis), "adoption growth") || strings.Contains(strings.ToLower(trending.RankBasis), "quality") {
		t.Fatalf("trending basis=%q", trending.RankBasis)
	}
	if _, err := client.EnrichModels(t.Context(), provider, ModelEnrichmentRequest{Rank: OpenRouterRankTrending, RankWindow: OpenRouterRankWindowMonth}); !IsCategory(err, ErrorUnsupported) {
		t.Fatalf("unsupported trending window err=%v", err)
	}
}

func TestOpenRouterBenchmarkRanksUseStableMachineReadableMetrics(t *testing.T) {
	const credential = "openrouter-benchmark-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/benchmarks" || r.URL.Query().Get("source") != "artificial-analysis" || r.URL.Query().Get("task_type") != "coding" {
			t.Fatalf("request=%s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("authorization=%q", got)
		}
		_, _ = fmt.Fprint(w, `{"data":[
			{"model_permaslug":"vendor/a","coding_index":65.8,"intelligence_index":71.2,"agentic_index":58.3,"source":"artificial-analysis"},
			{"model_permaslug":"vendor/b","coding_index":70.1,"source":"artificial-analysis"}
		],"meta":{"as_of":"2026-09-15T02:00:00Z","source":"artificial-analysis","task_type":"coding"}}`)
	}))
	defer server.Close()
	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	provider := DefaultOpenRouter()
	provider.BaseURL = server.URL + "/api/v1"
	result, err := client.EnrichModels(t.Context(), provider, ModelEnrichmentRequest{Rank: "coding"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Ranks["vendor/b"].Position != 1 || result.Ranks["vendor/b"].Kind != "benchmark-coding" || result.RankFreshness == "" || !strings.Contains(result.RankSource, "Artificial Analysis") {
		t.Fatalf("benchmark result=%#v", result)
	}
	if _, err := client.EnrichModels(t.Context(), provider, ModelEnrichmentRequest{Rank: "subjective-best"}); !IsCategory(err, ErrorUnsupported) {
		t.Fatalf("subjective rank err=%v", err)
	}
}

func TestOpenRouterTaskRecommendationsExposeConcreteTaskSourceAndBasis(t *testing.T) {
	const credential = "openrouter-task-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/classifications/task" || r.URL.Query().Get("window") != "7d" {
			t.Fatalf("request=%s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("authorization=%q", got)
		}
		_, _ = fmt.Fprint(w, `{"data":{"as_of":"2026-09-15","window_days":7,"classifications":[
			{"display_name":"Code Generation","macro_category":"code","tag":"code:general_impl","models":[
				{"id":"vendor/a","tag_usage_share":0.25,"tag_token_share":0.35},
				{"id":"vendor/b","tag_usage_share":0.55,"tag_token_share":0.45}
			]}
		]}}`)
	}))
	defer server.Close()
	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	provider := DefaultOpenRouter()
	provider.BaseURL = server.URL + "/api/v1"
	result, err := client.EnrichModels(t.Context(), provider, ModelEnrichmentRequest{RecommendFor: "Code Generation"})
	if err != nil {
		t.Fatal(err)
	}
	best := result.Recommendations["vendor/b"]
	if best.Position != 1 || best.Task != "code:general_impl" || best.Share != 0.55 || result.RecommendationSource != "OpenRouter task classifications" || !strings.Contains(result.RecommendationBasis, "Code Generation") || result.RecommendationFreshness != "2026-09-15" {
		t.Fatalf("recommendations=%#v", result)
	}
	if _, err := client.EnrichModels(t.Context(), provider, ModelEnrichmentRequest{RecommendFor: "Unknown Task"}); !IsCategory(err, ErrorUnsupported) {
		t.Fatalf("unknown task err=%v", err)
	}
}
