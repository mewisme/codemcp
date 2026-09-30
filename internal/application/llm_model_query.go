package application

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/llm"
)

const (
	defaultLLMModelReadLimit   = 50
	maxLLMModelReadLimit       = 500
	maxLLMModelQueryValues     = 128
	maxLLMModelSearchBytes     = 256
	maxLLMModelSortFields      = 8
	maxLLMModelEnrichmentCache = 16
	llmModelEnrichmentCacheTTL = 5 * time.Minute
)

type LLMModelRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type LLMModelSort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type LLMOllamaModelQuery struct {
	Families          []string `json:"families,omitempty"`
	Formats           []string `json:"formats,omitempty"`
	Quantizations     []string `json:"quantizations,omitempty"`
	MinParameterCount *int64   `json:"min_parameter_count,omitempty"`
	MaxParameterCount *int64   `json:"max_parameter_count,omitempty"`
	MinSizeBytes      *int64   `json:"min_size_bytes,omitempty"`
	MaxSizeBytes      *int64   `json:"max_size_bytes,omitempty"`
}

type LLMModelQuery struct {
	Search             string              `json:"search,omitempty"`
	ExactIDs           []string            `json:"exact_ids,omitempty"`
	Authors            []string            `json:"authors,omitempty"`
	Free               *bool               `json:"free,omitempty"`
	MinContext         *int                `json:"min_context,omitempty"`
	MaxContext         *int                `json:"max_context,omitempty"`
	MinPromptPrice     string              `json:"min_prompt_price,omitempty"`
	MaxPromptPrice     string              `json:"max_prompt_price,omitempty"`
	MinCompletionPrice string              `json:"min_completion_price,omitempty"`
	MaxCompletionPrice string              `json:"max_completion_price,omitempty"`
	Capabilities       []string            `json:"capabilities,omitempty"`
	Parameters         []string            `json:"supported_parameters,omitempty"`
	InputModalities    []string            `json:"input_modalities,omitempty"`
	OutputModalities   []string            `json:"output_modalities,omitempty"`
	CreatedAfter       *time.Time          `json:"created_after,omitempty"`
	CreatedBefore      *time.Time          `json:"created_before,omitempty"`
	ModifiedAfter      *time.Time          `json:"modified_after,omitempty"`
	ModifiedBefore     *time.Time          `json:"modified_before,omitempty"`
	Ollama             LLMOllamaModelQuery `json:"ollama,omitempty"`
	Sort               []LLMModelSort      `json:"sort,omitempty"`
	Rank               string              `json:"rank,omitempty"`
	RankWindow         string              `json:"rank_window,omitempty"`
	RecommendFor       string              `json:"recommend_for,omitempty"`
	Offset             int                 `json:"offset,omitempty"`
	Limit              int                 `json:"limit,omitempty"`
	Range              *LLMModelRange      `json:"range,omitempty"`
	All                bool                `json:"all,omitempty"`
	CountOnly          bool                `json:"count_only,omitempty"`
	Refresh            bool                `json:"refresh,omitempty"`
}

type LLMModelQueryCapabilities struct {
	Filters        []string `json:"filters"`
	Sorts          []string `json:"sorts"`
	Ranks          []string `json:"ranks,omitempty"`
	RankWindows    []string `json:"rank_windows,omitempty"`
	Recommendation bool     `json:"recommendation"`
}

type LLMModelPage struct {
	ProviderID              llm.ProviderID            `json:"provider_id"`
	TotalCatalog            int                       `json:"total_catalog"`
	Matched                 int                       `json:"matched"`
	Offset                  int                       `json:"offset"`
	Limit                   int                       `json:"limit"`
	Returned                int                       `json:"returned"`
	HasMore                 bool                      `json:"has_more"`
	Refreshed               bool                      `json:"refreshed"`
	Sort                    []LLMModelSort            `json:"sort"`
	RankSource              string                    `json:"rank_source,omitempty"`
	RankWindow              string                    `json:"rank_window,omitempty"`
	RankBasis               string                    `json:"rank_basis,omitempty"`
	RankFreshness           string                    `json:"rank_freshness,omitempty"`
	RecommendationBasis     string                    `json:"recommendation_basis,omitempty"`
	RecommendationSource    string                    `json:"recommendation_source,omitempty"`
	RecommendationFreshness string                    `json:"recommendation_freshness,omitempty"`
	QueryCapabilities       LLMModelQueryCapabilities `json:"query_capabilities"`
	Models                  []llm.Model               `json:"models"`
}

