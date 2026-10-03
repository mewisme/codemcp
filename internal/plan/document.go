package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxArtifactNameLength         = 64
	MaxPlanContentBytes           = 512 * 1024
	MaxImplementationOrderBytes   = 256 * 1024
	MaxDocumentBytes              = MaxPlanContentBytes + MaxImplementationOrderBytes + 256
	MaxPhaseCount                 = 128
	MaxPhaseIDLength              = 16
	MaxPhaseTitleRunes            = 160
	MaxTitleRunes                 = 200
	maxValidationErrorDetailRunes = 512
	ImplementationOrderHeading    = "# Implementation order"
	implementationOrderSeparator  = "---"
)

var (
	artifactNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	phaseIDPattern      = regexp.MustCompile(`^[1-9][0-9]*(?:[A-Z][0-9]*)*$`)
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

type ErrorCode string

const (
	ErrorInvalidName      ErrorCode = "invalid_name"
	ErrorInvalidUTF8      ErrorCode = "invalid_utf8"
	ErrorTooLarge         ErrorCode = "too_large"
	ErrorInvalidFormat    ErrorCode = "invalid_format"
	ErrorInvalidHeading   ErrorCode = "invalid_heading"
	ErrorInvalidPhase     ErrorCode = "invalid_phase"
	ErrorInvalidChecklist ErrorCode = "invalid_checklist"
	ErrorStateRegression  ErrorCode = "state_regression"
)

type ValidationError struct {
	Code   ErrorCode `json:"code"`
	Field  string    `json:"field,omitempty"`
	Line   int       `json:"line,omitempty"`
	Detail string    `json:"-"`
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "plan document validation failed"
	}
	prefix := "plan document validation failed"
	if e.Code != "" {
		prefix += ": " + string(e.Code)
	}
	if e.Field != "" {
		prefix += ": " + e.Field
	}
	if e.Line > 0 {
		prefix += fmt.Sprintf(": line %d", e.Line)
	}
	if e.Detail != "" {
		prefix += ": " + e.Detail
	}
	return prefix
}

func AsValidationError(err error) (*ValidationError, bool) {
	var value *ValidationError
	if !errors.As(err, &value) || value == nil {
		return nil, false
	}
	return value, true
}

func IsErrorCode(err error, code ErrorCode) bool {
	value, ok := AsValidationError(err)
	return ok && value.Code == code
}

type Phase struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

type Document struct {
	planContent                string
	implementationOrder        string
	rendered                   []byte
	title                      string
	phases                     []Phase
	status                     Status
	completedPhaseCount        int
	terminalAcceptanceComplete bool
	contentID                  string
}

func ValidateName(name string) error {
	if len(name) > MaxArtifactNameLength || name != strings.TrimSpace(name) || !artifactNamePattern.MatchString(name) {
		return validationError(
			ErrorInvalidName,
			"name",
			0,
			"plan name must be 1-64 lowercase letters, digits, or hyphens and start with a letter or digit",
		)
	}
	return nil
}

