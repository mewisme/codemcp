package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	DefaultInferenceTimeout  = 30 * time.Second
	DefaultMaxRequestBytes   = 1 << 20
	DefaultMaxResponseBytes  = 4 << 20
	DefaultMaxOutputTokens   = 1024
	MaxInferenceOutputTokens = 128 * 1024
	MaxDiscoveredModels      = 4096
	AnthropicAPIVersion      = "2023-06-01"
)

type CredentialResolver func(context.Context, ProviderID) (string, error)

type AdapterCapabilities struct {
	StructuredOutput bool `json:"structured_output"`
	ModelDiscovery   bool `json:"model_discovery"`
}

type CapabilityReporter interface {
	Capabilities(Provider) AdapterCapabilities
}

type ClientOptions struct {
	HTTPClient       *http.Client
	Credential       CredentialResolver
	Timeout          time.Duration
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

type Client struct {
	httpClient       *http.Client
	credential       CredentialResolver
	timeout          time.Duration
	maxRequestBytes  int64
	maxResponseBytes int64
}

var (
	_ InferenceClient    = (*Client)(nil)
	_ ModelDiscoverer    = (*Client)(nil)
	_ CapabilityReporter = (*Client)(nil)
)

func NewClient(options ClientOptions) *Client {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultInferenceTimeout
	}
	maxRequestBytes := options.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = DefaultMaxRequestBytes
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	return &Client{
		httpClient: httpClient, credential: options.Credential, timeout: timeout,
		maxRequestBytes: maxRequestBytes, maxResponseBytes: maxResponseBytes,
	}
}

func (c *Client) Capabilities(provider Provider) AdapterCapabilities {
	return AdapterCapabilities{
		StructuredOutput: provider.Protocol == ProtocolOpenAI && provider.Capabilities != nil && provider.Capabilities.StructuredOutput,
		ModelDiscovery:   provider.Protocol == ProtocolOpenAI && provider.Discovery == DiscoveryOpenAIModels,
	}
}

func (c *Client) Infer(ctx context.Context, provider Provider, request Request) (result Result, resultErr error) {
	if c == nil {
		return Result{}, NewError(ErrorUnavailable, "", "inference client is unavailable")
	}
	provider, err := normalizeProvider(provider, true)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(provider.Model) == "" {
		return Result{}, NewError(ErrorMisconfigured, "model", "provider model is not configured")
	}
	request, err = normalizeInferenceRequest(request)
	if err != nil {
		return Result{}, err
	}
	if len(request.ResponseSchema) > 0 && !c.Capabilities(provider).StructuredOutput {
		return Result{}, NewError(ErrorUnsupported, "response_schema", "provider does not declare structured-output support")
	}

	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	span := tracepkg.Start(ctx, "LLM", "llm.inference", "Running LLM inference",
		tracepkg.String("provider_id", string(provider.ID)),
		tracepkg.String("protocol", string(provider.Protocol)),
		tracepkg.String("model", provider.Model),
		tracepkg.Int("messages", len(request.Messages)),
		tracepkg.Bool("structured_output", len(request.ResponseSchema) > 0),
	)
	defer func() {
		if resultErr != nil {
			fields := []tracepkg.Field{}
			if typed, ok := AsError(resultErr); ok {
				fields = append(fields, tracepkg.String("error_category", string(typed.Category)))
			}
			span.FailMessage("LLM inference failed", resultErr, fields...)
			return
		}
		fields := []tracepkg.Field{
			tracepkg.String("provider_id", string(result.ProviderID)),
			tracepkg.String("model", provider.Model),
		}
		if result.Usage != nil {
			fields = append(fields,
				tracepkg.Int("input_tokens", result.Usage.InputTokens),
				tracepkg.Int("output_tokens", result.Usage.OutputTokens),
			)
		}
		span.EndMessage("LLM inference completed", fields...)
	}()

	switch provider.Protocol {
	case ProtocolOpenAI:
		result, resultErr = c.inferOpenAI(ctx, provider, request)
	case ProtocolAnthropic:
		result, resultErr = c.inferAnthropic(ctx, provider, request)
	default:
		resultErr = NewError(ErrorUnsupported, "protocol", "provider protocol is not supported")
	}
	return result, resultErr
}

func (c *Client) DiscoverModels(ctx context.Context, provider Provider) (models []Model, resultErr error) {
	if c == nil {
		return nil, NewError(ErrorUnavailable, "", "inference client is unavailable")
	}
	provider, err := normalizeProvider(provider, true)
	if err != nil {
		return nil, err
	}
	if !c.Capabilities(provider).ModelDiscovery {
		return nil, NewError(ErrorUnsupported, "discovery", "provider does not support OpenAI-compatible model discovery")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	span := tracepkg.Start(ctx, "LLM", "llm.models.discover", "Discovering LLM models",
		tracepkg.String("provider_id", string(provider.ID)),
		tracepkg.String("protocol", string(provider.Protocol)),
		tracepkg.String("discovery", string(provider.Discovery)),
	)
	defer func() {
		if resultErr != nil {
			fields := []tracepkg.Field{}
			if typed, ok := AsError(resultErr); ok {
				fields = append(fields, tracepkg.String("error_category", string(typed.Category)))
			}
			span.FailMessage("LLM model discovery failed", resultErr, fields...)
			return
		}
		span.EndMessage("LLM model discovery completed", tracepkg.Int("models", len(models)))
	}()
	return c.discoverOpenAIModels(ctx, provider)
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, c.timeout)
}

