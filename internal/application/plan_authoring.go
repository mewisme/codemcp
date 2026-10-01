package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/instructioncontext"
	plandoc "go.mewis.me/codemcp/internal/plan"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

var (
	ErrPlanInvalid     = errors.New("invalid plan authoring request")
	ErrPlanNotFound    = errors.New("plan artifact or workspace not found")
	ErrPlanConflict    = errors.New("plan authoring conflict")
	ErrPlanStale       = errors.New("plan authoring target is stale")
	ErrPlanUnavailable = errors.New("plan authoring is unavailable")

	planAuthoringMutationMu sync.Mutex
)

type PlanAuthoringMode string

const (
	PlanCreate PlanAuthoringMode = "create"
	PlanUpdate PlanAuthoringMode = "update"
)

type PlanAuthoringRequest struct {
	WorkspaceID         string            `json:"workspace_id"`
	Mode                PlanAuthoringMode `json:"mode"`
	Name                string            `json:"name"`
	PlanContent         string            `json:"plan_content"`
	ImplementationOrder string            `json:"implementation_order"`
	ExpectedContentID   string            `json:"expected_content_id,omitempty"`
	DryRun              bool              `json:"dry_run,omitempty"`
}

type PlanPhaseSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type PlanAuthoringResult struct {
	Path                string            `json:"path"`
	Name                string            `json:"name"`
	ContentID           string            `json:"content_id"`
	Status              plandoc.Status    `json:"status"`
	PhaseCount          int               `json:"phase_count"`
	CompletedPhaseCount int               `json:"completed_phase_count"`
	NextPhase           *PlanPhaseSummary `json:"next_phase,omitempty"`
	DryRun              bool              `json:"dry_run"`
}

type PlanAuthoringService struct {
	Workspaces *workspace.Manager
	Changes    *instructioncontext.ChangeStream

	activationHook func(point string) error
}

type planAuthoringTarget struct {
	workspaceID   string
	workspaceRoot string
	basePath      string
	targetRel     string
}

func NewPlanAuthoringService(workspaces *workspace.Manager, streams ...*instructioncontext.ChangeStream) *PlanAuthoringService {
	var changes *instructioncontext.ChangeStream
	if len(streams) > 0 {
		changes = streams[0]
	}
	return &PlanAuthoringService{Workspaces: workspaces, Changes: changes}
}

