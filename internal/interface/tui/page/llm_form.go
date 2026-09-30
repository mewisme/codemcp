package page

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type llmProviderFormData struct {
	ID        string
	Name      string
	Protocol  string
	BaseURL   string
	Model     string
	AuthMode  string
	Discovery string
}

func newLLMProviderEditor(provider *application.LLMProviderResult) (component.Editor, *llmProviderFormData) {
	data := &llmProviderFormData{Protocol: "openai", AuthMode: "none", Discovery: "none"}
	primary, title, description := "create", "Add provider", "Add an OpenAI-compatible or Anthropic-compatible custom provider."
	if provider != nil {
		data.ID, data.Name, data.Protocol, data.BaseURL, data.Model = string(provider.ID), provider.Name, string(provider.Protocol), provider.BaseURL, provider.Model
		data.AuthMode, data.Discovery = string(provider.AuthMode), string(provider.Discovery)
		primary, title, description = "save", "Edit provider", string(provider.ID)+" · custom provider configuration"
	}
	idTitle := "Provider ID"
	if provider != nil {
		idTitle = "Provider ID (immutable)"
	}
	idField := component.Input(idTitle, &data.ID).Validate(func(value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("provider ID is required")
		}
		return nil
	})
	form := component.NewEditorForm(component.Group(
		idField,
		component.Input("Name", &data.Name),
		component.Select("Protocol", &data.Protocol,
			huh.NewOption("OpenAI compatible", "openai"),
			huh.NewOption("Anthropic compatible", "anthropic")),
		component.Input("Base URL", &data.BaseURL).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("base URL is required")
			}
			return nil
		}),
		component.Input("Model", &data.Model),
		component.Select("Authentication", &data.AuthMode,
			huh.NewOption("None", "none"),
			huh.NewOption("Bearer", "bearer"),
			huh.NewOption("x-api-key", "x-api-key")),
		component.Select("Model discovery", &data.Discovery,
			huh.NewOption("None", "none"),
			huh.NewOption("OpenAI /models", "openai-models"),
			huh.NewOption("Ollama /api/tags", "ollama-tags")),
	))
	editor := component.NewEditor(primary, component.EditorSection{ID: "provider", Title: title, Description: description, Form: form})
	return editor, data
}

type llmCredentialFormData struct{ Value string }

func newLLMCredentialEditor(provider application.LLMProviderResult) (component.Editor, *llmCredentialFormData) {
	data := &llmCredentialFormData{}
	hint := "not configured"
	if provider.Credential.Configured {
		hint = provider.Credential.Preview
	}
	form := component.NewEditorForm(component.Group(
		component.PasswordInputWithHint("New API key", hint, &data.Value).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("API key is required")
			}
			return nil
		}),
	))
	editor := component.NewEditor("save", component.EditorSection{
		ID: "credential", Title: "API key", Description: string(provider.ID) + " · the raw value is never rendered or retained in TUI notice/history state.", Form: form,
	})
	return editor, data
}

type llmModelFormData struct{ Model string }

func newLLMModelEditor(provider application.LLMProviderResult) (component.Editor, *llmModelFormData) {
	data := &llmModelFormData{Model: provider.Model}
	form := component.NewEditorForm(component.Group(
		component.Input("Model ID", &data.Model).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("model ID is required")
			}
			return nil
		}),
	))
	editor := component.NewEditor("save", component.EditorSection{
		ID: "model", Title: "Model", Description: string(provider.ID) + " · set an exact model ID, including IDs not present in the current discovery page.", Form: form,
	})
	return editor, data
}

type llmModelQueryFormData struct {
	Search             string
	ExactIDs           string
	Price              string
	Authors            string
	MinContext         string
	MaxContext         string
	MinPromptPrice     string
	MaxPromptPrice     string
	MinCompletionPrice string
	MaxCompletionPrice string
	Capabilities       string
	Parameters         string
	InputModalities    string
	OutputModalities   string
	CreatedAfter       string
	CreatedBefore      string
	ModifiedAfter      string
	ModifiedBefore     string
	Families           string
	Formats            string
	Quantizations      string
	MinParameters      string
	MaxParameters      string
	MinSize            string
	MaxSize            string
	Sort               string
	Rank               string
	RankWindow         string
	RecommendFor       string
	PageMode           string
	Range              string
	Offset             string
	Limit              string
}

