package codegraph

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/workspace"
)

const (
	ToolName            = "codegraph_explore"
	MaxQueryBytes       = 16 << 10
	MaxProjectPathBytes = 4 << 10
	ExploreTimeout      = 30 * time.Second
)

type EffectiveState string

const (
	StateDisabled    EffectiveState = "disabled"
	StateUnavailable EffectiveState = "unavailable"
	StateUnindexed   EffectiveState = "unindexed"
	StateReady       EffectiveState = "ready"
)

type ExploreInput struct {
	WorkspaceID string `json:"workspace_id"`
	Query       string `json:"query"`
	Path        string `json:"path,omitempty"`
}

type ToolState struct {
	State       EffectiveState `json:"state"`
	WorkspaceID string         `json:"workspace_id"`
	ProjectPath string         `json:"path,omitempty"`
	Guidance    string         `json:"guidance,omitempty"`
	Output      string         `json:"output,omitempty"`
}

func Effective(executable ExecutableState, index IndexState) EffectiveState {
	switch executable {
	case ExecutableDisabled:
		return StateDisabled
	case ExecutableConfigured, ExecutableSystem, ExecutableManaged:
		if index == IndexIndexed {
			return StateReady
		}
		return StateUnindexed
	default:
		return StateUnavailable
	}
}

func NormalizeExploreInput(workspaceID, query, projectPath string) (ExploreInput, error) {
	input := ExploreInput{
		WorkspaceID: strings.TrimSpace(workspaceID),
		Query:       strings.TrimSpace(query),
		Path:        strings.TrimSpace(projectPath),
	}
	if input.WorkspaceID == "" {
		return ExploreInput{}, errors.New("workspace_id is required")
	}
	if input.Query == "" {
		return ExploreInput{}, errors.New("query is required")
	}
	if strings.ContainsRune(input.Query, 0) {
		return ExploreInput{}, errors.New("query contains a NUL byte")
	}
	if len([]byte(input.Query)) > MaxQueryBytes {
		return ExploreInput{}, fmt.Errorf("query exceeds %d-byte codegraph_explore limit", MaxQueryBytes)
	}
	if input.Path == "" || input.Path == "." {
		input.Path = ""
		return input, nil
	}
	if strings.ContainsRune(input.Path, 0) {
		return ExploreInput{}, errors.New("codegraph project path contains a NUL byte")
	}
	if len([]byte(input.Path)) > MaxProjectPathBytes {
		return ExploreInput{}, fmt.Errorf("codegraph project path exceeds %d-byte limit", MaxProjectPathBytes)
	}
	if filepath.IsAbs(input.Path) {
		return ExploreInput{}, errors.New("codegraph project path must be workspace-relative")
	}
	clean := filepath.Clean(input.Path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ExploreInput{}, errors.New("codegraph project path escapes workspace root")
	}
	input.Path = filepath.ToSlash(clean)
	return input, nil
}

func ValidateExploreInput(workspaceID, query, projectPath string) error {
	_, err := NormalizeExploreInput(workspaceID, query, projectPath)
	return err
}

func ExploreInputSchema() string {
	return "{\"type\":\"object\",\"properties\":{\"workspace_id\":{\"type\":\"string\",\"minLength\":1},\"query\":{\"type\":\"string\",\"minLength\":1,\"maxLength\":16384},\"path\":{\"type\":\"string\",\"maxLength\":4096}},\"required\":[\"workspace_id\",\"query\"],\"additionalProperties\":false}"
}

func ExploreOutputSchema() string {
	return "{\"type\":\"object\",\"properties\":{\"state\":{\"type\":\"string\",\"enum\":[\"disabled\",\"unavailable\",\"unindexed\",\"ready\"]},\"workspace_id\":{\"type\":\"string\"},\"path\":{\"type\":\"string\"},\"guidance\":{\"type\":\"string\"},\"output\":{\"type\":\"string\"}},\"required\":[\"state\",\"workspace_id\"],\"additionalProperties\":false}"
}

func ExploreArgs(query string) []string {
	return []string{"explore", strings.TrimSpace(query)}
}