func (s *PlanAuthoringService) Write(ctx context.Context, request PlanAuthoringRequest) (PlanAuthoringResult, error) {
	if s == nil || s.Workspaces == nil {
		return PlanAuthoringResult{}, ErrPlanUnavailable
	}
	if request.Mode != PlanCreate && request.Mode != PlanUpdate {
		return PlanAuthoringResult{}, fmt.Errorf("%w: unsupported mode %q", ErrPlanInvalid, request.Mode)
	}
	if err := plandoc.ValidateName(request.Name); err != nil {
		return PlanAuthoringResult{}, fmt.Errorf("%w: %v", ErrPlanInvalid, err)
	}
	if request.Mode == PlanCreate && strings.TrimSpace(request.ExpectedContentID) != "" {
		return PlanAuthoringResult{}, fmt.Errorf("%w: expected_content_id is only valid for update", ErrPlanInvalid)
	}
	if request.Mode == PlanUpdate && strings.TrimSpace(request.ExpectedContentID) == "" {
		return PlanAuthoringResult{}, fmt.Errorf("%w: expected_content_id is required for update", ErrPlanInvalid)
	}
	document, err := plandoc.ParseParts(request.PlanContent, request.ImplementationOrder)
	if err != nil {
		return PlanAuthoringResult{}, fmt.Errorf("%w: %v", ErrPlanInvalid, err)
	}
	rendered := document.Render()
	target, err := s.resolveTarget(strings.TrimSpace(request.WorkspaceID), request.Name)
	if err != nil {
		return PlanAuthoringResult{}, err
	}
	result := planAuthoringResult(target, request.Name, document, request.DryRun)

	planAuthoringMutationMu.Lock()
	defer planAuthoringMutationMu.Unlock()

	if err := s.revalidateTarget(target); err != nil {
		return PlanAuthoringResult{}, err
	}
	if err := plandoc.ValidateWorkspaceState(filepath.Dir(result.Path)); err != nil {
		return PlanAuthoringResult{}, fmt.Errorf("%w: invalid workspace plan state: %v", ErrPlanInvalid, err)
	}
	root, err := openStableDirectory(target.basePath, false)
	if err != nil {
		return PlanAuthoringResult{}, err
	}
	defer root.Close()

	plansExist, err := stableSubdirectoryExists(root, "plans")
	if err != nil {
		return PlanAuthoringResult{}, err
	}
	currentID := ""
	var currentDocument plandoc.Document
	exists := false
	if plansExist {
		currentDocument, exists, err = snapshotRootPlan(root, target.targetRel)
		if err != nil {
			return PlanAuthoringResult{}, err
		}
		if exists {
			currentID = currentDocument.ContentID()
		}
	}
	if err := validatePlanMode(request.Mode, exists); err != nil {
		return PlanAuthoringResult{}, fmt.Errorf("%s %q: %w", request.Mode, request.Name, err)
	}
	if request.Mode == PlanUpdate && currentID != strings.TrimSpace(request.ExpectedContentID) {
		return PlanAuthoringResult{}, fmt.Errorf("%w: expected content %q, found %q", ErrPlanStale, strings.TrimSpace(request.ExpectedContentID), currentID)
	}
	if request.Mode == PlanUpdate {
		if err := plandoc.ValidateUpdate(currentDocument, document); err != nil {
			return PlanAuthoringResult{}, fmt.Errorf("%w: %v", ErrPlanInvalid, err)
		}
	}
	if request.DryRun {
		return result, nil
	}

	if err := ensureStableSubdirectory(root, "plans"); err != nil {
		return PlanAuthoringResult{}, err
	}
	stageRel, err := stageRootFile(root, "plans", request.Name+".md", rendered, fs.FileMode(0o600))
	if err != nil {
		return PlanAuthoringResult{}, err
	}
	defer root.Remove(stageRel)
	if err := s.callActivationHook("before-activate"); err != nil {
		return PlanAuthoringResult{}, err
	}
	if err := s.revalidateTarget(target); err != nil {
		return PlanAuthoringResult{}, err
	}
	if err := ensureStableSubdirectory(root, "plans"); err != nil {
		return PlanAuthoringResult{}, err
	}

	if request.Mode == PlanCreate {
		if _, err := root.Lstat(target.targetRel); err == nil {
			return PlanAuthoringResult{}, fmt.Errorf("%w: create %q target already exists", ErrPlanConflict, request.Name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return PlanAuthoringResult{}, err
		}
		if err := root.Link(stageRel, target.targetRel); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return PlanAuthoringResult{}, fmt.Errorf("%w: create %q target already exists", ErrPlanConflict, request.Name)
			}
			return PlanAuthoringResult{}, fmt.Errorf("create plan %q: %w", request.Name, err)
		}
		if err := root.Remove(stageRel); err != nil {
			return PlanAuthoringResult{}, fmt.Errorf("remove staged plan %q: %w", request.Name, err)
		}
	} else if err := s.activateUpdate(root, target, strings.TrimSpace(request.ExpectedContentID), stageRel); err != nil {
		return PlanAuthoringResult{}, err
	}

	if s.Changes != nil {
		s.Changes.Publish(instructioncontext.Change{
			Kind: "plan", Scope: "workspace", WorkspaceID: target.workspaceID,
			Name: request.Name, Operation: string(request.Mode),
		})
	}
	return result, nil
}

func (s *PlanAuthoringService) resolveTarget(workspaceID, name string) (planAuthoringTarget, error) {
	if workspaceID == "" {
		return planAuthoringTarget{}, fmt.Errorf("%w: workspace id is required", ErrPlanInvalid)
	}
	item, err := s.Workspaces.Get(workspaceID)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return planAuthoringTarget{}, fmt.Errorf("%w: %v", ErrPlanNotFound, err)
		}
		return planAuthoringTarget{}, err
	}
	store, err := s.Workspaces.LocalState(item.ID)
	if err != nil {
		return planAuthoringTarget{}, err
	}
	identity, err := store.LoadIdentity()
	if err != nil {
		return planAuthoringTarget{}, fmt.Errorf("verify workspace local state: %w", err)
	}
	if identity.ID != item.ID {
		return planAuthoringTarget{}, fmt.Errorf("%w: workspace local state identity mismatch", ErrPlanStale)
	}
	return planAuthoringTarget{
		workspaceID: item.ID, workspaceRoot: item.Path, basePath: store.Root(),
		targetRel: filepath.Join("plans", name+".md"),
	}, nil
}

func (s *PlanAuthoringService) revalidateTarget(target planAuthoringTarget) error {
	item, err := s.Workspaces.Get(target.workspaceID)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return fmt.Errorf("%w: workspace disappeared during plan authoring", ErrPlanStale)
		}
		return err
	}
	if filepath.Clean(item.Path) != filepath.Clean(target.workspaceRoot) {
		return fmt.Errorf("%w: workspace root changed during plan authoring", ErrPlanStale)
	}
	store := workspacestate.New(item.Path)
	identity, err := store.LoadIdentity()
	if err != nil {
		return fmt.Errorf("%w: revalidate workspace local state: %v", ErrPlanStale, err)
	}
	if identity.ID != target.workspaceID || filepath.Clean(store.Root()) != filepath.Clean(target.basePath) {
		return fmt.Errorf("%w: workspace local state changed during plan authoring", ErrPlanStale)
	}
	return nil
}

