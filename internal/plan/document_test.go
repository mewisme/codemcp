package plan

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParsePartsDerivesCanonicalDocumentAndLifecycle(t *testing.T) {
	planContent := `
# OAuth redesign

## Goal
Replace the current authentication flow.

## Phase 1A - Define model

- [x] Define the canonical model.
- [x] Validate the model.

## Phase 1B - Wire service

- [ ] Connect the service.
- [ ] Add service tests.

## Acceptance

The new flow is covered by the canonical integration contract.
`
	order := `
## Execution rules

Finish one phase at a time.

## Why this order

The model must exist before the service consumes it.

## Ordered phases

- [x] Phase 1A - Define model
- [ ] Phase 1B - Wire service

## Terminal acceptance

- [ ] Full integration validation passes.
`

	document, err := ParseParts(planContent, order)
	if err != nil {
		t.Fatalf("ParseParts() error = %v", err)
	}
	if got, want := document.Title(), "OAuth redesign"; got != want {
		t.Fatalf("Title() = %q, want %q", got, want)
	}
	if got, want := document.Status(), StatusInProgress; got != want {
		t.Fatalf("Status() = %q, want %q", got, want)
	}
	if got, want := document.PhaseCount(), 2; got != want {
		t.Fatalf("PhaseCount() = %d, want %d", got, want)
	}
	if got, want := document.CompletedPhaseCount(), 1; got != want {
		t.Fatalf("CompletedPhaseCount() = %d, want %d", got, want)
	}
	next, ok := document.NextPhase()
	if !ok || next.ID != "1B" || next.Title != "Wire service" || next.Completed {
		t.Fatalf("NextPhase() = %#v, %v", next, ok)
	}
	if document.TerminalAcceptanceComplete() {
		t.Fatal("TerminalAcceptanceComplete() = true, want false")
	}
	if !strings.HasPrefix(document.ContentID(), "sha256:") || len(document.ContentID()) != len("sha256:")+64 {
		t.Fatalf("ContentID() = %q", document.ContentID())
	}

	rendered := document.Render()
	if !bytes.Contains(rendered, []byte("\n\n---\n\n# Implementation order\n\n")) {
		t.Fatalf("Render() missing canonical boundary:\n%s", rendered)
	}
	reparsed, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse(Render()) error = %v", err)
	}
	if !bytes.Equal(rendered, reparsed.Render()) {
		t.Fatalf("Parse(Render()).Render() is not byte stable\nfirst:\n%s\nsecond:\n%s", rendered, reparsed.Render())
	}
	if reparsed.ContentID() != document.ContentID() {
		t.Fatalf("content ID changed after parse/render: %q != %q", reparsed.ContentID(), document.ContentID())
	}
}

func TestParsePartsLifecycleStatesNeedNoExternalState(t *testing.T) {
	tests := []struct {
		name           string
		firstDone      bool
		secondDone     bool
		acceptanceDone bool
		want           Status
		wantNext       string
	}{
		{name: "pending", want: StatusPending, wantNext: "1A"},
		{name: "in progress", firstDone: true, want: StatusInProgress, wantNext: "1B"},
		{name: "all phases waiting acceptance", firstDone: true, secondDone: true, want: StatusInProgress},
		{name: "completed", firstDone: true, secondDone: true, acceptanceDone: true, want: StatusCompleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planContent, order := lifecycleFixture(tt.firstDone, tt.secondDone, tt.acceptanceDone)
			document, err := ParseParts(planContent, order)
			if err != nil {
				t.Fatalf("ParseParts() error = %v", err)
			}
			if got := document.Status(); got != tt.want {
				t.Fatalf("Status() = %q, want %q", got, tt.want)
			}
			next, ok := document.NextPhase()
			if tt.wantNext == "" {
				if ok {
					t.Fatalf("NextPhase() = %#v, true; want no next phase", next)
				}
				return
			}
			if !ok || next.ID != tt.wantNext {
				t.Fatalf("NextPhase() = %#v, %v; want %s", next, ok, tt.wantNext)
			}
		})
	}
}

