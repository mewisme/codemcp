package llm

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	OpenRouterRankUsage    = "usage"
	OpenRouterRankTrending = "trending"

	OpenRouterRankWindowDay   = "day"
	OpenRouterRankWindowWeek  = "week"
	OpenRouterRankWindowMonth = "month"
)

func (c *Client) EnrichModels(ctx context.Context, provider Provider, request ModelEnrichmentRequest) (ModelEnrichmentResult, error) {
	if c == nil {
		return ModelEnrichmentResult{}, NewError(ErrorUnavailable, "", "inference client is unavailable")
	}
	provider, err := normalizeProvider(provider, true)
	if err != nil {
		return ModelEnrichmentResult{}, err
	}
	if provider.ID != OpenRouterID && provider.CoreKind != CoreOpenRouter {
		return ModelEnrichmentResult{}, NewError(ErrorUnsupported, "query", "ranking and recommendation enrichment is only available for OpenRouter")
	}
	request.Rank = normalizeOpenRouterRank(request.Rank)
	request.RankWindow = strings.ToLower(strings.TrimSpace(request.RankWindow))
	request.RecommendFor = strings.TrimSpace(request.RecommendFor)
	result := ModelEnrichmentResult{}
	if request.Rank != "" {
		result, err = c.openRouterRanks(ctx, provider, request.Rank, request.RankWindow)
		if err != nil {
			return ModelEnrichmentResult{}, err
		}
	}
	if request.RecommendFor != "" {
		recommendations, source, basis, freshness, recommendErr := c.openRouterRecommendations(ctx, provider, request.RecommendFor)
		if recommendErr != nil {
			return ModelEnrichmentResult{}, recommendErr
		}
		result.Recommendations = recommendations
		result.RecommendationSource = source
		result.RecommendationBasis = basis
		result.RecommendationFreshness = freshness
	}
	return result, nil
}

func normalizeOpenRouterRank(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "benchmark-intelligence", "intelligence":
		return "intelligence"
	case "benchmark-coding", "coding":
		return "coding"
	case "benchmark-agentic", "agentic":
		return "agentic"
	default:
		return value
	}
}