func Parse(data []byte) (Document, error) {
	if len(data) > MaxDocumentBytes {
		return Document{}, validationError(ErrorTooLarge, "document", 0, fmt.Sprintf("document exceeds %d bytes", MaxDocumentBytes))
	}
	if !utf8.Valid(data) {
		return Document{}, validationError(ErrorInvalidUTF8, "document", 0, "document must be valid UTF-8")
	}
	normalized := normalizeNewlines(string(data))
	lines := strings.Split(normalized, "\n")
	structural, err := structuralLines(normalized, "document")
	if err != nil {
		return Document{}, err
	}

	headingIndexes := make([]int, 0, 1)
	for _, line := range structural {
		if !isReservedImplementationHeading(line.Text) {
			continue
		}
		if line.Text != ImplementationOrderHeading {
			return Document{}, validationError(
				ErrorInvalidHeading,
				"document",
				line.Number,
				fmt.Sprintf("reserved heading must be exactly %q", ImplementationOrderHeading),
			)
		}
		headingIndexes = append(headingIndexes, line.Index)
	}
	if len(headingIndexes) != 1 {
		detail := "document must contain exactly one implementation-order heading"
		if len(headingIndexes) > 1 {
			detail = "document contains duplicate implementation-order headings"
		}
		return Document{}, validationError(ErrorInvalidFormat, "document", 0, detail)
	}

	headingIndex := headingIndexes[0]
	separatorIndex := previousNonBlankLine(lines, headingIndex-1)
	if separatorIndex < 0 || strings.TrimSpace(lines[separatorIndex]) != implementationOrderSeparator {
		return Document{}, validationError(
			ErrorInvalidFormat,
			"document",
			headingIndex+1,
			"implementation-order heading must be preceded by the reserved document separator",
		)
	}
	for index := separatorIndex + 1; index < headingIndex; index++ {
		if strings.TrimSpace(lines[index]) != "" {
			return Document{}, validationError(ErrorInvalidFormat, "document", index+1, "only blank lines may appear between the separator and implementation-order heading")
		}
	}

	planContent := strings.Join(lines[:separatorIndex], "\n")
	implementationOrder := strings.Join(lines[headingIndex+1:], "\n")
	document, err := ParseParts(planContent, implementationOrder)
	if err != nil {
		return Document{}, err
	}
	if len(document.rendered) > MaxDocumentBytes {
		return Document{}, validationError(ErrorTooLarge, "document", 0, fmt.Sprintf("canonical document exceeds %d bytes", MaxDocumentBytes))
	}
	return document, nil
}