func (s *PlanAuthoringService) activateUpdate(root *os.Root, target planAuthoringTarget, expectedID, stageRel string) error {
	backupRel, err := uniqueArtifactPath("plans", filepath.Base(target.targetRel), "backup")
	if err != nil {
		return err
	}
	if err := root.Rename(target.targetRel, backupRel); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: plan artifact changed before update activation", ErrPlanStale)
		}
		return fmt.Errorf("capture current plan artifact: %w", err)
	}
	restore := func(cause error) error {
		if restoreErr := root.Rename(backupRel, target.targetRel); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restore previous plan artifact: %w", restoreErr))
		}
		return cause
	}
	captured, exists, err := snapshotRootPlan(root, backupRel)
	if err != nil {
		return restore(err)
	}
	if !exists || captured.ContentID() != expectedID {
		return restore(fmt.Errorf("%w: plan artifact changed before update activation", ErrPlanStale))
	}
	if err := s.callActivationHook("after-backup"); err != nil {
		return restore(err)
	}
	if err := s.revalidateTarget(target); err != nil {
		return restore(err)
	}
	if err := root.Rename(stageRel, target.targetRel); err != nil {
		return restore(fmt.Errorf("activate plan update: %w", err))
	}
	if err := root.Remove(backupRel); err != nil {
		rollbackErr := root.Rename(target.targetRel, stageRel)
		if rollbackErr == nil {
			rollbackErr = root.Rename(backupRel, target.targetRel)
		}
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("remove previous plan artifact backup: %w", err), fmt.Errorf("restore previous plan artifact: %w", rollbackErr))
		}
		return fmt.Errorf("remove previous plan artifact backup: %w", err)
	}
	return nil
}

func (s *PlanAuthoringService) callActivationHook(point string) error {
	if s.activationHook == nil {
		return nil
	}
	return s.activationHook(point)
}

func stableSubdirectoryExists(root *os.Root, relative string) (bool, error) {
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("managed %s root must be a real directory", relative)
	}
	opened, err := root.OpenRoot(relative)
	if err != nil {
		return false, err
	}
	defer opened.Close()
	current, err := opened.Stat(".")
	if err != nil || !current.IsDir() || !os.SameFile(info, current) {
		if err != nil {
			return false, err
		}
		return false, fmt.Errorf("managed %s root changed while opening", relative)
	}
	return true, nil
}

func snapshotRootPlan(root *os.Root, relative string) (plandoc.Document, bool, error) {
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return plandoc.Document{}, false, nil
	}
	if err != nil {
		return plandoc.Document{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return plandoc.Document{}, false, fmt.Errorf("%w: plan target must be a regular non-symlink file", ErrPlanInvalid)
	}
	if info.Size() > int64(plandoc.MaxDocumentBytes) {
		return plandoc.Document{}, false, fmt.Errorf("%w: plan target exceeds size limit", ErrPlanInvalid)
	}
	file, err := root.Open(relative)
	if err != nil {
		return plandoc.Document{}, false, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		if statErr != nil {
			return plandoc.Document{}, false, statErr
		}
		return plandoc.Document{}, false, fmt.Errorf("%w: plan target changed while opening", ErrPlanStale)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(plandoc.MaxDocumentBytes)+1))
	closeErr := file.Close()
	if readErr != nil {
		return plandoc.Document{}, false, readErr
	}
	if closeErr != nil {
		return plandoc.Document{}, false, closeErr
	}
	if len(data) > plandoc.MaxDocumentBytes {
		return plandoc.Document{}, false, fmt.Errorf("%w: plan target exceeds size limit", ErrPlanInvalid)
	}
	document, err := plandoc.Parse(data)
	if err != nil {
		return plandoc.Document{}, false, fmt.Errorf("%w: malformed existing plan: %v", ErrPlanInvalid, err)
	}
	return document, true, nil
}

func validatePlanMode(mode PlanAuthoringMode, exists bool) error {
	switch {
	case mode == PlanCreate && exists:
		return fmt.Errorf("%w: target already exists", ErrPlanConflict)
	case mode == PlanUpdate && !exists:
		return fmt.Errorf("%w: target does not exist", ErrPlanNotFound)
	default:
		return nil
	}
}

func planAuthoringResult(target planAuthoringTarget, name string, document plandoc.Document, dryRun bool) PlanAuthoringResult {
	result := PlanAuthoringResult{
		Path: filepath.Join(target.basePath, target.targetRel), Name: name, ContentID: document.ContentID(),
		Status: document.Status(), PhaseCount: document.PhaseCount(), CompletedPhaseCount: document.CompletedPhaseCount(), DryRun: dryRun,
	}
	if next, ok := document.NextPhase(); ok {
		result.NextPhase = &PlanPhaseSummary{ID: next.ID, Title: next.Title}
	}
	return result
}
