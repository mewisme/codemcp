package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func validRequest() Request {
	return Request{
		Consumer: Consumer{ID: "project_context.memory", Purpose: "memory_rerank"},
		State: map[string]any{
			"query":     "database design",
			"candidate": map[string]any{"note": "Use PostgreSQL"},
		},
		Questions: map[string]Question{
			"relevance": {
				Type: PrimitiveNoul, Instructions: "Is candidate relevant?",
				Noul: &NoulCriteria{True: "Relevant", False: "Irrelevant"},
			},
			"route": {
				Type: PrimitiveChoice, Instructions: "Choose route",
				Choice: map[string]any{"keep": "Keep it", "skip": "Skip it"},
			},
			"quality": {
				Type: PrimitiveScore, Instructions: "Score quality",
				Score: []ScoreLevel{
					{Key: "low", Value: 0, Description: "Low"},
					{Key: "high", Value: 1, Description: "High"},
				},
			},
		},
	}
}

func validResult() Result {
	return Result{
		Answers: map[string]Answer{
			"relevance": {Type: PrimitiveNoul, Noul: &NoulAnswer{ProbabilityYes: 0.8}},
			"route": {
				Type: PrimitiveChoice,
				Choice: &ChoiceAnswer{
					Choice:        "keep",
					Probabilities: map[string]float64{"keep": 0.75, "skip": 0.25},
					Confidence:    0.7,
				},
			},
			"quality": {
				Type: PrimitiveScore,
				Score: &ScoreAnswer{
					Score:         0.6,
					Probabilities: map[string]float64{"low": 0.4, "high": 0.6},
					Confidence:    0.55,
				},
			},
		},
		ProviderMetadata: ProviderMetadata{
			Provider: "fake",
			Model:    "test-model",
			Duration: time.Millisecond,
			Usage:    &Usage{InputTokens: 10, OutputTokens: 3},
		},
	}
}

func TestValidateRequestAcceptsChoiceScoreNoulBatch(t *testing.T) {
	request := validRequest()
	if err := ValidateRequest(request); err != nil {
		t.Fatal(err)
	}
	if len(request.Questions) != 3 {
		t.Fatalf("batch question count=%d", len(request.Questions))
	}
	if request.Questions["relevance"].Type != PrimitiveNoul ||
		request.Questions["route"].Type != PrimitiveChoice ||
		request.Questions["quality"].Type != PrimitiveScore {
		t.Fatal("neutral primitive identity changed")
	}
}

func TestNoulCriteriaAreOptional(t *testing.T) {
	request := validRequest()
	request.Questions["relevance"] = Question{
		Type: PrimitiveNoul, Instructions: "Is candidate relevant?",
	}
	if err := ValidateRequest(request); err != nil {
		t.Fatal(err)
	}
}

func TestNoulDecisionMakesUncertaintyExplicit(t *testing.T) {
	tests := []struct {
		probability float64
		minimum     float64
		want        NoulDecision
	}{
		{0.95, 0.8, NoulYes},
		{0.05, 0.8, NoulNo},
		{0.7, 0.8, NoulUncertain},
		{0.5, 0, NoulUncertain},
	}
	for _, test := range tests {
		answer := NoulAnswer{ProbabilityYes: test.probability}
		if got := answer.Decision(test.minimum); got != test.want {
			t.Fatalf("p=%v minimum=%v decision=%q want=%q", test.probability, test.minimum, got, test.want)
		}
	}
}

