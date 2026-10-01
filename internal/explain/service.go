package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/llm"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	MaxCommandBytes = 16 * 1024
	MaxSummaryRunes = 600
	MaxListItems    = 8
	MaxItemRunes    = 400
	OutputTokens    = 1000
)

const instructions = `You are CodeMCP's command explainer for a human operator.
Explain only the syntax and likely effects of the exact redacted command supplied by CodeMCP.
State uncertainty explicitly. Do not recommend approving, denying, executing, or trusting the command.
Do not infer hidden intent, missing context, credentials, or values represented by <redacted>.
Return only one JSON object with exactly these keys:
{"summary":"...","steps":["..."],"effects":["..."],"risk_notes":["..."],"unknowns":["..."]}`

var responseSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "properties":{
    "summary":{"type":"string","minLength":1,"maxLength":600},
    "steps":{"type":"array","maxItems":8,"items":{"type":"string","minLength":1,"maxLength":400}},
    "effects":{"type":"array","maxItems":8,"items":{"type":"string","minLength":1,"maxLength":400}},
    "risk_notes":{"type":"array","maxItems":8,"items":{"type":"string","minLength":1,"maxLength":400}},
    "unknowns":{"type":"array","maxItems":8,"items":{"type":"string","minLength":1,"maxLength":400}}
  },
  "required":["summary","steps","effects","risk_notes","unknowns"]
}`)

type Inference interface {
	Infer(context.Context, llm.Request) (llm.Result, error)
}

type CommandInput struct {
	Command    string
	TargetTool string
}

type Explanation struct {
	Summary     string         `json:"summary"`
	Steps       []string       `json:"steps,omitempty"`
	Effects     []string       `json:"effects,omitempty"`
	RiskNotes   []string       `json:"risk_notes,omitempty"`
	Unknowns    []string       `json:"unknowns,omitempty"`
	ProviderID  llm.ProviderID `json:"provider_id"`
	Model       string         `json:"model"`
	GeneratedAt time.Time      `json:"generated_at"`
}

type Options struct {
	Inference        Inference
	StructuredOutput func(context.Context) bool
	Now              func() time.Time
}

type Service struct {
	inference        Inference
	structuredOutput func(context.Context) bool
	now              func() time.Time
}

func NewService(options Options) *Service {
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	structured := options.StructuredOutput
	if structured == nil {
		structured = func(context.Context) bool { return true }
	}
	return &Service{inference: options.Inference, structuredOutput: structured, now: now}
}

func (s *Service) Generate(ctx context.Context, input CommandInput) (Explanation, error) {
	if s == nil || s.inference == nil {
		return Explanation{}, errors.New("explain service is unavailable")
	}
	safeCommand, err := SanitizeCommand(input.Command)
	if err != nil {
		return Explanation{}, err
	}
	zero := 0.0
	request := llm.Request{
		Instructions:    instructions,
		Messages:        []llm.Message{{Role: llm.RoleUser, Content: "Explain this exact redacted command:\n" + safeCommand}},
		MaxOutputTokens: OutputTokens,
		Temperature:     &zero,
	}
	if s.structuredOutput(ctx) {
		request.ResponseSchema = append(json.RawMessage(nil), responseSchema...)
	}
	result, err := s.inference.Infer(ctx, request)
	if err != nil {
		return Explanation{}, err
	}
	return ParseResult(result, s.nowUTC())
}

func SanitizeCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > MaxCommandBytes {
		return "", errors.New("command cannot be safely explained")
	}
	safe := strings.TrimSpace(tracepkg.SanitizeText(tracepkg.SanitizeCommand(command)))
	if safe == "" || !commandMeaningful(safe) {
		return "", errors.New("command cannot be safely explained")
	}
	return safe, nil
}

func commandMeaningful(command string) bool {
	remaining := strings.ReplaceAll(strings.ToLower(command), "<redacted>", "")
	remaining = strings.ReplaceAll(remaining, "[redacted]", "")
	for _, r := range remaining {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

type explanationWire struct {
	Summary   string   `json:"summary"`
	Steps     []string `json:"steps"`
	Effects   []string `json:"effects"`
	RiskNotes []string `json:"risk_notes"`
	Unknowns  []string `json:"unknowns"`
}

func ParseResult(result llm.Result, generatedAt time.Time) (Explanation, error) {
	payload := explanationPayload(result)
	if len(payload) == 0 || len(payload) > 32*1024 {
		return Explanation{}, errors.New("invalid explanation payload")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire explanationWire
	if err := decoder.Decode(&wire); err != nil {
		return Explanation{}, errors.New("invalid explanation payload")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Explanation{}, errors.New("invalid explanation payload")
	}
	wire.Summary = strings.TrimSpace(wire.Summary)
	if wire.Summary == "" || utf8.RuneCountInString(wire.Summary) > MaxSummaryRunes {
		return Explanation{}, errors.New("invalid explanation summary")
	}
	var err error
	wire.Steps, err = normalizeList(wire.Steps)
	if err != nil {
		return Explanation{}, err
	}
	wire.Effects, err = normalizeList(wire.Effects)
	if err != nil {
		return Explanation{}, err
	}
	wire.RiskNotes, err = normalizeList(wire.RiskNotes)
	if err != nil {
		return Explanation{}, err
	}
	wire.Unknowns, err = normalizeList(wire.Unknowns)
	if err != nil {
		return Explanation{}, err
	}
	if result.ProviderID == "" || strings.TrimSpace(result.Model) == "" {
		return Explanation{}, errors.New("explanation provenance is missing")
	}
	return Explanation{
		Summary: wire.Summary, Steps: cloneStrings(wire.Steps), Effects: cloneStrings(wire.Effects),
		RiskNotes: cloneStrings(wire.RiskNotes), Unknowns: cloneStrings(wire.Unknowns),
		ProviderID: result.ProviderID, Model: strings.TrimSpace(result.Model), GeneratedAt: generatedAt.UTC(),
	}, nil
}

func explanationPayload(result llm.Result) []byte {
	if payload := bytes.TrimSpace(result.Structured); len(payload) > 0 {
		return payload
	}
	payload := bytes.TrimSpace([]byte(result.Text))
	if len(payload) == 0 || json.Valid(payload) {
		return payload
	}
	start := bytes.IndexByte(payload, '{')
	if start < 0 {
		return payload
	}
	decoder := json.NewDecoder(bytes.NewReader(payload[start:]))
	var extracted json.RawMessage
	if err := decoder.Decode(&extracted); err != nil {
		return payload
	}
	return bytes.TrimSpace(extracted)
}

func normalizeList(list []string) ([]string, error) {
	if len(list) > MaxListItems {
		return nil, errors.New("explanation list exceeds limit")
	}
	result := make([]string, len(list))
	for index, value := range list {
		value = strings.TrimSpace(value)
		if value == "" || utf8.RuneCountInString(value) > MaxItemRunes {
			return nil, errors.New("invalid explanation list item")
		}
		result[index] = value
	}
	return result, nil
}

func cloneStrings(value []string) []string {
	return append([]string(nil), value...)
}

func (s *Service) nowUTC() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