type llmModelEnrichmentCacheEntry struct {
	providerID  llm.ProviderID
	fingerprint string
	key         string
	value       llm.ModelEnrichmentResult
	expiresAt   time.Time
	access      uint64
}

type llmModelEnrichmentCache struct {
	mu      sync.Mutex
	entries map[string]llmModelEnrichmentCacheEntry
	clock   uint64
}

func newLLMModelEnrichmentCache() *llmModelEnrichmentCache {
	return &llmModelEnrichmentCache{entries: map[string]llmModelEnrichmentCacheEntry{}}
}

func (s *LLMService) ModelCatalog(ctx context.Context, rawID string, query LLMModelQuery) (LLMModelPage, error) {
	provider, err := s.Provider(ctx, rawID)
	if err != nil {
		return LLMModelPage{}, err
	}
	query, err = normalizeLLMModelQuery(query)
	if err != nil {
		return LLMModelPage{}, err
	}
	var models []llm.Model
	if query.Refresh {
		models, err = s.RefreshProviderModels(ctx, string(provider.ID))
	} else {
		models, err = s.ProviderModels(ctx, string(provider.ID))
	}
	if err != nil {
		return LLMModelPage{}, err
	}
	capabilities := modelQueryCapabilities(provider, models)
	if err := validateLLMModelQueryCapabilities(query, capabilities); err != nil {
		return LLMModelPage{}, err
	}
	filtered, err := filterLLMModels(models, query)
	if err != nil {
		return LLMModelPage{}, err
	}
	page := LLMModelPage{ProviderID: provider.ID, TotalCatalog: len(models), Matched: len(filtered), Refreshed: query.Refresh, QueryCapabilities: capabilities}
	if query.CountOnly {
		page.Sort = normalizedSortForResult(query)
		return page, nil
	}
	if query.Rank != "" || query.RecommendFor != "" {
		enrichment, enrichErr := s.modelEnrichment(ctx, provider, query)
		if enrichErr != nil {
			return LLMModelPage{}, enrichErr
		}
		applyModelEnrichment(filtered, enrichment)
		page.RankSource = enrichment.RankSource
		page.RankWindow = enrichment.RankWindow
		page.RankBasis = enrichment.RankBasis
		page.RankFreshness = enrichment.RankFreshness
		page.RecommendationSource = enrichment.RecommendationSource
		page.RecommendationBasis = enrichment.RecommendationBasis
		page.RecommendationFreshness = enrichment.RecommendationFreshness
	}
	page.Sort = normalizedSortForResult(query)
	if err := sortLLMModels(filtered, query); err != nil {
		return LLMModelPage{}, err
	}
	offset, limit := modelQueryWindow(query, len(filtered))
	page.Offset = offset
	page.Limit = limit
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page.Models = cloneLLMModels(filtered[offset:end])
	page.Returned = len(page.Models)
	page.HasMore = end < len(filtered)
	return page, nil
}

