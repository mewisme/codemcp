package explain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/llm"
)

type fakeInference struct {
	request llm.Request
	result  llm.Result
	err     error
}

func (f *fakeInference) Infer(_ context.Context, request llm.Request) (llm.Result, error) {
	f.request = request
	return f.result, f.err
}

func TestServiceGenerateUsesBoundedStructuredContract(t *testing.T) {
	generatedAt := time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC)
	inference := &fakeInference{result: llm.Result{
		Structured: json.RawMessage(`{"summary":"Prints a value.","steps":["Runs echo."],"effects":["Writes to stdout."],"risk_notes":["No persistent effect is visible."],"unknowns":[]}`),
		ProviderID: "ollama",
		Model:      "fixture",
	}}
	service := NewService(Options{Inference: inference, Now: func() time.Time { return generatedAt }})
	result, err := service.Generate(t.Context(), CommandInput{Command: "echo hello", TargetTool: "run_command"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "Prints a value." || result.ProviderID != "ollama" || result.Model != "fixture" || !result.GeneratedAt.Equal(generatedAt) {
		t.Fatalf("result=%#v", result)
	}
	if len(inference.request.ResponseSchema) == 0 || inference.request.MaxOutputTokens != OutputTokens || inference.request.Temperature == nil || *inference.request.Temperature != 0 {
		t.Fatalf("request=%#v", inference.request)
	}
	if len(inference.request.Messages) != 1 || !strings.Contains(inference.request.Messages[0].Content, "echo hello") {
		t.Fatalf("messages=%#v", inference.request.Messages)
	}
}

func TestParseResultRejectsUnknownFieldsAndMissingProvenance(t *testing.T) {
	payload := json.RawMessage(`{"summary":"ok","steps":[],"effects":[],"risk_notes":[],"unknowns":[],"extra":true}`)
	if _, err := ParseResult(llm.Result{Structured: payload, ProviderID: "ollama", Model: "fixture"}, time.Now()); err == nil {
		t.Fatal("unknown explanation field was accepted")
	}
	valid := json.RawMessage(`{"summary":"ok","steps":[],"effects":[],"risk_notes":[],"unknowns":[]}`)
	if _, err := ParseResult(llm.Result{Structured: valid}, time.Now()); err == nil {
		t.Fatal("missing explanation provenance was accepted")
	}
}

func TestSanitizeCommandRejectsEmptyAndRedactedOnlyInput(t *testing.T) {
	for _, input := range []string{"", "   ", "<redacted>", "[redacted]"} {
		if _, err := SanitizeCommand(input); err == nil {
			t.Fatalf("unsafe command %q was accepted", input)
		}
	}
}