func TestParsePartsRejectsCompletedPhaseAfterIncompletePhase(t *testing.T) {
	planContent, order := lifecycleFixture(false, true, false)
	_, err := ParseParts(planContent, order)
	assertErrorCode(t, err, ErrorInvalidChecklist)
	if err == nil || !strings.Contains(err.Error(), "cannot be completed before an earlier phase") {
		t.Fatalf("ParseParts() error = %v, want ordered-prefix rejection", err)
	}
}

func TestCanonicalNormalizationMakesEquivalentInputStable(t *testing.T) {
	planContent, order := lifecycleFixture(false, false, false)
	canonical, err := ParseParts(planContent, order)
	if err != nil {
		t.Fatalf("canonical ParseParts() error = %v", err)
	}

	crlfPlan := "\r\n  \r\n" + strings.ReplaceAll(planContent, "\n", "\r\n") + "\r\n \r\n"
	crlfOrder := "\r\n" + strings.ReplaceAll(order, "\n", "\r\n") + "\r\n"
	normalized, err := ParseParts(crlfPlan, crlfOrder)
	if err != nil {
		t.Fatalf("CRLF ParseParts() error = %v", err)
	}
	if canonical.ContentID() != normalized.ContentID() {
		t.Fatalf("equivalent normalized input produced different IDs: %q != %q", canonical.ContentID(), normalized.ContentID())
	}
	if !bytes.Equal(canonical.Render(), normalized.Render()) {
		t.Fatalf("equivalent normalized input produced different bytes\ncanonical:\n%s\nnormalized:\n%s", canonical.Render(), normalized.Render())
	}
}

func TestFencedExamplesDoNotCreatePlanStructure(t *testing.T) {
	planContent := `# Parser safety

## Goal
Document examples without changing canonical structure.

## Phase 1A - Parse real structure

- [ ] Parse the real phase.

~~~markdown
# Implementation order
---
## Phase 9Z - Fake phase
- [x] fake task
~~~

## Acceptance
Examples remain inert.
`
	order := `## Execution rules
Use the canonical checklist.

## Why this order
Only the real phase is ordered.

## Ordered phases
- [ ] Phase 1A - Parse real structure

## Terminal acceptance
- [ ] Parser tests pass.

~~~markdown
## Ordered phases
- [x] Phase 9Z - Fake phase
# Implementation order
~~~
`
	document, err := ParseParts(planContent, order)
	if err != nil {
		t.Fatalf("ParseParts() error = %v", err)
	}
	if got := document.Phases(); len(got) != 1 || got[0].ID != "1A" {
		t.Fatalf("Phases() = %#v, want only real phase", got)
	}
	parsed, err := Parse(document.Render())
	if err != nil {
		t.Fatalf("Parse(Render()) error = %v", err)
	}
	if parsed.PhaseCount() != 1 {
		t.Fatalf("parsed PhaseCount() = %d, want 1", parsed.PhaseCount())
	}
}

func TestParseRejectsDuplicateReservedBoundaryOutsideFence(t *testing.T) {
	planContent, order := lifecycleFixture(false, false, false)
	document, err := ParseParts(planContent, order)
	if err != nil {
		t.Fatalf("ParseParts() error = %v", err)
	}
	value := string(document.Render()) + "\n---\n\n# Implementation order\n\n" + order
	_, err = Parse([]byte(value))
	assertErrorCode(t, err, ErrorInvalidFormat)
}