func TestValidateRequestRejectsMalformedAndOversizedInputsLocally(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{"missing consumer", func(r *Request) { r.Consumer.ID = "" }},
		{"unsafe consumer purpose", func(r *Request) { r.Consumer.Purpose = "query: user secret text?" }},
		{"oversized state", func(r *Request) { r.State = map[string]any{"value": strings.Repeat("x", MaxStateBytes)} }},
		{"too many questions", func(r *Request) {
			r.Questions = map[string]Question{}
			for i := 0; i <= MaxQuestions; i++ {
				r.Questions["q_"+strings.Repeat("x", i)] = Question{Type: PrimitiveNoul, Instructions: "judge", Noul: &NoulCriteria{}}
			}
		}},
		{"unsafe question id", func(r *Request) {
			r.Questions["question with spaces"] = r.Questions["relevance"]
			delete(r.Questions, "relevance")
		}},
		{"invalid JSON state", func(r *Request) { r.State = map[string]any{"bad": math.NaN()} }},
		{"choice underflow", func(r *Request) {
			r.Questions["route"] = Question{Type: PrimitiveChoice, Instructions: "choose", Choice: map[string]any{"only": "one"}}
		}},
		{"score unordered", func(r *Request) {
			r.Questions["quality"] = Question{Type: PrimitiveScore, Instructions: "score", Score: []ScoreLevel{{Key: "high", Value: 1, Description: "High"}, {Key: "low", Value: 0, Description: "Low"}}}
		}},
		{"score overflow", func(r *Request) {
			levels := make([]ScoreLevel, MaxScoreLevels+1)
			for i := range levels {
				levels[i] = ScoreLevel{Key: "level_" + string(rune('a'+i)), Value: float64(i), Description: "Level"}
			}
			r.Questions["quality"] = Question{Type: PrimitiveScore, Instructions: "score", Score: levels}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			test.mutate(&request)
			if err := ValidateRequest(request); err == nil || !IsCategory(err, ErrorInvalidRequest) {
				t.Fatalf("expected invalid_request, got %v", err)
			}
		})
	}
}

func TestValidateResultPreservesCompleteDistributionsAndProviderMetadata(t *testing.T) {
	request := validRequest()
	result := validResult()
	if err := ValidateResult(request, result); err != nil {
		t.Fatal(err)
	}
	if len(result.Answers["route"].Choice.Probabilities) != 2 ||
		len(result.Answers["quality"].Score.Probabilities) != 2 {
		t.Fatal("full probability distribution was not retained")
	}
	if result.Provider != "fake" || result.Model != "test-model" || result.Usage == nil {
		t.Fatalf("provider metadata=%#v", result.ProviderMetadata)
	}
}

func TestValidateResultRejectsIncompleteOrOutOfRangeProviderData(t *testing.T) {
	request := validRequest()
	tests := []struct {
		name   string
		mutate func(*Result)
	}{
		{"noul range", func(r *Result) {
			r.Answers["relevance"] = Answer{Type: PrimitiveNoul, Noul: &NoulAnswer{ProbabilityYes: 1.1}}
		}},
		{"choice incomplete distribution", func(r *Result) {
			r.Answers["route"] = Answer{Type: PrimitiveChoice, Choice: &ChoiceAnswer{Choice: "keep", Probabilities: map[string]float64{"keep": 1}, Confidence: 1}}
		}},
		{"score outside range", func(r *Result) {
			r.Answers["quality"] = Answer{Type: PrimitiveScore, Score: &ScoreAnswer{Score: 2, Probabilities: map[string]float64{"low": 0, "high": 1}, Confidence: 1}}
		}},
		{"unsafe provider metadata", func(r *Result) { r.Provider = "remote provider body" }},
		{"negative usage", func(r *Result) { r.Usage = &Usage{InputTokens: -1} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validResult()
			test.mutate(&result)
			if err := ValidateResult(request, result); err == nil || !IsCategory(err, ErrorInvalidResponse) {
				t.Fatalf("expected invalid_response, got %#v", err)
			}
		})
	}
}