func normalizeLLMModelQuery(query LLMModelQuery) (LLMModelQuery, error) {
	query.Search = strings.TrimSpace(query.Search)
	if len(query.Search) > maxLLMModelSearchBytes {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "search", fmt.Sprintf("model search must be at most %d bytes", maxLLMModelSearchBytes))
	}
	var err error
	for _, target := range []struct {
		name   string
		values *[]string
	}{
		{"exact_ids", &query.ExactIDs}, {"authors", &query.Authors}, {"capabilities", &query.Capabilities}, {"supported_parameters", &query.Parameters},
		{"input_modalities", &query.InputModalities}, {"output_modalities", &query.OutputModalities}, {"families", &query.Ollama.Families}, {"formats", &query.Ollama.Formats}, {"quantizations", &query.Ollama.Quantizations},
	} {
		*target.values, err = normalizeQueryValues(*target.values)
		if err != nil {
			return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, target.name, err.Error())
		}
	}
	if query.MinContext != nil && *query.MinContext < 0 || query.MaxContext != nil && *query.MaxContext < 0 {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "context", "context bounds must be non-negative")
	}
	if query.MinContext != nil && query.MaxContext != nil && *query.MinContext > *query.MaxContext {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "context", "minimum context must not exceed maximum context")
	}
	if err := validateInt64Bounds(query.Ollama.MinParameterCount, query.Ollama.MaxParameterCount, "parameter_count"); err != nil {
		return LLMModelQuery{}, err
	}
	if err := validateInt64Bounds(query.Ollama.MinSizeBytes, query.Ollama.MaxSizeBytes, "size"); err != nil {
		return LLMModelQuery{}, err
	}
	for _, price := range []struct{ field, min, max string }{{"prompt_price", query.MinPromptPrice, query.MaxPromptPrice}, {"completion_price", query.MinCompletionPrice, query.MaxCompletionPrice}} {
		if err := validatePriceBounds(price.field, price.min, price.max); err != nil {
			return LLMModelQuery{}, err
		}
	}
	if err := validateTimeBounds(query.CreatedAfter, query.CreatedBefore, "created"); err != nil {
		return LLMModelQuery{}, err
	}
	if err := validateTimeBounds(query.ModifiedAfter, query.ModifiedBefore, "modified"); err != nil {
		return LLMModelQuery{}, err
	}
	if len(query.Sort) > maxLLMModelSortFields {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "sort", fmt.Sprintf("at most %d sort fields are allowed", maxLLMModelSortFields))
	}
	for index := range query.Sort {
		query.Sort[index].Field = normalizeQueryKey(query.Sort[index].Field)
		query.Sort[index].Direction = strings.ToLower(strings.TrimSpace(query.Sort[index].Direction))
		if query.Sort[index].Direction == "" {
			query.Sort[index].Direction = "asc"
		}
		if query.Sort[index].Direction != "asc" && query.Sort[index].Direction != "desc" {
			return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "sort", "sort direction must be asc or desc")
		}
	}
	query.Rank = normalizeQueryKey(query.Rank)
	query.RankWindow = normalizeQueryKey(query.RankWindow)
	query.RecommendFor = strings.TrimSpace(query.RecommendFor)
	if query.Rank != "" && query.RecommendFor != "" {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "query", "rank and recommend_for cannot be combined")
	}
	if (query.Rank != "" || query.RecommendFor != "") && len(query.Sort) > 0 {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "query", "explicit sort cannot be combined with rank or recommend_for")
	}
	if query.Offset < 0 || query.Limit < 0 {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "pagination", "offset and limit must be non-negative")
	}
	windowModes := 0
	if query.All {
		windowModes++
	}
	if query.Range != nil {
		windowModes++
	}
	if query.Offset != 0 || query.Limit != 0 {
		windowModes++
	}
	if query.CountOnly {
		windowModes++
	}
	if windowModes > 1 {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "pagination", "all, range, offset/limit and count_only are mutually exclusive")
	}
	if query.CountOnly && (query.Rank != "" || query.RecommendFor != "" || len(query.Sort) > 0) {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "count_only", "count-only queries cannot sort, rank or request recommendations")
	}
	if query.Range != nil {
		if query.Range.Start < 1 || query.Range.End < query.Range.Start {
			return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "range", "range must use 1-based inclusive start:end positions")
		}
		span := query.Range.End - query.Range.Start + 1
		if span > maxLLMModelReadLimit {
			return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "range", fmt.Sprintf("range may return at most %d models", maxLLMModelReadLimit))
		}
		query.Offset = query.Range.Start - 1
		query.Limit = span
	}
	if !query.All && !query.CountOnly && query.Limit == 0 {
		query.Limit = defaultLLMModelReadLimit
	}
	if query.Limit > maxLLMModelReadLimit {
		return LLMModelQuery{}, llm.NewError(llm.ErrorInvalidRequest, "limit", fmt.Sprintf("limit must be at most %d", maxLLMModelReadLimit))
	}
	return query, nil
}