func TestValidationRejectsMalformedDocumentsDeterministically(t *testing.T) {
	basePlan, baseOrder := lifecycleFixture(false, false, false)
	tests := []struct {
		name  string
		plan  string
		order string
		code  ErrorCode
	}{
		{
			name:  "duplicate phase",
			plan:  strings.Replace(basePlan, "## Acceptance", "## Phase 1A - Define model\n- [ ] duplicate\n\n## Acceptance", 1),
			order: baseOrder,
			code:  ErrorInvalidPhase,
		},
		{
			name:  "orphan ordered phase",
			plan:  basePlan,
			order: strings.Replace(baseOrder, "- [ ] Phase 1B - Wire service", "- [ ] Phase 9A - Orphan", 1),
			code:  ErrorInvalidPhase,
		},
		{
			name: "phase order mismatch",
			plan: basePlan,
			order: strings.Replace(
				baseOrder,
				"- [ ] Phase 1A - Define model\n- [ ] Phase 1B - Wire service",
				"- [ ] Phase 1B - Wire service\n- [ ] Phase 1A - Define model",
				1,
			),
			code: ErrorInvalidPhase,
		},
		{
			name:  "checklist mismatch",
			plan:  basePlan,
			order: strings.Replace(baseOrder, "- [ ] Phase 1A - Define model", "- [x] Phase 1A - Define model", 1),
			code:  ErrorInvalidChecklist,
		},
		{
			name:  "missing terminal acceptance",
			plan:  basePlan,
			order: strings.Replace(baseOrder, "## Terminal acceptance\n- [ ] Release gate passes.", "", 1),
			code:  ErrorInvalidHeading,
		},
		{
			name:  "invalid phase id",
			plan:  strings.Replace(basePlan, "Phase 1A", "Phase alpha", 1),
			order: baseOrder,
			code:  ErrorInvalidPhase,
		},
		{
			name:  "phase has no task checklist",
			plan:  strings.Replace(basePlan, "- [ ] Define the model.", "Define the model.", 1),
			order: baseOrder,
			code:  ErrorInvalidChecklist,
		},
		{
			name:  "reserved heading in plan body",
			plan:  strings.Replace(basePlan, "## Goal", "# Implementation order\n\n## Goal", 1),
			order: baseOrder,
			code:  ErrorInvalidHeading,
		},
		{
			name:  "unterminated fence",
			plan:  strings.Replace(basePlan, "## Acceptance", "```\n## Acceptance", 1),
			order: baseOrder,
			code:  ErrorInvalidFormat,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseParts(tt.plan, tt.order)
			assertErrorCode(t, err, tt.code)
		})
	}
}

func TestInvalidUTF8AndSizeBoundsAreTyped(t *testing.T) {
	_, err := Parse([]byte{0xff})
	assertErrorCode(t, err, ErrorInvalidUTF8)

	basePlan, baseOrder := lifecycleFixture(false, false, false)
	invalidPlan := string([]byte{0xff})
	_, err = ParseParts(invalidPlan, baseOrder)
	assertErrorCode(t, err, ErrorInvalidUTF8)

	oversizePlan := basePlan + strings.Repeat("a", MaxPlanContentBytes-len(basePlan)+1)
	_, err = ParseParts(oversizePlan, baseOrder)
	assertErrorCode(t, err, ErrorTooLarge)

	_, err = Parse(bytes.Repeat([]byte{'a'}, MaxDocumentBytes+1))
	assertErrorCode(t, err, ErrorTooLarge)
}

func TestValidateNameMatchesCanonicalAuthoredArtifactDiscipline(t *testing.T) {
	valid := []string{"oauth", "oauth-redesign", "a1", strings.Repeat("a", MaxArtifactNameLength)}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q) error = %v", name, err)
		}
	}
	invalid := []string{"", " OAuth", "oauth ", "-oauth", "OAuth", "oauth/redesign", "oauth_redesign", strings.Repeat("a", MaxArtifactNameLength+1)}
	for _, name := range invalid {
		err := ValidateName(name)
		assertErrorCode(t, err, ErrorInvalidName)
	}
}

