package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/integrations/semantic"
)

const (
	maxSystemOneRequestBody  = 512 << 10
	maxSystemOneResponseBody = 256 << 10
	riskQuestionID           = "risk_class"
	riskCategory             = "command_execution"
)

type ClientOptions struct {
	HTTPClient *http.Client
	BaseURL    string
}

type Client struct {
	apiKey   string
	model    string
	endpoint string
	http     *http.Client
	timeout  time.Duration
}

type wireRequest struct {
	State     any                     `json:"state"`
	Model     string                  `json:"model"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireQuestion struct {
	Type         semantic.Primitive `json:"type"`
	Instructions any                `json:"instructions"`
	Criteria     any                `json:"criteria,omitempty"`
}

type wireResponse struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
	Usage   *wireUsage            `json:"usage"`
}

type wireUsage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

type wireAnswer struct {
	Type          semantic.Primitive `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

func NewClient(apiKey, model string, timeout time.Duration) (*Client, error) {
	return NewClientWithOptions(apiKey, model, timeout, ClientOptions{})
}

func NewClientWithOptions(apiKey, model string, timeout time.Duration, options ClientOptions) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	model = strings.TrimSpace(model)
	if apiKey == "" {
		return nil, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe API key is not configured")
	}
	if model == "" {
		return nil, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe model is not configured")
	}
	if timeout <= 0 {
		return nil, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe timeout is not configured")
	}
	baseURL := strings.TrimSpace(options.BaseURL)
	production := baseURL == ""
	if production {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return nil, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe base URL is invalid")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe base URL must not include a path, query, or fragment")
	}
	if production && (parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "api.typesafe.ai")) {
		return nil, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe production base URL is invalid")
	}
	if !production && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe test base URL must use http or https")
	}
	endpoint := strings.TrimRight(parsed.String(), "/") + SystemOnePath
	return &Client{
		apiKey: apiKey, model: model, endpoint: endpoint,
		http:    cloneSystemOneHTTPClient(options.HTTPClient, production),
		timeout: timeout,
	}, nil
}

func (client *Client) Evaluate(ctx context.Context, request semantic.Request) (semantic.Result, error) {
	if client == nil || client.http == nil {
		return semantic.Result{}, semantic.NewError(semantic.ErrorMisconfigured, "TypeSafe client is unavailable")
	}
	if err := semantic.ValidateRequest(request); err != nil {
		return semantic.Result{}, err
	}
	wire, err := encodeWireRequest(request, client.model)
	if err != nil {
		return semantic.Result{}, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return semantic.Result{}, semantic.NewError(semantic.ErrorInvalidRequest, "request cannot be encoded")
	}
	if len(body) > maxSystemOneRequestBody {
		return semantic.Result{}, semantic.NewError(semantic.ErrorInvalidRequest, "encoded request exceeds local TypeSafe limit")
	}
	started := time.Now()
	requestCtx, cancel := boundedClientContext(ctx, client.timeout)
	defer cancel()
	decoded, err := client.do(requestCtx, body)
	if err != nil {
		return semantic.Result{}, err
	}
	return decodeWireResponse(request, decoded, time.Since(started))
}

func (client *Client) ClassifyRisk(ctx context.Context, input semantic.RiskInput) (semantic.RiskAssessment, error) {
	if err := semantic.ValidateRiskInput(input); err != nil {
		return semantic.RiskAssessment{}, err
	}
	request := semantic.Request{
		Consumer: input.Consumer,
		State: map[string]any{
			"invocation": map[string]any{
				"operation": input.Invocation.Operation,
				"tool":      input.Invocation.Tool,
				"arguments": input.Invocation.Arguments,
			},
		},
		Questions: map[string]semantic.Question{
			riskQuestionID: {
				Type:         semantic.PrimitiveChoice,
				Instructions: "Classify the operational risk of the exact canonical invocation. Judge likely impact only; do not decide authorization, approval, or whether execution should proceed.",
				Choice: map[string]any{
					string(semantic.RiskLow):      "Routine and narrowly scoped; expected effects are limited, reversible, and unlikely to cause meaningful loss or exposure.",
					string(semantic.RiskMedium):   "Meaningful state mutation or external effect with bounded scope; mistakes may require manual recovery or affect user/project state.",
					string(semantic.RiskHigh):     "Broad, destructive, security-sensitive, privileged, or difficult-to-recover effects; plausible data loss, exposure, or significant external impact.",
					string(semantic.RiskCritical): "Severe or clearly dangerous effects such as systemic destruction, credential/security compromise, broad exfiltration, or irreversible high-impact damage.",
				},
			},
		},
	}
	result, err := client.Evaluate(ctx, request)
	if err != nil {
		return semantic.RiskAssessment{}, err
	}
	answer, ok := result.Answers[riskQuestionID]
	if !ok || answer.Choice == nil {
		return semantic.RiskAssessment{}, semantic.NewError(semantic.ErrorInvalidResponse, "System One risk answer is missing")
	}
	assessment := semantic.RiskAssessment{
		Class:      semantic.RiskClass(answer.Choice.Choice),
		Confidence: answer.Choice.Confidence,
		Category:   riskCategory,
		Provider:   result.ProviderMetadata,
	}
	if err := semantic.ValidateRiskAssessment(input, assessment, 0); err != nil {
		return semantic.RiskAssessment{}, err
	}
	return assessment, nil
}