func newLLMModelQueryEditor(query application.LLMModelQuery) (component.Editor, *llmModelQueryFormData) {
	data := llmModelQueryFormDataFromQuery(query)
	filterForm := component.NewEditorForm(component.Group(
		component.Input("Search", &data.Search),
		component.Input("Exact model IDs (comma-separated)", &data.ExactIDs),
		component.Select("Price", &data.Price, huh.NewOption("All", "all"), huh.NewOption("Free", "free"), huh.NewOption("Paid", "paid")),
		component.Input("Authors (comma-separated)", &data.Authors),
		component.Input("Min context", &data.MinContext),
		component.Input("Max context", &data.MaxContext),
		component.Input("Min prompt price", &data.MinPromptPrice),
		component.Input("Max prompt price", &data.MaxPromptPrice),
		component.Input("Min completion price", &data.MinCompletionPrice),
		component.Input("Max completion price", &data.MaxCompletionPrice),
		component.Input("Capabilities (comma-separated)", &data.Capabilities),
		component.Input("Supported parameters (comma-separated)", &data.Parameters),
		component.Input("Input modalities (comma-separated)", &data.InputModalities),
		component.Input("Output modalities (comma-separated)", &data.OutputModalities),
		component.Input("Created after (RFC3339)", &data.CreatedAfter),
		component.Input("Created before (RFC3339)", &data.CreatedBefore),
		component.Input("Modified after (RFC3339)", &data.ModifiedAfter),
		component.Input("Modified before (RFC3339)", &data.ModifiedBefore),
	))
	ollamaForm := component.NewEditorForm(component.Group(
		component.Input("Families (comma-separated)", &data.Families),
		component.Input("Formats (comma-separated)", &data.Formats),
		component.Input("Quantizations (comma-separated)", &data.Quantizations),
		component.Input("Min parameter count", &data.MinParameters),
		component.Input("Max parameter count", &data.MaxParameters),
		component.Input("Min size (bytes)", &data.MinSize),
		component.Input("Max size (bytes)", &data.MaxSize),
	))
	orderForm := component.NewEditorForm(component.Group(
		component.Input("Sort (field:asc|desc)", &data.Sort),
		component.Input("Rank", &data.Rank),
		component.Input("Rank window", &data.RankWindow),
		component.Input("Recommend for", &data.RecommendFor),
		component.Select("Result mode", &data.PageMode,
			huh.NewOption("Page", "page"),
			huh.NewOption("Range", "range"),
			huh.NewOption("All matches", "all"),
			huh.NewOption("Count only", "count")),
		component.Input("Range (start:end)", &data.Range),
		component.Input("Offset", &data.Offset).Validate(validateNonNegativeInt("offset")),
		component.Input("Limit", &data.Limit).Validate(validateLLMPositiveInt("limit")),
	))
	editor := component.NewEditor("apply",
		component.EditorSection{ID: "filters", Title: "Filters", Description: "Search and filter against the canonical model catalog.", Form: filterForm},
		component.EditorSection{ID: "ollama", Title: "Ollama", Description: "Provider-specific model metadata filters.", Form: ollamaForm},
		component.EditorSection{ID: "order", Title: "Sort, rank & page", Description: "Use the canonical sort/rank/recommendation and pagination contract.", Form: orderForm},
	)
	return editor, &data
}