func (c *Client) credentialFor(ctx context.Context, provider Provider, expected AuthMode) (string, error) {
	if provider.AuthMode == AuthNone {
		return "", nil
	}
	if provider.AuthMode != expected {
		return "", NewError(ErrorMisconfigured, "auth_mode", fmt.Sprintf("protocol %q requires auth mode %q or %q", provider.Protocol, expected, AuthNone))
	}
	if c.credential == nil {
		return "", NewError(ErrorMisconfigured, "api_key", "provider credential is not configured")
	}
	credential, err := c.credential(ctx, provider.ID)
	if err != nil {
		if typed, ok := AsError(err); ok {
			return "", NewError(typed.Category, "api_key", "provider credential could not be loaded")
		}
		return "", NewError(ErrorUnavailable, "api_key", "provider credential could not be loaded")
	}
	if strings.TrimSpace(credential) == "" {
		return "", NewError(ErrorMisconfigured, "api_key", "provider credential is not configured")
	}
	return strings.TrimSpace(credential), nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, body []byte, headers http.Header) ([]byte, error) {
	if int64(len(body)) > c.maxRequestBytes {
		return nil, NewError(ErrorInvalidRequest, "request", "serialized request exceeds size limit")
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, NewError(ErrorInvalidRequest, "request", "failed to construct provider request")
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, classifyTransportError(ctx, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, NewError(ErrorTransport, "response", "failed to read provider response")
	}
	if int64(len(raw)) > c.maxResponseBytes {
		return nil, NewError(ErrorInvalidResponse, "response", "provider response exceeds size limit")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyHTTPStatus(resp.StatusCode)
	}
	return raw, nil
}

func normalizeInferenceRequest(request Request) (Request, error) {
	if len(request.Messages) == 0 {
		return Request{}, NewError(ErrorInvalidRequest, "messages", "at least one message is required")
	}
	for index, message := range request.Messages {
		switch message.Role {
		case RoleSystem, RoleUser, RoleAssistant:
		default:
			return Request{}, NewError(ErrorInvalidRequest, "messages", fmt.Sprintf("message %d uses unsupported role %q", index, message.Role))
		}
	}
	if request.MaxOutputTokens < 0 || request.MaxOutputTokens > MaxInferenceOutputTokens {
		return Request{}, NewError(ErrorInvalidRequest, "max_output_tokens", fmt.Sprintf("max output tokens must be between 0 and %d", MaxInferenceOutputTokens))
	}
	if request.MaxOutputTokens == 0 {
		request.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if request.Temperature != nil && (*request.Temperature < 0 || *request.Temperature > 2) {
		return Request{}, NewError(ErrorInvalidRequest, "temperature", "temperature must be between 0 and 2")
	}
	if len(request.ResponseSchema) > 0 {
		if !json.Valid(request.ResponseSchema) {
			return Request{}, NewError(ErrorInvalidRequest, "response_schema", "response schema must be valid JSON")
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(request.ResponseSchema, &object); err != nil || object == nil {
			return Request{}, NewError(ErrorInvalidRequest, "response_schema", "response schema must be a JSON object")
		}
		request.ResponseSchema = append(json.RawMessage(nil), request.ResponseSchema...)
	}
	return request, nil
}

func classifyTransportError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return NewError(ErrorCancelled, "", "provider request was cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return NewError(ErrorTimeout, "", "provider request timed out")
	}
	return NewError(ErrorTransport, "", "provider request failed")
}

func classifyHTTPStatus(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return NewError(ErrorUnauthorized, "", fmt.Sprintf("provider returned HTTP %d", status))
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return NewError(ErrorTimeout, "", fmt.Sprintf("provider returned HTTP %d", status))
	case http.StatusTooManyRequests:
		return NewError(ErrorRateLimited, "", fmt.Sprintf("provider returned HTTP %d", status))
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return NewError(ErrorInvalidRequest, "", fmt.Sprintf("provider returned HTTP %d", status))
	default:
		if status >= 500 {
			return NewError(ErrorUnavailable, "", fmt.Sprintf("provider returned HTTP %d", status))
		}
		return NewError(ErrorProvider, "", fmt.Sprintf("provider returned HTTP %d", status))
	}
}

func decodeJSONResponse(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(output); err != nil {
		return NewError(ErrorInvalidResponse, "response", "provider returned malformed JSON")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return NewError(ErrorInvalidResponse, "response", "provider returned trailing JSON data")
	}
	return nil
}

func normalizedUsage(inputTokens, outputTokens int) (*Usage, error) {
	if inputTokens < 0 || outputTokens < 0 {
		return nil, NewError(ErrorInvalidResponse, "usage", "provider returned negative token usage")
	}
	return &Usage{InputTokens: inputTokens, OutputTokens: outputTokens}, nil
}

func normalizedResultModel(configured, reported string) (string, error) {
	model := strings.TrimSpace(reported)
	if model == "" {
		model = strings.TrimSpace(configured)
	}
	if model == "" || len(model) > MaxModelIDBytes {
		return "", NewError(ErrorInvalidResponse, "model", "provider returned an invalid model id")
	}
	return model, nil
}
