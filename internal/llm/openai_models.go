package llm

import (
	"sort"
	"strings"
	"time"
)

const maxSupportedParameters = 128

func modelFromOpenAICompatibleItem(item openAIModelItem) (Model, error) {
	id := strings.TrimSpace(item.ID)
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = id
	}
	if len(name) > MaxModelNameBytes {
		return Model{}, NewError(ErrorInvalidResponse, "models", "provider returned an invalid model name")
	}
	model := Model{ID: id, Name: name, CanonicalSlug: strings.TrimSpace(item.CanonicalSlug)}
	if slash := strings.IndexByte(id, '/'); slash > 0 {
		model.Author = id[:slash]
	}
	if item.ContextLength != nil {
		if *item.ContextLength < 0 {
			return Model{}, NewError(ErrorInvalidResponse, "models", "provider returned an invalid context length")
		}
		model.ContextLength = *item.ContextLength
		model.ContextLengthKnown = true
	}
	if item.Pricing != nil {
		model.PromptPrice = strings.TrimSpace(item.Pricing.Prompt)
		model.CompletionPrice = strings.TrimSpace(item.Pricing.Completion)
		model.PricingKnown = model.PromptPrice != "" || model.CompletionPrice != ""
		model.FreeKnown = model.PricingKnown
		model.Free = model.PricingKnown && isZeroPrice(model.PromptPrice) && isZeroPrice(model.CompletionPrice)
	}
	model.SupportedParameters = normalizeSupportedParameters(item.SupportedParameters)
	if item.SupportedParameters != nil {
		model.CapabilitiesKnown = true
	}
	model.SupportsStructuredOutput = containsParameter(item.SupportedParameters, "structured_outputs")
	model.Capabilities = capabilitiesFromSupportedParameters(item.SupportedParameters)
	if item.Architecture != nil {
		model.ModalitiesKnown = true
		model.InputModalities = normalizeModelMetadataList(item.Architecture.InputModalities, 32)
		model.OutputModalities = normalizeModelMetadataList(item.Architecture.OutputModalities, 32)
	}
	if item.Created != nil && *item.Created >= 0 {
		created := time.Unix(*item.Created, 0).UTC()
		model.CreatedAt = &created
	}
	if item.TopProvider != nil && item.TopProvider.MaxCompletionTokens != nil {
		if *item.TopProvider.MaxCompletionTokens < 0 {
			return Model{}, NewError(ErrorInvalidResponse, "models", "provider returned an invalid max output token count")
		}
		model.MaxOutputTokens = *item.TopProvider.MaxCompletionTokens
		model.MaxOutputTokensKnown = true
	}
	return model, nil
}

func capabilitiesFromSupportedParameters(parameters []string) []string {
	set := map[string]struct{}{}
	for _, parameter := range parameters {
		switch parameter {
		case "structured_outputs":
			set["structured-output"] = struct{}{}
		case "tools":
			set["tools"] = struct{}{}
		case "reasoning":
			set["reasoning"] = struct{}{}
		case "web_search":
			set["web-search"] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeModelMetadataList(values []string, limit int) []string {
	if len(values) == 0 || limit <= 0 {
		return nil
	}
	result := make([]string, 0, min(len(values), limit))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" || len(value) > 64 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result
}

func normalizeSupportedParameters(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, min(len(values), maxSupportedParameters))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == maxSupportedParameters {
			break
		}
	}
	return result
}

func containsParameter(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func isZeroPrice(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, char := range value {
		if char != '0' && char != '.' {
			return false
		}
	}
	return true
}