func TestProviderInterfaceSupportsFakeAndNativeImplementations(t *testing.T) {
	request := validRequest()
	want := validResult()
	providers := []Provider{
		ProviderFunc(func(context.Context, Request) (Result, error) { return want, nil }),
		nativeTestProvider{result: want},
	}
	for _, provider := range providers {
		got, err := provider.Evaluate(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateResult(request, got); err != nil {
			t.Fatal(err)
		}
	}
}

type nativeTestProvider struct{ result Result }

func (provider nativeTestProvider) Evaluate(context.Context, Request) (Result, error) {
	return provider.result, nil
}

func TestTypedSemanticErrorsCoverAvailabilityAndTransientFailures(t *testing.T) {
	for _, category := range []ErrorCategory{
		ErrorUnavailable,
		ErrorMisconfigured,
		ErrorRateLimited,
		ErrorTimeout,
		ErrorOverloaded,
	} {
		err := NewError(category, "")
		if !IsCategory(err, category) {
			t.Fatalf("category %q not discoverable", category)
		}
	}
	semanticErr := NewError(ErrorRateLimited, "")
	if semanticErr.Error() != "semantic evaluation failed: rate_limited" {
		t.Fatalf("err=%v", semanticErr)
	}
	wrapped := errors.New("outer: " + semanticErr.Error())
	if IsCategory(wrapped, ErrorRateLimited) {
		t.Fatal("string matching must not classify semantic errors")
	}
	withDetail := NewError(ErrorProvider, "safe local diagnostic")
	encoded, err := json.Marshal(withDetail.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), withDetail.Detail) || string(encoded) != `{"category":"provider"}` {
		t.Fatalf("error metadata leaked detail: %s", encoded)
	}
}

func TestRiskContractsRejectUnknownLowConfidenceAndMalformedAssessments(t *testing.T) {
	input := RiskInput{
		Consumer:    Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		WorkspaceID: "ws_demo",
		CallerID:    "caller_demo",
		Invocation: CanonicalInvocation{
			Operation: "shell.execute",
			Tool:      "shell",
			Arguments: map[string]any{"command": "git status"},
		},
	}
	valid := RiskAssessment{
		Class:      RiskMedium,
		Confidence: 0.9,
		Category:   "filesystem_mutation",
		Reason:     "Touches repository state.",
		Provider:   ProviderMetadata{Provider: "fake", Model: "risk-test"},
	}
	if err := ValidateRiskAssessment(input, valid, 0.8); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*RiskAssessment)
	}{
		{"unknown class", func(a *RiskAssessment) { a.Class = "extreme" }},
		{"low confidence", func(a *RiskAssessment) { a.Confidence = 0.7 }},
		{"unsafe category", func(a *RiskAssessment) { a.Category = "raw command: rm -rf /" }},
		{"oversized reason", func(a *RiskAssessment) { a.Reason = strings.Repeat("x", MaxRiskReasonBytes+1) }},
		{"invalid provider", func(a *RiskAssessment) { a.Provider.Provider = "provider body text" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assessment := valid
			test.mutate(&assessment)
			if err := ValidateRiskAssessment(input, assessment, 0.8); err == nil || !IsCategory(err, ErrorInvalidResponse) {
				t.Fatalf("expected invalid_response, got %#v", err)
			}
		})
	}
}

func TestRiskClassifierIsEvidenceOnlyTypedCapability(t *testing.T) {
	input := RiskInput{
		Consumer:   Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		Invocation: CanonicalInvocation{Operation: "shell.execute", Arguments: map[string]any{"command": "go test ./..."}},
	}
	classifier := RiskClassifierFunc(func(context.Context, RiskInput) (RiskAssessment, error) {
		return RiskAssessment{
			Class: RiskLow, Confidence: 0.95, Category: "read_only",
			Provider: ProviderMetadata{Provider: "native", Model: "fixture"},
		}, nil
	})
	assessment, err := classifier.ClassifyRisk(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRiskAssessment(input, assessment, 0.8); err != nil {
		t.Fatal(err)
	}
}

func TestSemanticPackageHasNoTypeSafeDependency(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve semantic package path")
	}
	dir := filepath.Dir(current)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			if strings.Contains(imported.Path.Value, "internal/integrations/typesafe") {
				t.Fatalf("provider-neutral semantic package imports TypeSafe in %s", entry.Name())
			}
		}
	}
}