func normalizeQueryValues(values []string) ([]string, error) {
	if len(values) > maxLLMModelQueryValues {
		return nil, fmt.Errorf("at most %d values are allowed", maxLLMModelQueryValues)
	}
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" || len(value) > llm.MaxModelIDBytes {
			return nil, fmt.Errorf("values must be non-empty and at most %d bytes", llm.MaxModelIDBytes)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func normalizeQueryKey(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "_", "-")
}

func validateInt64Bounds(minimum, maximum *int64, field string) error {
	if minimum != nil && *minimum < 0 || maximum != nil && *maximum < 0 {
		return llm.NewError(llm.ErrorInvalidRequest, field, "bounds must be non-negative")
	}
	if minimum != nil && maximum != nil && *minimum > *maximum {
		return llm.NewError(llm.ErrorInvalidRequest, field, "minimum must not exceed maximum")
	}
	return nil
}

func validateTimeBounds(after, before *time.Time, field string) error {
	if after != nil && before != nil && after.After(*before) {
		return llm.NewError(llm.ErrorInvalidRequest, field, "start time must not be after end time")
	}
	return nil
}

func validatePriceBounds(field, minimum, maximum string) error {
	min, minSet, err := parsePrice(minimum)
	if err != nil {
		return llm.NewError(llm.ErrorInvalidRequest, field, "minimum price must be a non-negative decimal")
	}
	max, maxSet, err := parsePrice(maximum)
	if err != nil {
		return llm.NewError(llm.ErrorInvalidRequest, field, "maximum price must be a non-negative decimal")
	}
	if minSet && maxSet && min.Cmp(max) > 0 {
		return llm.NewError(llm.ErrorInvalidRequest, field, "minimum price must not exceed maximum price")
	}
	return nil
}

func parsePrice(raw string) (*big.Rat, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, false, nil
	}
	result := new(big.Rat)
	if _, ok := result.SetString(value); !ok || result.Sign() < 0 {
		return nil, true, fmt.Errorf("invalid price")
	}
	return result, true, nil
}

func modelQueryCapabilities(provider llm.Provider, models []llm.Model) LLMModelQueryCapabilities {
	filters := []string{"search", "id"}
	sorts := []string{"id", "name"}
	cap := LLMModelQueryCapabilities{}
	if provider.Discovery == llm.DiscoveryOllamaTags {
		filters = append(filters, "family", "format", "quantization", "parameter-size", "size", "modified")
		sorts = append(sorts, "modified", "size", "parameter-size")
	} else {
		if modelMetadataPresent(models, func(model llm.Model) bool { return model.Author != "" }) {
			filters = append(filters, "author")
		}
		if modelMetadataPresent(models, func(model llm.Model) bool { return model.ContextLengthKnown }) {
			filters = append(filters, "context")
			sorts = append(sorts, "context")
		}
		if modelMetadataPresent(models, func(model llm.Model) bool { return model.PricingKnown }) {
			filters = append(filters, "free", "prompt-price", "completion-price")
			sorts = append(sorts, "prompt-price", "completion-price")
		}
		if modelMetadataPresent(models, func(model llm.Model) bool { return model.CapabilitiesKnown }) {
			filters = append(filters, "capability", "parameter")
		}
		if modelMetadataPresent(models, func(model llm.Model) bool { return model.ModalitiesKnown }) {
			filters = append(filters, "input", "output")
		}
		if modelMetadataPresent(models, func(model llm.Model) bool { return model.CreatedAt != nil }) {
			filters = append(filters, "created")
			sorts = append(sorts, "created")
		}
	}
	cap.Filters = uniqueSortedStrings(filters)
	cap.Sorts = uniqueSortedStrings(sorts)
	return cap
}

func modelMetadataPresent(models []llm.Model, predicate func(llm.Model) bool) bool {
	for _, model := range models {
		if predicate(model) {
			return true
		}
	}
	return false
}

