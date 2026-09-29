package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponseFormat struct {
	Type       string                 `json:"type"`
	JSONSchema openAIJSONSchemaFormat `json:"json_schema"`
}

type openAIJSONSchemaFormat struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openAIChatRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	MaxTokens      int                   `json:"max_tokens"`
	Temperature    *float64              `json:"temperature,omitempty"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
}

type openAIModelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (c *Client) inferOpenAI(ctx context.Context, provider Provider, request Request) (Result, error) {
	credential, err := c.credentialFor(ctx, provider, AuthBearer)
	if err != nil {
		return Result{}, err
	}
	messages := make([]openAIMessage, 0, len(request.Messages)+1)
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append(messages, openAIMessage{Role: string(RoleSystem), Content: request.Instructions})
	}
	for _, message := range request.Messages {
		messages = append(messages, openAIMessage{Role: string(message.Role), Content: message.Content})
	}
	payload := openAIChatRequest{
		Model: provider.Model, Messages: messages, MaxTokens: request.MaxOutputTokens, Temperature: request.Temperature,
	}
	if len(request.ResponseSchema) > 0 {
		payload.ResponseFormat = &openAIResponseFormat{
			Type:       "json_schema",
			JSONSchema: openAIJSONSchemaFormat{Name: "codemcp_response", Strict: true, Schema: request.ResponseSchema},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, NewError(ErrorInvalidRequest, "request", "failed to encode OpenAI-compatible request")
	}
	headers := make(http.Header)
	if credential != "" {
		headers.Set("Authorization", "Bearer "+credential)
	}
	raw, err := c.doJSON(ctx, http.MethodPost, provider.BaseURL+"/chat/completions", body, headers)
	if err != nil {
		return Result{}, err
	}
	var response openAIChatResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return Result{}, err
	}
	if len(response.Choices) == 0 {
		return Result{}, NewError(ErrorInvalidResponse, "choices", "provider response did not include a completion choice")
	}
	text := response.Choices[0].Message.Content
	model, err := normalizedResultModel(provider.Model, response.Model)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Text: text, ProviderID: provider.ID, Model: model,
		FinishReason: strings.TrimSpace(response.Choices[0].FinishReason),
	}
	if response.Usage != nil {
		result.Usage, err = normalizedUsage(response.Usage.PromptTokens, response.Usage.CompletionTokens)
		if err != nil {
			return Result{}, err
		}
	}
	if len(request.ResponseSchema) > 0 {
		structured := json.RawMessage(strings.TrimSpace(text))
		if !json.Valid(structured) {
			return Result{}, NewError(ErrorInvalidResponse, "structured", "provider returned invalid structured JSON")
		}
		result.Structured = append(json.RawMessage(nil), structured...)
	}
	return result, nil
}

func (c *Client) discoverOpenAIModels(ctx context.Context, provider Provider) ([]Model, error) {
	credential, err := c.credentialFor(ctx, provider, AuthBearer)
	if err != nil {
		return nil, err
	}
	headers := make(http.Header)
	if credential != "" {
		headers.Set("Authorization", "Bearer "+credential)
	}
	raw, err := c.doJSON(ctx, http.MethodGet, provider.BaseURL+"/models", nil, headers)
	if err != nil {
		return nil, err
	}
	var response openAIModelListResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return nil, err
	}
	if len(response.Data) > MaxDiscoveredModels {
		return nil, NewError(ErrorInvalidResponse, "models", "provider returned too many models")
	}
	models := make([]Model, 0, len(response.Data))
	seen := make(map[string]struct{}, len(response.Data))
	for _, item := range response.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || len(id) > MaxModelIDBytes {
			return nil, NewError(ErrorInvalidResponse, "models", "provider returned an invalid model id")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, Model{ID: id, Name: id})
	}
	return models, nil
}