type openRouterDailyRankingResponse struct {
	Data []struct {
		Date           string `json:"date"`
		ModelPermaslug string `json:"model_permaslug"`
		TotalTokens    string `json:"total_tokens"`
	} `json:"data"`
	Meta struct {
		AsOf      string `json:"as_of"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	} `json:"meta"`
}

func (c *Client) openRouterRanks(ctx context.Context, provider Provider, rank, window string) (ModelEnrichmentResult, error) {
	switch rank {
	case OpenRouterRankUsage, OpenRouterRankTrending:
		return c.openRouterUsageRanks(ctx, provider, rank, window)
	case "intelligence", "coding", "agentic":
		return c.openRouterBenchmarkRanks(ctx, provider, rank)
	case "":
		return ModelEnrichmentResult{}, nil
	default:
		return ModelEnrichmentResult{}, NewError(ErrorUnsupported, "rank", fmt.Sprintf("OpenRouter rank dimension %q is not supported", rank))
	}
}

func (c *Client) openRouterUsageRanks(ctx context.Context, provider Provider, rank, window string) (ModelEnrichmentResult, error) {
	if rank == OpenRouterRankTrending {
		if window != "" && window != OpenRouterRankWindowWeek {
			return ModelEnrichmentResult{}, NewError(ErrorUnsupported, "rank_window", "trending rank uses a trailing week compared with the prior week")
		}
		window = OpenRouterRankWindowWeek
	} else {
		if window == "" {
			window = OpenRouterRankWindowWeek
		}
		if window != OpenRouterRankWindowDay && window != OpenRouterRankWindowWeek && window != OpenRouterRankWindowMonth {
			return ModelEnrichmentResult{}, NewError(ErrorUnsupported, "rank_window", fmt.Sprintf("usage rank window %q is not supported", window))
		}
	}
	credential, err := c.credentialFor(ctx, provider, AuthBearer)
	if err != nil {
		return ModelEnrichmentResult{}, err
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+credential)
	raw, err := c.doJSON(ctx, http.MethodGet, provider.BaseURL+"/datasets/rankings-daily", nil, headers)
	if err != nil {
		return ModelEnrichmentResult{}, err
	}
	var response openRouterDailyRankingResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return ModelEnrichmentResult{}, err
	}
	if len(response.Data) > 31*51 {
		return ModelEnrichmentResult{}, NewError(ErrorInvalidResponse, "rank", "OpenRouter ranking response exceeds the bounded daily dataset")
	}
	end, err := time.Parse("2006-01-02", strings.TrimSpace(response.Meta.EndDate))
	if err != nil {
		return ModelEnrichmentResult{}, NewError(ErrorInvalidResponse, "rank", "OpenRouter ranking response has an invalid end date")
	}
	currentDays := 7
	if rank == OpenRouterRankUsage {
		switch window {
		case OpenRouterRankWindowDay:
			currentDays = 1
		case OpenRouterRankWindowMonth:
			currentDays = 30
		}
	}
	currentStart := end.AddDate(0, 0, -(currentDays - 1))
	priorEnd := currentStart.AddDate(0, 0, -1)
	priorStart := priorEnd.AddDate(0, 0, -6)
	current := map[string]*big.Int{}
	prior := map[string]*big.Int{}
	for _, row := range response.Data {
		id := strings.TrimSpace(row.ModelPermaslug)
		if id == "" || id == "other" {
			continue
		}
		date, parseErr := time.Parse("2006-01-02", row.Date)
		if parseErr != nil {
			return ModelEnrichmentResult{}, NewError(ErrorInvalidResponse, "rank", "OpenRouter ranking response has an invalid row date")
		}
		value := new(big.Int)
		if _, ok := value.SetString(strings.TrimSpace(row.TotalTokens), 10); !ok || value.Sign() < 0 {
			return ModelEnrichmentResult{}, NewError(ErrorInvalidResponse, "rank", "OpenRouter ranking response has an invalid token total")
		}
		if !date.Before(currentStart) && !date.After(end) {
			addBigInt(current, id, value)
		}
		if rank == OpenRouterRankTrending && !date.Before(priorStart) && !date.After(priorEnd) {
			addBigInt(prior, id, value)
		}
	}
	type ranked struct {
		id    string
		score float64
		value string
	}
	rows := make([]ranked, 0, len(current))
	for id, total := range current {
		if rank == OpenRouterRankUsage {
			score, _ := new(big.Float).SetInt(total).Float64()
			rows = append(rows, ranked{id: id, score: score, value: total.String()})
			continue
		}
		previous := prior[id]
		if total.Cmp(big.NewInt(1_000_000)) < 0 {
			continue
		}
		score := 0.0
		if previous == nil || previous.Sign() == 0 {
			if total.Sign() > 0 {
				score = math.Inf(1)
			}
		} else {
			recentFloat, _ := new(big.Float).SetInt(total).Float64()
			priorFloat, _ := new(big.Float).SetInt(previous).Float64()
			score = (recentFloat - priorFloat) / priorFloat
		}
		rows = append(rows, ranked{id: id, score: score, value: formatRankScore(score)})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].id < rows[j].id
		}
		return rows[i].score > rows[j].score
	})
	ranks := make(map[string]ModelRankMetadata, len(rows))
	basis := "OpenRouter adoption by total tokens processed"
	if rank == OpenRouterRankTrending {
		basis = "OpenRouter adoption growth: trailing 7 days versus prior 7 days"
	}
	for index, row := range rows {
		ranks[row.id] = ModelRankMetadata{Position: index + 1, Kind: rank, Source: "OpenRouter rankings", Basis: basis, Window: window, Freshness: strings.TrimSpace(response.Meta.AsOf), Value: row.value}
	}
	return ModelEnrichmentResult{Ranks: ranks, RankSource: "OpenRouter rankings", RankWindow: window, RankBasis: basis, RankFreshness: strings.TrimSpace(response.Meta.AsOf)}, nil
}

func addBigInt(values map[string]*big.Int, id string, value *big.Int) {
	current := values[id]
	if current == nil {
		values[id] = new(big.Int).Set(value)
		return
	}
	current.Add(current, value)
}

func formatRankScore(value float64) string {
	if math.IsInf(value, 1) {
		return "+inf"
	}
	return strconv.FormatFloat(value, 'g', 8, 64)
}

type openRouterBenchmarkResponse struct {
	Data []struct {
		ModelPermaslug    string   `json:"model_permaslug"`
		IntelligenceIndex *float64 `json:"intelligence_index"`
		CodingIndex       *float64 `json:"coding_index"`
		AgenticIndex      *float64 `json:"agentic_index"`
	} `json:"data"`
	Meta struct {
		AsOf string `json:"as_of"`
	} `json:"meta"`
}

func (c *Client) openRouterBenchmarkRanks(ctx context.Context, provider Provider, rank string) (ModelEnrichmentResult, error) {
	credential, err := c.credentialFor(ctx, provider, AuthBearer)
	if err != nil {
		return ModelEnrichmentResult{}, err
	}
	query := url.Values{"source": {"artificial-analysis"}, "task_type": {rank}}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+credential)
	raw, err := c.doJSON(ctx, http.MethodGet, provider.BaseURL+"/benchmarks?"+query.Encode(), nil, headers)
	if err != nil {
		return ModelEnrichmentResult{}, err
	}
	var response openRouterBenchmarkResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return ModelEnrichmentResult{}, err
	}
	if len(response.Data) > MaxDiscoveredModels {
		return ModelEnrichmentResult{}, NewError(ErrorInvalidResponse, "rank", "OpenRouter benchmark response contains too many models")
	}
	type ranked struct {
		id    string
		score float64
	}
	rows := make([]ranked, 0, len(response.Data))
	for _, row := range response.Data {
		id := strings.TrimSpace(row.ModelPermaslug)
		var score *float64
		switch rank {
		case "intelligence":
			score = row.IntelligenceIndex
		case "coding":
			score = row.CodingIndex
		case "agentic":
			score = row.AgenticIndex
		}
		if id != "" && score != nil {
			rows = append(rows, ranked{id: id, score: *score})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].id < rows[j].id
		}
		return rows[i].score > rows[j].score
	})
	basis := "Artificial Analysis " + rank + " benchmark index"
	ranks := make(map[string]ModelRankMetadata, len(rows))
	for index, row := range rows {
		ranks[row.id] = ModelRankMetadata{Position: index + 1, Kind: "benchmark-" + rank, Source: "OpenRouter benchmarks / Artificial Analysis", Basis: basis, Freshness: strings.TrimSpace(response.Meta.AsOf), Value: strconv.FormatFloat(row.score, 'g', 8, 64)}
	}
	return ModelEnrichmentResult{Ranks: ranks, RankSource: "OpenRouter benchmarks / Artificial Analysis", RankBasis: basis, RankFreshness: strings.TrimSpace(response.Meta.AsOf)}, nil
}

type openRouterTaskClassificationResponse struct {
	Data struct {
		AsOf            string `json:"as_of"`
		WindowDays      int    `json:"window_days"`
		Classifications []struct {
			DisplayName   string `json:"display_name"`
			MacroCategory string `json:"macro_category"`
			Tag           string `json:"tag"`
			Models        []struct {
				ID            string  `json:"id"`
				TagUsageShare float64 `json:"tag_usage_share"`
				TagTokenShare float64 `json:"tag_token_share"`
			} `json:"models"`
		} `json:"classifications"`
	} `json:"data"`
}

func (c *Client) openRouterRecommendations(ctx context.Context, provider Provider, task string) (map[string]ModelRecommendation, string, string, string, error) {
	credential, err := c.credentialFor(ctx, provider, AuthBearer)
	if err != nil {
		return nil, "", "", "", err
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+credential)
	raw, err := c.doJSON(ctx, http.MethodGet, provider.BaseURL+"/classifications/task?window=7d", nil, headers)
	if err != nil {
		return nil, "", "", "", err
	}
	var response openRouterTaskClassificationResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return nil, "", "", "", err
	}
	normalizedTask := normalizeTaskKey(task)
	var selected *struct {
		DisplayName   string `json:"display_name"`
		MacroCategory string `json:"macro_category"`
		Tag           string `json:"tag"`
		Models        []struct {
			ID            string  `json:"id"`
			TagUsageShare float64 `json:"tag_usage_share"`
			TagTokenShare float64 `json:"tag_token_share"`
		} `json:"models"`
	}
	for index := range response.Data.Classifications {
		item := &response.Data.Classifications[index]
		if normalizedTask == normalizeTaskKey(item.Tag) || normalizedTask == normalizeTaskKey(item.DisplayName) || normalizedTask == normalizeTaskKey(item.MacroCategory) {
			selected = item
			break
		}
	}
	if selected == nil {
		return nil, "", "", "", NewError(ErrorUnsupported, "recommend_for", fmt.Sprintf("OpenRouter task %q is not available in the current classification dataset", task))
	}
	models := append([]struct {
		ID            string  `json:"id"`
		TagUsageShare float64 `json:"tag_usage_share"`
		TagTokenShare float64 `json:"tag_token_share"`
	}(nil), selected.Models...)
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].TagUsageShare == models[j].TagUsageShare {
			return models[i].ID < models[j].ID
		}
		return models[i].TagUsageShare > models[j].TagUsageShare
	})
	basis := fmt.Sprintf("OpenRouter task-classification request share for %s over trailing %d days", selected.DisplayName, response.Data.WindowDays)
	result := make(map[string]ModelRecommendation, len(models))
	for index, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		result[id] = ModelRecommendation{Position: index + 1, Task: selected.Tag, Source: "OpenRouter task classifications", Basis: basis, Freshness: strings.TrimSpace(response.Data.AsOf), Share: model.TagUsageShare}
	}
	return result, "OpenRouter task classifications", basis, strings.TrimSpace(response.Data.AsOf), nil
}

func normalizeTaskKey(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