func uniqueSortedStrings(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateLLMModelQueryCapabilities(query LLMModelQuery, capabilities LLMModelQueryCapabilities) error {
	requested := []string{}
	if len(query.Authors) > 0 {
		requested = append(requested, "author")
	}
	if query.Free != nil {
		requested = append(requested, "free")
	}
	if query.MinContext != nil || query.MaxContext != nil {
		requested = append(requested, "context")
	}
	if query.MinPromptPrice != "" || query.MaxPromptPrice != "" {
		requested = append(requested, "prompt-price")
	}
	if query.MinCompletionPrice != "" || query.MaxCompletionPrice != "" {
		requested = append(requested, "completion-price")
	}
	if len(query.Capabilities) > 0 {
		requested = append(requested, "capability")
	}
	if len(query.Parameters) > 0 {
		requested = append(requested, "parameter")
	}
	if len(query.InputModalities) > 0 {
		requested = append(requested, "input")
	}
	if len(query.OutputModalities) > 0 {
		requested = append(requested, "output")
	}
	if query.CreatedAfter != nil || query.CreatedBefore != nil {
		requested = append(requested, "created")
	}
	if query.ModifiedAfter != nil || query.ModifiedBefore != nil {
		requested = append(requested, "modified")
	}
	if len(query.Ollama.Families) > 0 {
		requested = append(requested, "family")
	}
	if len(query.Ollama.Formats) > 0 {
		requested = append(requested, "format")
	}
	if len(query.Ollama.Quantizations) > 0 {
		requested = append(requested, "quantization")
	}
	if query.Ollama.MinParameterCount != nil || query.Ollama.MaxParameterCount != nil {
		requested = append(requested, "parameter-size")
	}
	if query.Ollama.MinSizeBytes != nil || query.Ollama.MaxSizeBytes != nil {
		requested = append(requested, "size")
	}
	for _, dimension := range requested {
		if !containsString(capabilities.Filters, dimension) {
			return llm.NewError(llm.ErrorUnsupported, "query", fmt.Sprintf("model filter %q is not supported by this provider catalog", dimension))
		}
	}
	for _, order := range query.Sort {
		if !containsString(capabilities.Sorts, order.Field) {
			return llm.NewError(llm.ErrorUnsupported, "sort", fmt.Sprintf("model sort %q is not supported by this provider catalog", order.Field))
		}
	}
	if query.Rank != "" && !containsString(capabilities.Ranks, query.Rank) {
		return llm.NewError(llm.ErrorUnsupported, "rank", fmt.Sprintf("model rank %q is not supported by this provider", query.Rank))
	}
	if query.RankWindow != "" && query.Rank != "usage" && query.Rank != "trending" {
		return llm.NewError(llm.ErrorUnsupported, "rank_window", "rank window is only valid for usage or trending rank")
	}
	if query.RecommendFor != "" && !capabilities.Recommendation {
		return llm.NewError(llm.ErrorUnsupported, "recommend_for", "model recommendations are not supported by this provider")
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func filterLLMModels(models []llm.Model, query LLMModelQuery) ([]llm.Model, error) {
	result := make([]llm.Model, 0, len(models))
	search := strings.ToLower(query.Search)
	for _, model := range models {
		if search != "" && !containsFold(model.ID, search) && !containsFold(model.Name, search) && !containsFold(model.Author, search) {
			continue
		}
		if len(query.ExactIDs) > 0 && !matchesOneFold(model.ID, query.ExactIDs) {
			continue
		}
		if len(query.Authors) > 0 && !matchesOneFold(model.Author, query.Authors) {
			continue
		}
		if query.Free != nil && (!model.FreeKnown || model.Free != *query.Free) {
			continue
		}
		if query.MinContext != nil && (!model.ContextLengthKnown || model.ContextLength < *query.MinContext) {
			continue
		}
		if query.MaxContext != nil && (!model.ContextLengthKnown || model.ContextLength > *query.MaxContext) {
			continue
		}
		if !priceInRange(model.PromptPrice, model.PricingKnown, query.MinPromptPrice, query.MaxPromptPrice) {
			continue
		}
		if !priceInRange(model.CompletionPrice, model.PricingKnown, query.MinCompletionPrice, query.MaxCompletionPrice) {
			continue
		}
		if len(query.Capabilities) > 0 && !matchesAnyFold(model.Capabilities, query.Capabilities) {
			continue
		}
		if len(query.Parameters) > 0 && !matchesAnyFold(model.SupportedParameters, query.Parameters) {
			continue
		}
		if len(query.InputModalities) > 0 && !matchesAnyFold(model.InputModalities, query.InputModalities) {
			continue
		}
		if len(query.OutputModalities) > 0 && !matchesAnyFold(model.OutputModalities, query.OutputModalities) {
			continue
		}
		if !timeInRange(model.CreatedAt, query.CreatedAfter, query.CreatedBefore) || !timeInRange(model.ModifiedAt, query.ModifiedAfter, query.ModifiedBefore) {
			continue
		}
		if !matchesOllamaFilters(model, query.Ollama) {
			continue
		}
		result = append(result, model)
	}
	return result, nil
}

func containsFold(value, lowerTarget string) bool {
	return strings.Contains(strings.ToLower(value), lowerTarget)
}
func matchesOneFold(value string, targets []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, target := range targets {
		if value == target {
			return true
		}
	}
	return false
}
func matchesAnyFold(values, targets []string) bool {
	for _, value := range values {
		if matchesOneFold(value, targets) {
			return true
		}
	}
	return false
}

func priceInRange(raw string, known bool, minimum, maximum string) bool {
	if minimum == "" && maximum == "" {
		return true
	}
	if !known {
		return false
	}
	value, set, err := parsePrice(raw)
	if err != nil || !set {
		return false
	}
	min, minSet, _ := parsePrice(minimum)
	max, maxSet, _ := parsePrice(maximum)
	return (!minSet || value.Cmp(min) >= 0) && (!maxSet || value.Cmp(max) <= 0)
}

func timeInRange(value, after, before *time.Time) bool {
	if after == nil && before == nil {
		return true
	}
	if value == nil {
		return false
	}
	return (after == nil || !value.Before(*after)) && (before == nil || !value.After(*before))
}

func matchesOllamaFilters(model llm.Model, query LLMOllamaModelQuery) bool {
	if len(query.Families) == 0 && len(query.Formats) == 0 && len(query.Quantizations) == 0 && query.MinParameterCount == nil && query.MaxParameterCount == nil && query.MinSizeBytes == nil && query.MaxSizeBytes == nil {
		return true
	}
	if model.Ollama == nil {
		return false
	}
	metadata := model.Ollama
	families := append([]string{metadata.Family}, metadata.Families...)
	if len(query.Families) > 0 && !matchesAnyFold(families, query.Families) {
		return false
	}
	if len(query.Formats) > 0 && !matchesOneFold(metadata.Format, query.Formats) {
		return false
	}
	if len(query.Quantizations) > 0 && !matchesOneFold(metadata.QuantizationLevel, query.Quantizations) {
		return false
	}
	if query.MinParameterCount != nil && (metadata.ParameterCount == nil || *metadata.ParameterCount < *query.MinParameterCount) {
		return false
	}
	if query.MaxParameterCount != nil && (metadata.ParameterCount == nil || *metadata.ParameterCount > *query.MaxParameterCount) {
		return false
	}
	if query.MinSizeBytes != nil && (metadata.SizeBytes == nil || *metadata.SizeBytes < *query.MinSizeBytes) {
		return false
	}
	if query.MaxSizeBytes != nil && (metadata.SizeBytes == nil || *metadata.SizeBytes > *query.MaxSizeBytes) {
		return false
	}
	return true
}

func normalizedSortForResult(query LLMModelQuery) []LLMModelSort {
	if query.Rank != "" {
		return []LLMModelSort{{Field: "rank", Direction: "asc"}}
	}
	if query.RecommendFor != "" {
		return []LLMModelSort{{Field: "recommendation", Direction: "asc"}}
	}
	if len(query.Sort) == 0 {
		return []LLMModelSort{{Field: "id", Direction: "asc"}}
	}
	return append([]LLMModelSort(nil), query.Sort...)
}

func sortLLMModels(models []llm.Model, query LLMModelQuery) error {
	orders := normalizedSortForResult(query)
	sort.SliceStable(models, func(i, j int) bool {
		for _, order := range orders {
			cmp := compareLLMModels(models[i], models[j], order.Field)
			if cmp != 0 {
				if order.Direction == "desc" {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return models[i].ID < models[j].ID
	})
	return nil
}

func compareLLMModels(left, right llm.Model, field string) int {
	switch field {
	case "id":
		return strings.Compare(strings.ToLower(left.ID), strings.ToLower(right.ID))
	case "name":
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	case "context":
		return compareKnownInts(left.ContextLength, left.ContextLengthKnown, right.ContextLength, right.ContextLengthKnown)
	case "prompt-price":
		return comparePrices(left.PromptPrice, left.PricingKnown, right.PromptPrice, right.PricingKnown)
	case "completion-price":
		return comparePrices(left.CompletionPrice, left.PricingKnown, right.CompletionPrice, right.PricingKnown)
	case "created":
		return compareTimes(left.CreatedAt, right.CreatedAt)
	case "modified":
		return compareTimes(left.ModifiedAt, right.ModifiedAt)
	case "size":
		return compareInt64Pointers(ollamaSize(left), ollamaSize(right))
	case "parameter-size":
		return compareInt64Pointers(ollamaParameterCount(left), ollamaParameterCount(right))
	case "rank":
		return comparePositions(rankPosition(left), rankPosition(right))
	case "recommendation":
		return comparePositions(recommendationPosition(left), recommendationPosition(right))
	default:
		return 0
	}
}

func compareKnownInts(left int, leftKnown bool, right int, rightKnown bool) int {
	if leftKnown != rightKnown {
		if leftKnown {
			return -1
		}
		return 1
	}
	if !leftKnown || left == right {
		return 0
	}
	if left < right {
		return -1
	}
	return 1
}
func comparePrices(left string, leftKnown bool, right string, rightKnown bool) int {
	if leftKnown != rightKnown {
		if leftKnown {
			return -1
		}
		return 1
	}
	if !leftKnown {
		return 0
	}
	a, okA, _ := parsePrice(left)
	b, okB, _ := parsePrice(right)
	if okA != okB {
		if okA {
			return -1
		}
		return 1
	}
	if !okA {
		return 0
	}
	return a.Cmp(b)
}
func compareTimes(left, right *time.Time) int {
	if left == nil || right == nil {
		if left != nil {
			return -1
		}
		if right != nil {
			return 1
		}
		return 0
	}
	if left.Equal(*right) {
		return 0
	}
	if left.Before(*right) {
		return -1
	}
	return 1
}
func compareInt64Pointers(left, right *int64) int {
	if left == nil || right == nil {
		if left != nil {
			return -1
		}
		if right != nil {
			return 1
		}
		return 0
	}
	if *left == *right {
		return 0
	}
	if *left < *right {
		return -1
	}
	return 1
}
func comparePositions(left, right int) int {
	if left == 0 || right == 0 {
		if left != 0 {
			return -1
		}
		if right != 0 {
			return 1
		}
		return 0
	}
	if left == right {
		return 0
	}
	if left < right {
		return -1
	}
	return 1
}
func ollamaSize(model llm.Model) *int64 {
	if model.Ollama == nil {
		return nil
	}
	return model.Ollama.SizeBytes
}
func ollamaParameterCount(model llm.Model) *int64 {
	if model.Ollama == nil {
		return nil
	}
	return model.Ollama.ParameterCount
}
func rankPosition(model llm.Model) int {
	if model.Rank == nil {
		return 0
	}
	return model.Rank.Position
}
func recommendationPosition(model llm.Model) int {
	if model.Recommendation == nil {
		return 0
	}
	return model.Recommendation.Position
}

func modelQueryWindow(query LLMModelQuery, matched int) (int, int) {
	if query.All {
		return 0, matched
	}
	return query.Offset, query.Limit
}

func (s *LLMService) modelEnrichment(ctx context.Context, provider llm.Provider, query LLMModelQuery) (llm.ModelEnrichmentResult, error) {
	request := llm.ModelEnrichmentRequest{Rank: query.Rank, RankWindow: query.RankWindow, RecommendFor: query.RecommendFor}
	key := strings.Join([]string{query.Rank, query.RankWindow, strings.ToLower(query.RecommendFor)}, "\x00")
	if s != nil && s.enrichCache != nil {
		if value, ok := s.enrichCache.get(provider, key, time.Now()); ok {
			return value, nil
		}
	}
	enricher, ok := s.llmClient().(llm.ModelEnricher)
	if !ok {
		return llm.ModelEnrichmentResult{}, llm.NewError(llm.ErrorUnsupported, "query", "provider backend does not support ranking enrichment")
	}
	value, err := enricher.EnrichModels(ctx, provider, request)
	if err != nil {
		return llm.ModelEnrichmentResult{}, err
	}
	if s != nil && s.enrichCache != nil {
		s.enrichCache.put(provider, key, value, time.Now())
	}
	return cloneModelEnrichment(value), nil
}

func applyModelEnrichment(models []llm.Model, enrichment llm.ModelEnrichmentResult) {
	for index := range models {
		keys := []string{models[index].ID, models[index].CanonicalSlug}
		for _, key := range keys {
			if key == "" {
				continue
			}
			if rank, ok := enrichment.Ranks[key]; ok {
				value := rank
				models[index].Rank = &value
				break
			}
		}
		for _, key := range keys {
			if key == "" {
				continue
			}
			if recommendation, ok := enrichment.Recommendations[key]; ok {
				value := recommendation
				models[index].Recommendation = &value
				break
			}
		}
	}
}

func (c *llmModelEnrichmentCache) get(provider llm.Provider, key string, now time.Time) (llm.ModelEnrichmentResult, bool) {
	if c == nil {
		return llm.ModelEnrichmentResult{}, false
	}
	cacheKey := string(provider.ID) + "\x00" + key
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[cacheKey]
	if !ok || entry.fingerprint != llmModelCatalogFingerprint(provider) || now.After(entry.expiresAt) {
		delete(c.entries, cacheKey)
		return llm.ModelEnrichmentResult{}, false
	}
	c.clock++
	entry.access = c.clock
	c.entries[cacheKey] = entry
	return cloneModelEnrichment(entry.value), true
}

func (c *llmModelEnrichmentCache) put(provider llm.Provider, key string, value llm.ModelEnrichmentResult, now time.Time) {
	if c == nil {
		return
	}
	cacheKey := string(provider.ID) + "\x00" + key
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[cacheKey]; !exists && len(c.entries) >= maxLLMModelEnrichmentCache {
		oldestKey := ""
		var oldest uint64
		first := true
		for candidate, entry := range c.entries {
			if first || entry.access < oldest {
				oldestKey, oldest, first = candidate, entry.access, false
			}
		}
		delete(c.entries, oldestKey)
	}
	c.clock++
	c.entries[cacheKey] = llmModelEnrichmentCacheEntry{providerID: provider.ID, fingerprint: llmModelCatalogFingerprint(provider), key: key, value: cloneModelEnrichment(value), expiresAt: now.Add(llmModelEnrichmentCacheTTL), access: c.clock}
}

func (c *llmModelEnrichmentCache) delete(id llm.ProviderID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if entry.providerID == id {
			delete(c.entries, key)
		}
	}
}
func (c *llmModelEnrichmentCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

func cloneModelEnrichment(value llm.ModelEnrichmentResult) llm.ModelEnrichmentResult {
	result := value
	if value.Ranks != nil {
		result.Ranks = make(map[string]llm.ModelRankMetadata, len(value.Ranks))
		for key, item := range value.Ranks {
			result.Ranks[key] = item
		}
	}
	if value.Recommendations != nil {
		result.Recommendations = make(map[string]llm.ModelRecommendation, len(value.Recommendations))
		for key, item := range value.Recommendations {
			result.Recommendations[key] = item
		}
	}
	return result
}

func ParseLLMModelSort(raw string) (LLMModelSort, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) == 0 || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		return LLMModelSort{}, fmt.Errorf("sort must be field[:asc|desc]")
	}
	direction := "asc"
	if len(parts) == 2 {
		direction = strings.ToLower(strings.TrimSpace(parts[1]))
	}
	if direction != "asc" && direction != "desc" {
		return LLMModelSort{}, fmt.Errorf("sort direction must be asc or desc")
	}
	return LLMModelSort{Field: normalizeQueryKey(parts[0]), Direction: direction}, nil
}

func ParseLLMModelRange(raw string) (*LLMModelRange, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("range must be start:end")
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return nil, fmt.Errorf("range start must be an integer")
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, fmt.Errorf("range end must be an integer")
	}
	return &LLMModelRange{Start: start, End: end}, nil
}