func (client *Client) do(ctx context.Context, body []byte) (wireResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return wireResponse{}, semantic.NewError(semantic.ErrorTransport, "request cannot be created")
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return wireResponse{}, classifySystemOneTransportError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxSystemOneResponseBody+1))
		return wireResponse{}, semantic.NewError(systemOneCategoryForStatus(response.StatusCode), fmt.Sprintf("provider returned HTTP %d", response.StatusCode))
	}
	if !jsonContentType(response.Header.Get("Content-Type")) {
		return wireResponse{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider returned a non-JSON response")
	}
	if response.ContentLength > maxSystemOneResponseBody {
		return wireResponse{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider response exceeds the local limit")
	}
	data, err := readBounded(response.Body, maxSystemOneResponseBody)
	if err != nil {
		return wireResponse{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider response is unreadable or exceeds the local limit")
	}
	var decoded wireResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return wireResponse{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider response is malformed JSON")
	}
	return decoded, nil
}

func encodeWireRequest(request semantic.Request, model string) (wireRequest, error) {
	questions := make(map[string]wireQuestion, len(request.Questions))
	for id, question := range request.Questions {
		wire := wireQuestion{Type: question.Type, Instructions: question.Instructions}
		switch question.Type {
		case semantic.PrimitiveNoul:
			criteria := map[string]any{}
			if question.Noul != nil && question.Noul.True != nil {
				criteria["true"] = question.Noul.True
			}
			if question.Noul != nil && question.Noul.False != nil {
				criteria["false"] = question.Noul.False
			}
			if len(criteria) > 0 {
				wire.Criteria = criteria
			}
		case semantic.PrimitiveChoice:
			wire.Criteria = question.Choice
		case semantic.PrimitiveScore:
			if len(question.Score) > MaxScoreLevels {
				return wireRequest{}, semantic.NewError(semantic.ErrorInvalidRequest, fmt.Sprintf("score question %q exceeds TypeSafe maximum of %d levels", id, MaxScoreLevels))
			}
			criteria := make([]any, len(question.Score))
			for index, level := range question.Score {
				criteria[index] = level.Description
			}
			wire.Criteria = criteria
		default:
			return wireRequest{}, semantic.NewError(semantic.ErrorInvalidRequest, "unsupported semantic primitive")
		}
		questions[id] = wire
	}
	return wireRequest{State: request.State, Model: model, Questions: questions}, nil
}

func decodeWireResponse(request semantic.Request, response wireResponse, duration time.Duration) (semantic.Result, error) {
	model := strings.TrimSpace(response.Model)
	if model == "" {
		return semantic.Result{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider response model is missing")
	}
	if response.Usage == nil || response.Usage.InputTokens == nil || response.Usage.OutputTokens == nil || *response.Usage.InputTokens < 0 || *response.Usage.OutputTokens < 0 {
		return semantic.Result{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider usage metadata is missing or invalid")
	}
	if len(response.Answers) != len(request.Questions) {
		return semantic.Result{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider answer set does not match requested questions")
	}
	answers := make(map[string]semantic.Answer, len(request.Questions))
	for id, question := range request.Questions {
		wire, ok := response.Answers[id]
		if !ok {
			return semantic.Result{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider omitted a required answer")
		}
		answer, err := decodeWireAnswer(question, wire)
		if err != nil {
			return semantic.Result{}, err
		}
		answers[id] = answer
	}
	result := semantic.Result{
		Answers: answers,
		ProviderMetadata: semantic.ProviderMetadata{
			Provider: "typesafe", Model: model, Duration: duration,
			Usage: &semantic.Usage{InputTokens: *response.Usage.InputTokens, OutputTokens: *response.Usage.OutputTokens},
		},
	}
	if err := semantic.ValidateResult(request, result); err != nil {
		if semantic.IsCategory(err, semantic.ErrorInvalidResponse) {
			return semantic.Result{}, err
		}
		return semantic.Result{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider response failed neutral validation")
	}
	return result, nil
}

func decodeWireAnswer(question semantic.Question, wire wireAnswer) (semantic.Answer, error) {
	if wire.Type != question.Type {
		return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider answer primitive does not match request")
	}
	switch question.Type {
	case semantic.PrimitiveNoul:
		if wire.Noul == nil || wire.Choice != nil || wire.Score != nil || wire.Confidence != nil || len(wire.Probabilities) != 0 || len(wire.Legend) != 0 {
			return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider returned an invalid noul answer")
		}
		return semantic.Answer{Type: semantic.PrimitiveNoul, Noul: &semantic.NoulAnswer{ProbabilityYes: *wire.Noul}}, nil
	case semantic.PrimitiveChoice:
		if wire.Choice == nil || wire.Confidence == nil || wire.Noul != nil || wire.Score != nil || len(wire.Legend) != 0 || wire.Probabilities == nil {
			return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider returned an invalid choice answer")
		}
		return semantic.Answer{Type: semantic.PrimitiveChoice, Choice: &semantic.ChoiceAnswer{
			Choice: *wire.Choice, Probabilities: cloneProbabilities(wire.Probabilities), Confidence: *wire.Confidence,
		}}, nil
	case semantic.PrimitiveScore:
		if wire.Score == nil || wire.Confidence == nil || wire.Noul != nil || wire.Choice != nil || wire.Probabilities == nil || wire.Legend == nil {
			return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider returned an invalid score answer")
		}
		if !finite(*wire.Score) || *wire.Score < 0 || *wire.Score > float64(len(question.Score)-1) {
			return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider score is outside the requested level range")
		}
		probabilities := make(map[string]float64, len(question.Score))
		neutralScore, wireWeightedScore := 0.0, 0.0
		if len(wire.Legend) != len(question.Score) || len(wire.Probabilities) != len(question.Score) {
			return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider score distribution does not cover every requested level")
		}
		for index, level := range question.Score {
			wireKey := strconv.Itoa(index)
			if _, ok := wire.Legend[wireKey]; !ok {
				return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider score legend is incomplete")
			}
			probability, ok := wire.Probabilities[wireKey]
			if !ok {
				return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider score distribution is incomplete")
			}
			probabilities[level.Key] = probability
			neutralScore += probability * level.Value
			wireWeightedScore += probability * float64(index)
		}
		if !finite(wireWeightedScore) || math.Abs(*wire.Score-wireWeightedScore) > semantic.ProbabilityEpsilon {
			return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider score is inconsistent with its probability distribution")
		}
		return semantic.Answer{Type: semantic.PrimitiveScore, Score: &semantic.ScoreAnswer{
			Score: neutralScore, Probabilities: probabilities, Confidence: *wire.Confidence,
		}}, nil
	default:
		return semantic.Answer{}, semantic.NewError(semantic.ErrorInvalidResponse, "provider returned an unsupported answer primitive")
	}
}

func cloneSystemOneHTTPClient(source *http.Client, production bool) *http.Client {
	client := &http.Client{}
	if source != nil {
		*client = *source
	}
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 0 && !strings.EqualFold(request.URL.Hostname(), via[0].URL.Hostname()) {
			return errors.New("typesafe cross-host redirect rejected")
		}
		if production && (request.URL.Scheme != "https" ||
			!strings.EqualFold(request.URL.Hostname(), "api.typesafe.ai") ||
			request.URL.Path != SystemOnePath || request.URL.RawQuery != "" || request.URL.Fragment != "") {
			return errors.New("typesafe production redirect rejected")
		}
		if previous != nil {
			return previous(request, via)
		}
		if len(via) >= 10 {
			return errors.New("typesafe redirect limit exceeded")
		}
		return nil
	}
	return client
}

func systemOneCategoryForStatus(status int) semantic.ErrorCategory {
	switch status {
	case http.StatusUnauthorized:
		return semantic.ErrorUnauthorized
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return semantic.ErrorInvalidRequest
	case http.StatusTooManyRequests:
		return semantic.ErrorRateLimited
	case 529:
		return semantic.ErrorOverloaded
	default:
		return semantic.ErrorProvider
	}
}

func classifySystemOneTransportError(ctx context.Context) error {
	if ctx != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return semantic.NewError(semantic.ErrorCancelled, "")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return semantic.NewError(semantic.ErrorTimeout, "")
		}
	}
	return semantic.NewError(semantic.ErrorTransport, "")
}

func boundedClientContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response exceeds limit")
	}
	return data, nil
}

func jsonContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	return err == nil && mediaType == "application/json"
}

func cloneProbabilities(source map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