func llmModelQueryFormDataFromQuery(query application.LLMModelQuery) llmModelQueryFormData {
	data := llmModelQueryFormData{
		Search: query.Search, ExactIDs: strings.Join(query.ExactIDs, ","), Price: "all", Authors: strings.Join(query.Authors, ","), MinPromptPrice: query.MinPromptPrice,
		MaxPromptPrice: query.MaxPromptPrice, MinCompletionPrice: query.MinCompletionPrice, MaxCompletionPrice: query.MaxCompletionPrice,
		Capabilities: strings.Join(query.Capabilities, ","), Parameters: strings.Join(query.Parameters, ","), InputModalities: strings.Join(query.InputModalities, ","),
		OutputModalities: strings.Join(query.OutputModalities, ","), Families: strings.Join(query.Ollama.Families, ","), Formats: strings.Join(query.Ollama.Formats, ","),
		Quantizations: strings.Join(query.Ollama.Quantizations, ","), Rank: query.Rank, RankWindow: query.RankWindow, RecommendFor: query.RecommendFor,
		PageMode: "page", Offset: strconv.Itoa(query.Offset), Limit: strconv.Itoa(query.Limit),
	}
	if query.All {
		data.PageMode = "all"
	} else if query.CountOnly {
		data.PageMode = "count"
	} else if query.Range != nil {
		data.PageMode = "range"
	}
	if query.Range != nil {
		data.Range = fmt.Sprintf("%d:%d", query.Range.Start, query.Range.End)
	}
	if query.Free != nil {
		if *query.Free {
			data.Price = "free"
		} else {
			data.Price = "paid"
		}
	}
	if query.MinContext != nil {
		data.MinContext = strconv.Itoa(*query.MinContext)
	}
	if query.MaxContext != nil {
		data.MaxContext = strconv.Itoa(*query.MaxContext)
	}
	if query.CreatedAfter != nil {
		data.CreatedAfter = query.CreatedAfter.Format(time.RFC3339)
	}
	if query.CreatedBefore != nil {
		data.CreatedBefore = query.CreatedBefore.Format(time.RFC3339)
	}
	if query.ModifiedAfter != nil {
		data.ModifiedAfter = query.ModifiedAfter.Format(time.RFC3339)
	}
	if query.ModifiedBefore != nil {
		data.ModifiedBefore = query.ModifiedBefore.Format(time.RFC3339)
	}
	if query.Ollama.MinParameterCount != nil {
		data.MinParameters = strconv.FormatInt(*query.Ollama.MinParameterCount, 10)
	}
	if query.Ollama.MaxParameterCount != nil {
		data.MaxParameters = strconv.FormatInt(*query.Ollama.MaxParameterCount, 10)
	}
	if query.Ollama.MinSizeBytes != nil {
		data.MinSize = strconv.FormatInt(*query.Ollama.MinSizeBytes, 10)
	}
	if query.Ollama.MaxSizeBytes != nil {
		data.MaxSize = strconv.FormatInt(*query.Ollama.MaxSizeBytes, 10)
	}
	if len(query.Sort) > 0 {
		parts := make([]string, 0, len(query.Sort))
		for _, sort := range query.Sort {
			parts = append(parts, sort.Field+":"+sort.Direction)
		}
		data.Sort = strings.Join(parts, ",")
	}
	return data
}

