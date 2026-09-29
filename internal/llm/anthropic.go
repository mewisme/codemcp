package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicMessageRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
}

type anthropicMessageResponse struct {
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

func (c *Client) inferAnthropic(ctx context.Context, provider Provider, request Request) (Result, error) {
	credential, err := c.credentialFor(ctx, provider, AuthAPIKey)
	if err != nil {
		return Result{}, err
	}
	systemParts := make([]string, 0, 2)
	if strings.TrimSpace(request.Instructions) != "" {
		systemParts = append(systemParts, request.Instructions)
	}
	messages := make([]anthropicMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == RoleSystem {
			systemParts = append(systemParts, message.Content)
			continue
		}
		messages = append(messages, anthropicMessage{Role: string(message.Role), Content: message.Content})
	}
	if len(messages) == 0 {
		return Result{}, NewError(ErrorInvalidRequest, "messages", "Anthropic-compatible requests require at least one user or assistant message")
	}
	payload := anthropicMessageRequest{
		Model: provider.Model, MaxTokens: request.MaxOutputTokens,
		System: strings.Join(systemParts, "\n\n"), Messages: messages, Temperature: request.Temperature,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, NewError(ErrorInvalidRequest, "request", "failed to encode Anthropic-compatible request")
	}
	headers := make(http.Header)
	headers.Set("anthropic-version", AnthropicAPIVersion)
	if credential != "" {
		headers.Set("x-api-key", credential)
	}
	raw, err := c.doJSON(ctx, http.MethodPost, provider.BaseURL+"/messages", body, headers)
	if err != nil {
		return Result{}, err
	}
	var response anthropicMessageResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return Result{}, err
	}
	textParts := make([]string, 0, len(response.Content))
	for _, block := range response.Content {
		if block.Type == "text" {
			textParts = append(textParts, block.Text)
		}
	}
	if len(textParts) == 0 {
		return Result{}, NewError(ErrorInvalidResponse, "content", "provider response did not include a text block")
	}
	model, err := normalizedResultModel(provider.Model, response.Model)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Text: strings.Join(textParts, ""), ProviderID: provider.ID, Model: model,
		FinishReason: strings.TrimSpace(response.StopReason),
	}
	if response.Usage != nil {
		result.Usage, err = normalizedUsage(response.Usage.InputTokens, response.Usage.OutputTokens)
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}