func TestValidateUpdateRejectsCompletedStateRegression(t *testing.T) {
	previousPlan, previousOrder := lifecycleFixture(true, false, false)
	previous, err := ParseParts(previousPlan, previousOrder)
	if err != nil {
		t.Fatalf("previous ParseParts() error = %v", err)
	}

	progressPlan, progressOrder := lifecycleFixture(true, true, false)
	progress, err := ParseParts(progressPlan, progressOrder)
	if err != nil {
		t.Fatalf("progress ParseParts() error = %v", err)
	}
	if err := ValidateUpdate(previous, progress); err != nil {
		t.Fatalf("ValidateUpdate(progress) error = %v", err)
	}

	regressedPlan, regressedOrder := lifecycleFixture(false, false, false)
	regressed, err := ParseParts(regressedPlan, regressedOrder)
	if err != nil {
		t.Fatalf("regressed ParseParts() error = %v", err)
	}
	assertErrorCode(t, ValidateUpdate(previous, regressed), ErrorStateRegression)

	completedPlan, completedOrder := lifecycleFixture(true, true, true)
	completed, err := ParseParts(completedPlan, completedOrder)
	if err != nil {
		t.Fatalf("completed ParseParts() error = %v", err)
	}
	notAcceptedPlan, notAcceptedOrder := lifecycleFixture(true, true, false)
	notAccepted, err := ParseParts(notAcceptedPlan, notAcceptedOrder)
	if err != nil {
		t.Fatalf("notAccepted ParseParts() error = %v", err)
	}
	assertErrorCode(t, ValidateUpdate(completed, notAccepted), ErrorStateRegression)
}

func TestValidationErrorDetailIsBoundedAndMetadataSafe(t *testing.T) {
	err := validationError(ErrorInvalidFormat, "document", 7, strings.Repeat("界", maxValidationErrorDetailRunes+100))
	if !utf8.ValidString(err.Detail) {
		t.Fatal("bounded validation detail is not valid UTF-8")
	}
	if got := utf8.RuneCountInString(err.Detail); got != maxValidationErrorDetailRunes {
		t.Fatalf("detail rune count = %d, want %d", got, maxValidationErrorDetailRunes)
	}
	parsed, ok := AsValidationError(err)
	if !ok || parsed.Code != ErrorInvalidFormat || parsed.Field != "document" || parsed.Line != 7 {
		t.Fatalf("AsValidationError() = %#v, %v", parsed, ok)
	}
}

func lifecycleFixture(firstDone, secondDone, acceptanceDone bool) (string, string) {
	first := " "
	if firstDone {
		first = "x"
	}
	second := " "
	if secondDone {
		second = "x"
	}
	acceptance := " "
	if acceptanceDone {
		acceptance = "x"
	}
	planContent := `# Lifecycle plan

## Goal
Exercise derived lifecycle state.

## Phase 1A - Define model
- [FIRST] Define the model.

## Phase 1B - Wire service
- [SECOND] Wire the service.

## Acceptance
The implementation is ready when terminal acceptance passes.`
	planContent = strings.ReplaceAll(planContent, "[FIRST]", "["+first+"]")
	planContent = strings.ReplaceAll(planContent, "[SECOND]", "["+second+"]")

	order := `## Execution rules
Complete one phase per session.

## Why this order
The service depends on the model.

## Ordered phases
- [FIRST] Phase 1A - Define model
- [SECOND] Phase 1B - Wire service

## Terminal acceptance
- [ACCEPTANCE] Release gate passes.`
	order = strings.ReplaceAll(order, "[FIRST]", "["+first+"]")
	order = strings.ReplaceAll(order, "[SECOND]", "["+second+"]")
	order = strings.ReplaceAll(order, "[ACCEPTANCE]", "["+acceptance+"]")
	return planContent, order
}

func assertErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", code)
	}
	value, ok := AsValidationError(err)
	if !ok {
		t.Fatalf("error %T = %v, want *ValidationError", err, err)
	}
	if value.Code != code {
		t.Fatalf("error code = %q, want %q: %v", value.Code, code, err)
	}
}