func ParseParts(planContent, implementationOrder string) (Document, error) {
	planContent, err := normalizeBody("plan_content", planContent, MaxPlanContentBytes)
	if err != nil {
		return Document{}, err
	}
	implementationOrder, err = normalizeBody("implementation_order", implementationOrder, MaxImplementationOrderBytes)
	if err != nil {
		return Document{}, err
	}

	planResult, err := parsePlanContent(planContent)
	if err != nil {
		return Document{}, err
	}
	orderResult, err := parseImplementationOrder(implementationOrder)
	if err != nil {
		return Document{}, err
	}
	if len(planResult.phases) != len(orderResult.phases) {
		return Document{}, validationError(
			ErrorInvalidPhase,
			"phases",
			0,
			fmt.Sprintf("plan defines %d phases but implementation order defines %d", len(planResult.phases), len(orderResult.phases)),
		)
	}
	if len(planResult.phases) > MaxPhaseCount {
		return Document{}, validationError(ErrorInvalidPhase, "phases", 0, fmt.Sprintf("phase count exceeds %d", MaxPhaseCount))
	}

	phases := make([]Phase, len(planResult.phases))
	completedCount := 0
	for index := range planResult.phases {
		planPhase := planResult.phases[index]
		orderPhase := orderResult.phases[index]
		if planPhase.ID != orderPhase.ID || planPhase.Title != orderPhase.Title {
			return Document{}, validationError(
				ErrorInvalidPhase,
				"implementation_order",
				orderPhase.line,
				fmt.Sprintf("ordered phase %q must match plan phase %q at position %d", phaseLabel(orderPhase.Phase), phaseLabel(planPhase.Phase), index+1),
			)
		}
		if planPhase.Completed != orderPhase.Completed {
			return Document{}, validationError(
				ErrorInvalidChecklist,
				"implementation_order",
				orderPhase.line,
				fmt.Sprintf("phase %s checklist state does not match the plan body", planPhase.ID),
			)
		}
		phases[index] = planPhase.Phase
		if planPhase.Completed {
			completedCount++
		}
	}
	if err := validateCompletedPhasePrefix(phases); err != nil {
		return Document{}, err
	}

	status := StatusPending
	if completedCount == len(phases) && orderResult.terminalAcceptanceComplete {
		status = StatusCompleted
	} else if completedCount > 0 {
		status = StatusInProgress
	}

	rendered := renderCanonical(planContent, implementationOrder)
	if len(rendered) > MaxDocumentBytes {
		return Document{}, validationError(ErrorTooLarge, "document", 0, fmt.Sprintf("canonical document exceeds %d bytes", MaxDocumentBytes))
	}
	sum := sha256.Sum256(rendered)

	return Document{
		planContent:                planContent,
		implementationOrder:        implementationOrder,
		rendered:                   rendered,
		title:                      planResult.title,
		phases:                     phases,
		status:                     status,
		completedPhaseCount:        completedCount,
		terminalAcceptanceComplete: orderResult.terminalAcceptanceComplete,
		contentID:                  "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func ValidateUpdate(previous, next Document) error {
	if len(previous.rendered) == 0 || len(next.rendered) == 0 {
		return validationError(ErrorInvalidFormat, "document", 0, "both previous and next documents must be valid parsed documents")
	}
	if err := validateCompletedPhasePrefix(previous.phases); err != nil {
		return err
	}
	if err := validateCompletedPhasePrefix(next.phases); err != nil {
		return err
	}
	nextByID := make(map[string]Phase, len(next.phases))
	for _, phase := range next.phases {
		nextByID[phase.ID] = phase
	}
	for _, phase := range previous.phases {
		if !phase.Completed {
			continue
		}
		updated, ok := nextByID[phase.ID]
		if !ok {
			return validationError(ErrorStateRegression, "phases", 0, fmt.Sprintf("completed phase %s cannot be removed", phase.ID))
		}
		if updated.Title != phase.Title {
			return validationError(ErrorStateRegression, "phases", 0, fmt.Sprintf("completed phase %s cannot change identity", phase.ID))
		}
		if !updated.Completed {
			return validationError(ErrorStateRegression, "phases", 0, fmt.Sprintf("completed phase %s cannot regress to incomplete", phase.ID))
		}
	}
	if previous.terminalAcceptanceComplete && !next.terminalAcceptanceComplete {
		return validationError(ErrorStateRegression, "terminal_acceptance", 0, "completed terminal acceptance cannot regress to incomplete")
	}
	return nil
}

func validateCompletedPhasePrefix(phases []Phase) error {
	seenIncomplete := false
	for _, phase := range phases {
		if !phase.Completed {
			seenIncomplete = true
			continue
		}
		if seenIncomplete {
			return validationError(ErrorInvalidChecklist, "phases", 0, fmt.Sprintf("phase %s cannot be completed before an earlier phase", phase.ID))
		}
	}
	return nil
}

func (d Document) PlanContent() string {
	return d.planContent
}

func (d Document) ImplementationOrder() string {
	return d.implementationOrder
}

func (d Document) Render() []byte {
	return append([]byte(nil), d.rendered...)
}

func (d Document) Title() string {
	return d.title
}

func (d Document) Phases() []Phase {
	return append([]Phase(nil), d.phases...)
}

func (d Document) Status() Status {
	return d.status
}

func (d Document) ContentID() string {
	return d.contentID
}

func (d Document) PhaseCount() int {
	return len(d.phases)
}

func (d Document) CompletedPhaseCount() int {
	return d.completedPhaseCount
}

func (d Document) TerminalAcceptanceComplete() bool {
	return d.terminalAcceptanceComplete
}

func (d Document) NextPhase() (Phase, bool) {
	for _, phase := range d.phases {
		if !phase.Completed {
			return phase, true
		}
	}
	return Phase{}, false
}

type parsedPlan struct {
	title  string
	phases []parsedPhase
}

type parsedOrder struct {
	phases                     []parsedPhase
	terminalAcceptanceComplete bool
}

type parsedPhase struct {
	Phase
	line int
}

type structuralLine struct {
	Index  int
	Number int
	Text   string
}

func parsePlanContent(content string) (parsedPlan, error) {
	structural, err := structuralLines(content, "plan_content")
	if err != nil {
		return parsedPlan{}, err
	}
	if len(structural) == 0 || structural[0].Index != 0 || !strings.HasPrefix(structural[0].Text, "# ") {
		return parsedPlan{}, validationError(ErrorInvalidHeading, "plan_content", 1, "plan content must start with exactly one top-level title")
	}

	title := strings.TrimSpace(strings.TrimPrefix(structural[0].Text, "# "))
	if title == "" || utf8.RuneCountInString(title) > MaxTitleRunes {
		return parsedPlan{}, validationError(ErrorInvalidHeading, "plan_content", structural[0].Number, fmt.Sprintf("plan title must be 1-%d Unicode characters", MaxTitleRunes))
	}
	if isReservedImplementationHeading(structural[0].Text) {
		return parsedPlan{}, validationError(ErrorInvalidHeading, "plan_content", structural[0].Number, "plan title cannot use the reserved implementation-order heading")
	}

	topLevelHeadings := 0
	acceptanceCount := 0
	seenAcceptance := false
	currentPhase := -1
	phases := make([]parsedPhase, 0)
	phaseTaskCount := make([]int, 0)
	phaseAllComplete := make([]bool, 0)
	seenPhaseIDs := map[string]struct{}{}

	for _, line := range structural {
		if isReservedImplementationHeading(line.Text) {
			return parsedPlan{}, validationError(ErrorInvalidHeading, "plan_content", line.Number, "plan content cannot contain the reserved implementation-order heading")
		}
		if strings.HasPrefix(line.Text, "# ") {
			topLevelHeadings++
			if line.Index != 0 {
				return parsedPlan{}, validationError(ErrorInvalidHeading, "plan_content", line.Number, "plan content may contain only one top-level title")
			}
			continue
		}
		if strings.HasPrefix(line.Text, "## Phase ") {
			if seenAcceptance {
				return parsedPlan{}, validationError(ErrorInvalidPhase, "plan_content", line.Number, "phase headings cannot appear after the Acceptance section")
			}
			phase, err := parsePhaseHeading(line.Text, "plan_content", line.Number)
			if err != nil {
				return parsedPlan{}, err
			}
			if _, exists := seenPhaseIDs[phase.ID]; exists {
				return parsedPlan{}, validationError(ErrorInvalidPhase, "plan_content", line.Number, fmt.Sprintf("duplicate phase identifier %s", phase.ID))
			}
			if len(phases) >= MaxPhaseCount {
				return parsedPlan{}, validationError(ErrorInvalidPhase, "plan_content", line.Number, fmt.Sprintf("phase count exceeds %d", MaxPhaseCount))
			}
			seenPhaseIDs[phase.ID] = struct{}{}
			phases = append(phases, parsedPhase{Phase: phase, line: line.Number})
			phaseTaskCount = append(phaseTaskCount, 0)
			phaseAllComplete = append(phaseAllComplete, true)
			currentPhase = len(phases) - 1
			continue
		}
		if strings.HasPrefix(line.Text, "## ") {
			currentPhase = -1
			if line.Text == "## Acceptance" {
				acceptanceCount++
				seenAcceptance = true
			}
			continue
		}
		if currentPhase >= 0 {
			if completed, matched := parseTaskItem(line.Text); matched {
				phaseTaskCount[currentPhase]++
				if !completed {
					phaseAllComplete[currentPhase] = false
				}
			}
		}
	}

	if topLevelHeadings != 1 {
		return parsedPlan{}, validationError(ErrorInvalidHeading, "plan_content", 0, "plan content must contain exactly one top-level title")
	}
	if len(phases) == 0 {
		return parsedPlan{}, validationError(ErrorInvalidPhase, "plan_content", 0, "plan content must define at least one phase")
	}
	if acceptanceCount != 1 {
		return parsedPlan{}, validationError(ErrorInvalidHeading, "plan_content", 0, "plan content must contain exactly one ## Acceptance section")
	}
	for index := range phases {
		if phaseTaskCount[index] == 0 {
			return parsedPlan{}, validationError(
				ErrorInvalidChecklist,
				"plan_content",
				phases[index].line,
				fmt.Sprintf("phase %s must contain at least one canonical task checklist item", phases[index].ID),
			)
		}
		phases[index].Completed = phaseAllComplete[index]
	}
	return parsedPlan{title: title, phases: phases}, nil
}

func parseImplementationOrder(content string) (parsedOrder, error) {
	structural, err := structuralLines(content, "implementation_order")
	if err != nil {
		return parsedOrder{}, err
	}
	if len(structural) == 0 {
		return parsedOrder{}, validationError(ErrorInvalidFormat, "implementation_order", 0, "implementation order is empty")
	}

	reservedSections := []string{
		"## Execution rules",
		"## Why this order",
		"## Ordered phases",
		"## Terminal acceptance",
	}
	sectionIndex := -1
	seenSections := make(map[string]struct{}, len(reservedSections))
	phases := make([]parsedPhase, 0)
	seenPhaseIDs := map[string]struct{}{}
	terminalTaskCount := 0
	terminalAllComplete := true

	for _, line := range structural {
		if isReservedImplementationHeading(line.Text) {
			return parsedOrder{}, validationError(ErrorInvalidHeading, "implementation_order", line.Number, "implementation-order body must not include the reserved top-level heading")
		}
		if strings.HasPrefix(line.Text, "# ") {
			return parsedOrder{}, validationError(ErrorInvalidHeading, "implementation_order", line.Number, "implementation-order body cannot contain top-level headings")
		}
		if strings.HasPrefix(line.Text, "## ") {
			expected := sectionIndex + 1
			if expected >= len(reservedSections) || line.Text != reservedSections[expected] {
				return parsedOrder{}, validationError(
					ErrorInvalidHeading,
					"implementation_order",
					line.Number,
					fmt.Sprintf("implementation-order sections must be exactly %s in canonical order", strings.Join(reservedSections, ", ")),
				)
			}
			if _, exists := seenSections[line.Text]; exists {
				return parsedOrder{}, validationError(ErrorInvalidHeading, "implementation_order", line.Number, fmt.Sprintf("duplicate section %q", line.Text))
			}
			seenSections[line.Text] = struct{}{}
			sectionIndex = expected
			continue
		}

		ordered, matched, err := parseOrderedPhaseItem(line.Text, line.Number)
		if err != nil {
			return parsedOrder{}, err
		}
		if matched {
			if sectionIndex != 2 {
				return parsedOrder{}, validationError(ErrorInvalidChecklist, "implementation_order", line.Number, "phase checklist items are allowed only under ## Ordered phases")
			}
			if _, exists := seenPhaseIDs[ordered.ID]; exists {
				return parsedOrder{}, validationError(ErrorInvalidPhase, "implementation_order", line.Number, fmt.Sprintf("duplicate ordered phase identifier %s", ordered.ID))
			}
			if len(phases) >= MaxPhaseCount {
				return parsedOrder{}, validationError(ErrorInvalidPhase, "implementation_order", line.Number, fmt.Sprintf("phase count exceeds %d", MaxPhaseCount))
			}
			seenPhaseIDs[ordered.ID] = struct{}{}
			phases = append(phases, parsedPhase{Phase: ordered, line: line.Number})
			continue
		}

		if sectionIndex == 2 && strings.TrimSpace(line.Text) != "" {
			return parsedOrder{}, validationError(
				ErrorInvalidChecklist,
				"implementation_order",
				line.Number,
				"## Ordered phases may contain only canonical phase checklist items",
			)
		}
		if sectionIndex == 3 {
			if completed, matched := parseTaskItem(line.Text); matched {
				terminalTaskCount++
				if !completed {
					terminalAllComplete = false
				}
			}
		}
	}

	if sectionIndex != len(reservedSections)-1 {
		return parsedOrder{}, validationError(
			ErrorInvalidHeading,
			"implementation_order",
			0,
			fmt.Sprintf("implementation order must contain %s", strings.Join(reservedSections, ", ")),
		)
	}
	if len(phases) == 0 {
		return parsedOrder{}, validationError(ErrorInvalidPhase, "implementation_order", 0, "implementation order must contain at least one ordered phase")
	}
	if terminalTaskCount == 0 {
		return parsedOrder{}, validationError(ErrorInvalidChecklist, "implementation_order", 0, "## Terminal acceptance must contain at least one canonical task checklist item")
	}
	return parsedOrder{phases: phases, terminalAcceptanceComplete: terminalAllComplete}, nil
}

func parsePhaseHeading(line, field string, lineNumber int) (Phase, error) {
	const prefix = "## Phase "
	if !strings.HasPrefix(line, prefix) {
		return Phase{}, validationError(ErrorInvalidPhase, field, lineNumber, "invalid phase heading")
	}
	value := strings.TrimPrefix(line, prefix)
	id, title, ok := strings.Cut(value, " - ")
	if !ok || id == "" || title == "" || title != strings.TrimSpace(title) {
		return Phase{}, validationError(ErrorInvalidPhase, field, lineNumber, "phase heading must use ## Phase <ID> - <title>")
	}
	if err := validatePhaseIdentity(id, title, field, lineNumber); err != nil {
		return Phase{}, err
	}
	return Phase{ID: id, Title: title}, nil
}

func parseOrderedPhaseItem(line string, lineNumber int) (Phase, bool, error) {
	completed, rest, matched := parseTopLevelTaskItem(line)
	if !matched {
		return Phase{}, false, nil
	}
	if !strings.HasPrefix(rest, "Phase ") {
		return Phase{}, false, nil
	}
	value := strings.TrimPrefix(rest, "Phase ")
	id, title, ok := strings.Cut(value, " - ")
	if !ok || id == "" || title == "" || title != strings.TrimSpace(title) {
		return Phase{}, true, validationError(ErrorInvalidPhase, "implementation_order", lineNumber, "ordered phase must use - [ ] Phase <ID> - <title>")
	}
	if err := validatePhaseIdentity(id, title, "implementation_order", lineNumber); err != nil {
		return Phase{}, true, err
	}
	return Phase{ID: id, Title: title, Completed: completed}, true, nil
}

func validatePhaseIdentity(id, title, field string, lineNumber int) error {
	if len(id) == 0 || len(id) > MaxPhaseIDLength || !phaseIDPattern.MatchString(id) {
		return validationError(
			ErrorInvalidPhase,
			field,
			lineNumber,
			fmt.Sprintf("phase identifier must match %s and be at most %d bytes", phaseIDPattern.String(), MaxPhaseIDLength),
		)
	}
	if utf8.RuneCountInString(title) == 0 || utf8.RuneCountInString(title) > MaxPhaseTitleRunes {
		return validationError(ErrorInvalidPhase, field, lineNumber, fmt.Sprintf("phase title must be 1-%d Unicode characters", MaxPhaseTitleRunes))
	}
	return nil
}

func parseTaskItem(line string) (bool, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	completed, _, matched := parseTaskPrefix(trimmed)
	return completed, matched
}

func parseTopLevelTaskItem(line string) (bool, string, bool) {
	if line != strings.TrimLeft(line, " \t") {
		return false, "", false
	}
	return parseTaskPrefix(line)
}

func parseTaskPrefix(line string) (bool, string, bool) {
	if len(line) < 5 || line[0] != '-' || line[1] != ' ' || line[2] != '[' || line[4] != ']' {
		return false, "", false
	}
	if line[3] != ' ' && line[3] != 'x' {
		return false, "", false
	}
	if len(line) > 5 && line[5] != ' ' && line[5] != '\t' {
		return false, "", false
	}
	rest := ""
	if len(line) > 5 {
		rest = strings.TrimSpace(line[6:])
	}
	return line[3] == 'x', rest, true
}

func structuralLines(content, field string) ([]structuralLine, error) {
	lines := strings.Split(content, "\n")
	result := make([]structuralLine, 0, len(lines))
	inFence := false
	var fenceMarker byte
	fenceLength := 0
	fenceLine := 0

	for index, line := range lines {
		if inFence {
			if isFenceClose(line, fenceMarker, fenceLength) {
				inFence = false
				fenceMarker = 0
				fenceLength = 0
			}
			continue
		}
		if marker, length, ok := fenceOpen(line); ok {
			inFence = true
			fenceMarker = marker
			fenceLength = length
			fenceLine = index + 1
			continue
		}
		result = append(result, structuralLine{Index: index, Number: index + 1, Text: line})
	}
	if inFence {
		return nil, validationError(ErrorInvalidFormat, field, fenceLine, "unterminated fenced code block")
	}
	return result, nil
}

func fenceOpen(line string) (byte, int, bool) {
	trimmed, indent := trimMarkdownIndent(line)
	if indent > 3 || len(trimmed) < 3 {
		return 0, 0, false
	}
	marker := trimmed[0]
	if marker != '`' && marker != '~' {
		return 0, 0, false
	}
	length := 0
	for length < len(trimmed) && trimmed[length] == marker {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	if marker == '`' && strings.Contains(trimmed[length:], "`") {
		return 0, 0, false
	}
	return marker, length, true
}

func isFenceClose(line string, marker byte, minimum int) bool {
	trimmed, indent := trimMarkdownIndent(line)
	if indent > 3 || len(trimmed) < minimum {
		return false
	}
	length := 0
	for length < len(trimmed) && trimmed[length] == marker {
		length++
	}
	if length < minimum {
		return false
	}
	return strings.TrimSpace(trimmed[length:]) == ""
}

func trimMarkdownIndent(line string) (string, int) {
	indent := 0
	for indent < len(line) && indent < 4 && line[indent] == ' ' {
		indent++
	}
	return line[indent:], indent
}

func normalizeBody(field, value string, limit int) (string, error) {
	if len(value) > limit {
		return "", validationError(ErrorTooLarge, field, 0, fmt.Sprintf("%s exceeds %d bytes", field, limit))
	}
	if !utf8.ValidString(value) {
		return "", validationError(ErrorInvalidUTF8, field, 0, fmt.Sprintf("%s must be valid UTF-8", field))
	}
	value = normalizeNewlines(value)
	lines := strings.Split(value, "\n")
	for index := range lines {
		if strings.TrimSpace(lines[index]) == "" {
			lines[index] = ""
		}
	}
	start := 0
	for start < len(lines) && lines[start] == "" {
		start++
	}
	end := len(lines)
	for end > start && lines[end-1] == "" {
		end--
	}
	if start == end {
		return "", validationError(ErrorInvalidFormat, field, 0, fmt.Sprintf("%s is empty", field))
	}
	value = strings.Join(lines[start:end], "\n")
	if len(value) > limit {
		return "", validationError(ErrorTooLarge, field, 0, fmt.Sprintf("canonical %s exceeds %d bytes", field, limit))
	}
	return value, nil
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func renderCanonical(planContent, implementationOrder string) []byte {
	return []byte(planContent + "\n\n" + implementationOrderSeparator + "\n\n" + ImplementationOrderHeading + "\n\n" + implementationOrder + "\n")
}

func previousNonBlankLine(lines []string, start int) int {
	for index := start; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) != "" {
			return index
		}
	}
	return -1
}

func isReservedImplementationHeading(line string) bool {
	return strings.EqualFold(strings.TrimSpace(line), ImplementationOrderHeading)
}

func phaseLabel(phase Phase) string {
	return "Phase " + phase.ID + " - " + phase.Title
}

func validationError(code ErrorCode, field string, line int, detail string) *ValidationError {
	return &ValidationError{Code: code, Field: field, Line: line, Detail: boundRunes(strings.TrimSpace(detail), maxValidationErrorDetailRunes)}
}

func boundRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}
