package projectcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	plandoc "go.mewis.me/codemcp/internal/plan"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	MaxPlanSummaryCount    = 12
	MaxPlanSummaryBytes    = 8 << 10
	MaxPlanScanCount       = 64
	MaxPlanDiagnosticCount = 8
	maxPlanDiagnosticRunes = 256
)

var ErrPlanNotFound = errors.New("project context plan not found")

type PlanNotFoundError struct {
	Name string
}

func (e *PlanNotFoundError) Error() string {
	return fmt.Sprintf("plan %q was not found in workspace state", e.Name)
}

func (e *PlanNotFoundError) Unwrap() error { return ErrPlanNotFound }

type PlanPhaseSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type PlanSummary struct {
	Name                string            `json:"name"`
	Path                string            `json:"path"`
	ContentID           string            `json:"content_id"`
	Status              plandoc.Status    `json:"status"`
	PhaseCount          int               `json:"phase_count"`
	CompletedPhaseCount int               `json:"completed_phase_count"`
	NextPhase           *PlanPhaseSummary `json:"next_phase,omitempty"`
}

type PlanDiagnostic struct {
	Name    string `json:"name,omitempty"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type PlanContext struct {
	Summaries            []PlanSummary    `json:"summaries"`
	Selected             *PlanSummary     `json:"selected,omitempty"`
	Inferred             *PlanSummary     `json:"inferred,omitempty"`
	Diagnostics          []PlanDiagnostic `json:"diagnostics,omitempty"`
	RequestedName        string           `json:"requested_name,omitempty"`
	TotalEntries         int              `json:"total_entries"`
	ScannedEntries       int              `json:"scanned_entries"`
	NonCompletedCount    int              `json:"non_completed_count"`
	DiagnosticCount      int              `json:"diagnostic_count"`
	SummaryBytes         int              `json:"summary_bytes"`
	ScanComplete         bool             `json:"scan_complete"`
	SummariesTruncated   bool             `json:"summaries_truncated"`
	DiagnosticsTruncated bool             `json:"diagnostics_truncated"`
}

func loadWorkspacePlans(workspaceRoot, requestedName string) (PlanContext, error) {
	requestedName = strings.TrimSpace(requestedName)
	result := PlanContext{
		Summaries:     []PlanSummary{},
		Diagnostics:   []PlanDiagnostic{},
		RequestedName: requestedName,
		ScanComplete:  true,
	}
	if requestedName != "" {
		if err := plandoc.ValidateName(requestedName); err != nil {
			return PlanContext{}, fmt.Errorf("plan_name: %w", err)
		}
	}

	plansRoot := workspacestate.New(workspaceRoot).PlansRoot()
	rootInfo, err := os.Lstat(plansRoot)
	if errors.Is(err, os.ErrNotExist) {
		if requestedName != "" {
			return PlanContext{}, &PlanNotFoundError{Name: requestedName}
		}
		return result, nil
	}
	if err != nil {
		return PlanContext{}, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		result.ScanComplete = false
		addPlanDiagnostic(&result, "", filepath.ToSlash(filepath.Join(workspacestate.DirectoryName, "plans")), errors.New("workspace plan state is not a real directory"))
		return result, nil
	}

	root, err := os.OpenRoot(plansRoot)
	if err != nil {
		return PlanContext{}, err
	}
	defer root.Close()
	openedRoot, err := root.Stat(".")
	if err != nil {
		return PlanContext{}, err
	}
	if !openedRoot.IsDir() || !os.SameFile(rootInfo, openedRoot) {
		result.ScanComplete = false
		addPlanDiagnostic(&result, "", filepath.ToSlash(filepath.Join(workspacestate.DirectoryName, "plans")), errors.New("workspace plan state changed while opening"))
		return result, nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return PlanContext{}, err
	}
	targetFilename := ""
	if requestedName != "" {
		targetFilename = requestedName + ".md"
		if _, err := root.Lstat(targetFilename); errors.Is(err, os.ErrNotExist) {
			_ = dir.Close()
			return PlanContext{}, &PlanNotFoundError{Name: requestedName}
		} else if err != nil {
			_ = dir.Close()
			return PlanContext{}, err
		}
	}

	names, totalEntries, truncated, readErr := boundedPlanEntryNames(dir)
	closeErr := dir.Close()
	if readErr != nil {
		return PlanContext{}, readErr
	}
	if closeErr != nil {
		return PlanContext{}, closeErr
	}
	result.TotalEntries = totalEntries
	result.ScannedEntries = len(names)
	result.ScanComplete = !truncated
	loaded := make(map[string]PlanSummary, len(names)+1)
	var onlyNonCompleted *PlanSummary
	for _, filename := range names {
		summary, err := readPlanSummary(root, filename)
		if err != nil {
			addPlanDiagnostic(&result, strings.TrimSuffix(filename, filepath.Ext(filename)), planRelativePath(filename), err)
			continue
		}
		loaded[filename] = summary
		if summary.Status != plandoc.StatusCompleted {
			result.NonCompletedCount++
			copy := summary
			onlyNonCompleted = &copy
		}
		appendPlanSummary(&result, summary)
	}

	if requestedName != "" {
		summary, ok := loaded[targetFilename]
		if !ok {
			summary, err = readPlanSummary(root, targetFilename)
			if err != nil {
				addPlanDiagnostic(&result, requestedName, planRelativePath(targetFilename), err)
				return result, nil
			}
		}
		result.Selected = clonePlanSummary(summary)
		forcePlanSummary(&result, summary)
	} else if result.ScanComplete && result.DiagnosticCount == 0 && result.NonCompletedCount == 1 && onlyNonCompleted != nil {
		result.Inferred = clonePlanSummary(*onlyNonCompleted)
		forcePlanSummary(&result, *onlyNonCompleted)
	}
	return result, nil
}

func boundedPlanEntryNames(dir *os.File) ([]string, int, bool, error) {
	names := make([]string, 0, MaxPlanScanCount)
	total := 0
	for {
		entries, err := dir.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, total, false, err
		}
		for _, entry := range entries {
			total++
			name := entry.Name()
			if len(names) < MaxPlanScanCount {
				names = append(names, name)
				sort.Strings(names)
				continue
			}
			if name < names[len(names)-1] {
				names[len(names)-1] = name
				sort.Strings(names)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return names, total, total > MaxPlanScanCount, nil
}

func readPlanSummary(root *os.Root, filename string) (PlanSummary, error) {
	if filepath.Base(filename) != filename || filepath.Ext(filename) != ".md" {
		return PlanSummary{}, errors.New("unsupported plan path")
	}
	name := strings.TrimSuffix(filename, ".md")
	if err := plandoc.ValidateName(name); err != nil {
		return PlanSummary{}, err
	}
	info, err := root.Lstat(filename)
	if err != nil {
		return PlanSummary{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return PlanSummary{}, errors.New("plan artifact must be a regular non-symlink file")
	}
	if info.Size() > int64(plandoc.MaxDocumentBytes) {
		return PlanSummary{}, errors.New("plan artifact exceeds size limit")
	}
	file, err := root.Open(filename)
	if err != nil {
		return PlanSummary{}, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		if statErr != nil {
			return PlanSummary{}, statErr
		}
		return PlanSummary{}, errors.New("plan artifact changed while opening")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(plandoc.MaxDocumentBytes)+1))
	closeErr := file.Close()
	if readErr != nil {
		return PlanSummary{}, readErr
	}
	if closeErr != nil {
		return PlanSummary{}, closeErr
	}
	if len(data) > plandoc.MaxDocumentBytes {
		return PlanSummary{}, errors.New("plan artifact exceeds size limit")
	}
	document, err := plandoc.Parse(data)
	if err != nil {
		return PlanSummary{}, err
	}
	summary := PlanSummary{
		Name:                name,
		Path:                planRelativePath(filename),
		ContentID:           document.ContentID(),
		Status:              document.Status(),
		PhaseCount:          document.PhaseCount(),
		CompletedPhaseCount: document.CompletedPhaseCount(),
	}
	if next, ok := document.NextPhase(); ok {
		summary.NextPhase = &PlanPhaseSummary{ID: next.ID, Title: next.Title}
	}
	return summary, nil
}

func appendPlanSummary(result *PlanContext, summary PlanSummary) {
	size := planSummarySize(summary)
	if len(result.Summaries) >= MaxPlanSummaryCount || result.SummaryBytes+size > MaxPlanSummaryBytes {
		result.SummariesTruncated = true
		return
	}
	result.Summaries = append(result.Summaries, summary)
	result.SummaryBytes += size
}

func forcePlanSummary(result *PlanContext, summary PlanSummary) {
	for _, existing := range result.Summaries {
		if existing.Name == summary.Name {
			return
		}
	}
	size := planSummarySize(summary)
	for len(result.Summaries) > 0 && (len(result.Summaries) >= MaxPlanSummaryCount || result.SummaryBytes+size > MaxPlanSummaryBytes) {
		last := len(result.Summaries) - 1
		result.SummaryBytes -= planSummarySize(result.Summaries[last])
		result.Summaries = result.Summaries[:last]
		result.SummariesTruncated = true
	}
	if len(result.Summaries) < MaxPlanSummaryCount && result.SummaryBytes+size <= MaxPlanSummaryBytes {
		result.Summaries = append(result.Summaries, summary)
		result.SummaryBytes += size
		sort.Slice(result.Summaries, func(i, j int) bool { return result.Summaries[i].Name < result.Summaries[j].Name })
	}
}

func planSummarySize(summary PlanSummary) int {
	data, _ := json.Marshal(summary)
	return len(data)
}

func clonePlanSummary(summary PlanSummary) *PlanSummary {
	copy := summary
	if summary.NextPhase != nil {
		next := *summary.NextPhase
		copy.NextPhase = &next
	}
	return &copy
}

func addPlanDiagnostic(result *PlanContext, name, path string, err error) {
	result.DiagnosticCount++
	if len(result.Diagnostics) >= MaxPlanDiagnosticCount {
		result.DiagnosticsTruncated = true
		return
	}
	message := planDiagnosticMessage(err)
	runes := []rune(message)
	if len(runes) > maxPlanDiagnosticRunes {
		message = string(runes[:maxPlanDiagnosticRunes]) + "…"
	}
	result.Diagnostics = append(result.Diagnostics, PlanDiagnostic{Name: name, Path: path, Message: message})
}

func planDiagnosticMessage(err error) string {
	if validation, ok := plandoc.AsValidationError(err); ok {
		message := "invalid plan document"
		if validation.Code != "" {
			message += ": " + string(validation.Code)
		}
		if validation.Field != "" {
			message += ": " + validation.Field
		}
		if validation.Line > 0 {
			message += fmt.Sprintf(": line %d", validation.Line)
		}
		return message
	}
	return strings.TrimSpace(err.Error())
}

func planRelativePath(filename string) string {
	return filepath.ToSlash(filepath.Join(workspacestate.DirectoryName, "plans", filename))
}