func Explore(ctx context.Context, runtime *Runtime, workspaces *workspace.Manager, input ExploreInput) (ToolState, error) {
	normalized, err := NormalizeExploreInput(input.WorkspaceID, input.Query, input.Path)
	if err != nil {
		return ToolState{}, err
	}
	if workspaces == nil {
		return ToolState{}, errors.New("workspace manager is unavailable")
	}
	item, projectRoot, err := workspaces.ResolveDirectory(normalized.WorkspaceID, normalized.Path)
	if err != nil {
		return ToolState{}, err
	}
	relative, err := filepath.Rel(item.Path, projectRoot)
	if err != nil {
		return ToolState{}, err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return ToolState{}, errors.New("codegraph project path escapes registered workspace root")
	}
	state := ToolState{WorkspaceID: item.ID, ProjectPath: projectRoot}
	if runtime == nil {
		state.State = StateUnavailable
		state.Guidance = "CodeGraph runtime is unavailable. Configure or install CodeGraph before using codegraph_explore."
		return state, nil
	}
	runtimeStatus, statusErr := runtime.Status()
	if statusErr != nil {
		state.State = StateUnavailable
		state.Guidance = "CodeGraph executable is unavailable or invalid. Check the CodeGraph integration configuration before exploring."
		return state, nil
	}
	if runtimeStatus.Resolution.Source == ExecutableDisabled || !runtimeStatus.Enabled {
		state.State = StateDisabled
		state.Guidance = "CodeGraph is disabled. Enable the CodeGraph integration before using codegraph_explore."
		return state, nil
	}
	if runtimeStatus.Resolution.Source == ExecutableUnavailable || strings.TrimSpace(runtimeStatus.Resolution.Path) == "" {
		state.State = StateUnavailable
		state.Guidance = "CodeGraph executable is unavailable. Configure a valid executable or install the managed CodeGraph asset."
		return state, nil
	}
	store, err := workspaces.LocalState(item.ID)
	if err != nil {
		return ToolState{}, err
	}
	if InspectIndex(projectRoot) {
		if _, err := ReconcileProjectConfig(ctx, store, item.ID, projectRoot, relative); err != nil {
			return ToolState{}, fmt.Errorf("prepare CodeGraph project configuration: %w", err)
		}
	}
	workspaceStatus := InspectWorkspace(runtimeStatus, store, item.ID, projectRoot, relative)
	state.State = Effective(runtimeStatus.Resolution.Source, workspaceStatus.IndexState)
	if state.State == StateUnindexed {
		state.Guidance = "This workspace project is not indexed. Initialize CodeGraph for this registered workspace project before exploring."
		return state, nil
	}
	if state.State != StateReady {
		state.State = StateUnavailable
		state.Guidance = "CodeGraph is not ready for this workspace project."
		return state, nil
	}

	if workspaceStatus.Freshness != FreshnessFresh {
		lock, err := AcquireWorkspaceMutationLock(store)
		if err != nil {
			return ToolState{}, fmt.Errorf("prepare CodeGraph workspace: %w", err)
		}
		defer lock.Release()

		workspaceStatus = InspectWorkspace(runtimeStatus, store, item.ID, projectRoot, relative)
		if workspaceStatus.IndexState != IndexIndexed {
			state.State = StateUnindexed
			state.Guidance = "This workspace project is not indexed. Initialize CodeGraph for this registered workspace project before exploring."
			return state, nil
		}
		if workspaceStatus.Freshness != FreshnessFresh || ProjectConfigRequiresConservativeSync(projectRoot) {
			if _, err := runtime.ExecuteInDir(ctx, projectRoot, SyncArgs(projectRoot), SyncTimeout, MaxOutputBytes); err != nil {
				return ToolState{}, errors.New("CodeGraph could not refresh the workspace index before exploration")
			}
			if err := RecordWorkspaceLifecycle(store, item.ID, projectRoot, relative, "sync", time.Now()); err != nil {
				return ToolState{}, errors.New("CodeGraph refreshed the workspace index but could not record its freshness state")
			}
		}
	}

	result, err := runtime.ExecuteInDir(ctx, projectRoot, ExploreArgs(normalized.Query), ExploreTimeout, MaxOutputBytes)
	if err != nil {
		return ToolState{}, errors.New("CodeGraph exploration failed")
	}
	state.Output = strings.TrimSpace(result.Stdout)
	if state.Output == "" {
		state.Output = strings.TrimSpace(result.Stderr)
	}
	return state, nil
}