func (data *llmModelQueryFormData) Query() (application.LLMModelQuery, error) {
	if data == nil {
		return application.LLMModelQuery{}, fmt.Errorf("model query is required")
	}
	query := application.LLMModelQuery{
		Search: strings.TrimSpace(data.Search), ExactIDs: splitCSV(data.ExactIDs), Authors: splitCSV(data.Authors), MinPromptPrice: strings.TrimSpace(data.MinPromptPrice),
		MaxPromptPrice: strings.TrimSpace(data.MaxPromptPrice), MinCompletionPrice: strings.TrimSpace(data.MinCompletionPrice),
		MaxCompletionPrice: strings.TrimSpace(data.MaxCompletionPrice), Capabilities: splitCSV(data.Capabilities), Parameters: splitCSV(data.Parameters),
		InputModalities: splitCSV(data.InputModalities), OutputModalities: splitCSV(data.OutputModalities), Rank: strings.TrimSpace(data.Rank),
		RankWindow: strings.TrimSpace(data.RankWindow), RecommendFor: strings.TrimSpace(data.RecommendFor),
		Ollama: application.LLMOllamaModelQuery{Families: splitCSV(data.Families), Formats: splitCSV(data.Formats), Quantizations: splitCSV(data.Quantizations)},
	}
	switch data.Price {
	case "free":
		value := true
		query.Free = &value
	case "paid":
		value := false
		query.Free = &value
	}
	var err error
	if query.MinContext, err = parseOptionalInt(data.MinContext); err != nil {
		return query, fmt.Errorf("min context: %w", err)
	}
	if query.MaxContext, err = parseOptionalInt(data.MaxContext); err != nil {
		return query, fmt.Errorf("max context: %w", err)
	}
	if query.CreatedAfter, err = parseOptionalTime(data.CreatedAfter); err != nil {
		return query, fmt.Errorf("created after: %w", err)
	}
	if query.CreatedBefore, err = parseOptionalTime(data.CreatedBefore); err != nil {
		return query, fmt.Errorf("created before: %w", err)
	}
	if query.ModifiedAfter, err = parseOptionalTime(data.ModifiedAfter); err != nil {
		return query, fmt.Errorf("modified after: %w", err)
	}
	if query.ModifiedBefore, err = parseOptionalTime(data.ModifiedBefore); err != nil {
		return query, fmt.Errorf("modified before: %w", err)
	}
	if query.Ollama.MinParameterCount, err = parseOptionalInt64(data.MinParameters); err != nil {
		return query, fmt.Errorf("min parameters: %w", err)
	}
	if query.Ollama.MaxParameterCount, err = parseOptionalInt64(data.MaxParameters); err != nil {
		return query, fmt.Errorf("max parameters: %w", err)
	}
	if query.Ollama.MinSizeBytes, err = parseOptionalInt64(data.MinSize); err != nil {
		return query, fmt.Errorf("min size: %w", err)
	}
	if query.Ollama.MaxSizeBytes, err = parseOptionalInt64(data.MaxSize); err != nil {
		return query, fmt.Errorf("max size: %w", err)
	}
	if query.Sort, err = parseLLMSorts(data.Sort); err != nil {
		return query, err
	}
	switch strings.TrimSpace(data.PageMode) {
	case "", "page":
		if query.Offset, err = parseIntDefault(data.Offset, 0); err != nil || query.Offset < 0 {
			if err == nil {
				err = fmt.Errorf("must be zero or greater")
			}
			return query, fmt.Errorf("offset: %w", err)
		}
		if query.Limit, err = parseIntDefault(data.Limit, 25); err != nil || query.Limit <= 0 {
			if err == nil {
				err = fmt.Errorf("must be greater than zero")
			}
			return query, fmt.Errorf("limit: %w", err)
		}
	case "range":
		if strings.TrimSpace(data.Range) == "" {
			return query, fmt.Errorf("range is required in range mode")
		}
		if query.Range, err = application.ParseLLMModelRange(data.Range); err != nil {
			return query, fmt.Errorf("range: %w", err)
		}
	case "all":
		query.All = true
	case "count":
		if len(query.Sort) > 0 || query.Rank != "" || query.RecommendFor != "" {
			return query, fmt.Errorf("count-only mode cannot sort, rank, or request recommendations")
		}
		query.CountOnly = true
	default:
		return query, fmt.Errorf("unsupported result mode %q", data.PageMode)
	}
	return query, nil
}

func splitCSV(value string) []string {
	values := strings.Split(value, ",")
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func parseOptionalInt(value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		if err == nil {
			err = fmt.Errorf("must be zero or greater")
		}
		return nil, err
	}
	return &n, nil
}

func parseOptionalInt64(value string) (*int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		if err == nil {
			err = fmt.Errorf("must be zero or greater")
		}
		return nil, err
	}
	return &n, nil
}

func parseOptionalTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseLLMSorts(value string) ([]application.LLMModelSort, error) {
	values := splitCSV(value)
	sorts := make([]application.LLMModelSort, 0, len(values))
	for _, value := range values {
		parts := strings.SplitN(value, ":", 2)
		field := strings.TrimSpace(parts[0])
		direction := "asc"
		if len(parts) == 2 {
			direction = strings.ToLower(strings.TrimSpace(parts[1]))
		}
		if field == "" || direction != "asc" && direction != "desc" {
			return nil, fmt.Errorf("sort must use field:asc or field:desc")
		}
		sorts = append(sorts, application.LLMModelSort{Field: field, Direction: direction})
	}
	return sorts, nil
}

func parseIntDefault(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}

func validateNonNegativeInt(label string) func(string) error {
	return func(value string) error {
		n, err := parseIntDefault(value, 0)
		if err != nil || n < 0 {
			if err == nil {
				err = fmt.Errorf("must be zero or greater")
			}
			return fmt.Errorf("%s %w", label, err)
		}
		return nil
	}
}

func validateLLMPositiveInt(label string) func(string) error {
	return func(value string) error {
		n, err := parseIntDefault(value, 25)
		if err != nil || n <= 0 {
			if err == nil {
				err = fmt.Errorf("must be greater than zero")
			}
			return fmt.Errorf("%s %w", label, err)
		}
		return nil
	}
}
