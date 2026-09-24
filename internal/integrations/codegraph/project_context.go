package codegraph

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/workspace"
)

const projectContextSource = "cm integration:codegraph"

type ProjectContextRuntimeProvider func() (*Runtime, error)

func ProjectContextProjectionProvider(runtimeProvider ProjectContextRuntimeProvider, workspaces *workspace.Manager) projectcontext.IntegrationProjectionProvider {
	return func(_ context.Context, workspaceID, projectRoot string) (projectcontext.IntegrationProjection, error) {
		return projectContextProjection(runtimeProvider, workspaces, workspaceID, projectRoot)
	}
}

func projectContextProjection(runtimeProvider ProjectContextRuntimeProvider, workspaces *workspace.Manager, workspaceID, projectRoot string) (projectcontext.IntegrationProjection, error) {
	if workspaces == nil {
		return projectcontext.IntegrationProjection{}, errors.New("workspace manager is unavailable")
	}
	item, err := workspaces.Get(strings.TrimSpace(workspaceID))
	if err != nil {
		return projectcontext.IntegrationProjection{}, err
	}
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	relative, err := filepath.Rel(item.Path, projectRoot)
	if err != nil {
		return projectcontext.IntegrationProjection{}, err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return projectcontext.IntegrationProjection{}, nil
	}
	if runtimeProvider == nil {
		return unavailableProjectContextProjection("CodeGraph runtime is unavailable."), nil
	}
	runtime, runtimeErr := runtimeProvider()
	if runtimeErr != nil || runtime == nil {
		return unavailableProjectContextProjection("CodeGraph runtime is unavailable."), nil
	}
	status, statusErr := runtime.Status()
	if statusErr != nil {
		return unavailableProjectContextProjection("CodeGraph is enabled, but its executable is unavailable or invalid."), nil
	}
	if !status.Enabled || status.Resolution.Source == ExecutableDisabled {
		return projectcontext.IntegrationProjection{}, nil
	}
	if status.Resolution.Source == ExecutableUnavailable || strings.TrimSpace(status.Resolution.Path) == "" {
		return unavailableProjectContextProjection("CodeGraph is enabled, but its executable is unavailable or invalid."), nil
	}

	store, err := workspaces.LocalState(item.ID)
	if err != nil {
		return projectcontext.IntegrationProjection{}, err
	}
	workspaceStatus := InspectWorkspace(status, store, item.ID, projectRoot, relative)
	if workspaceStatus.IndexState != IndexIndexed {
		return projectcontext.IntegrationProjection{
			Diagnostics: []instructioncontext.IntegrationDiagnostic{{
				ID:      "CodeGraph",
				Source:  projectContextSource,
				State:   "uninitialized",
				Message: "CodeGraph is enabled for this project, but the graph is not initialized. Initialize the registered workspace project before relying on codegraph_explore.",
			}},
		}, nil
	}

	projection := projectcontext.IntegrationProjection{
		Instructions: []instructioncontext.IntegrationInstruction{{
			ID:     "CodeGraph",
			Source: projectContextSource,
			Content: strings.Join([]string{
				"Use the CM-native `codegraph_explore` tool first for codebase architecture, symbol relationships, call paths, dependency exploration, and targeted source discovery before broad grep/read exploration.",
				"Prefer precise workspace file reads when exact source text, line-level details, or files outside the indexed project are needed; CodeGraph complements rather than replaces targeted reads.",
				"`codegraph_explore` performs a bounded incremental sync before querying an indexed project; do not start `codegraph serve --mcp` or wire the upstream MCP server.",
				"Treat source returned by `codegraph_explore` as already read unless it is stale, incomplete, unavailable, or outside the indexed project.",
			}, "\n"),
		}},
	}
	if workspaceStatus.Freshness != FreshnessFresh {
		message := "CodeGraph index metadata indicates this project may be stale; codegraph_explore will perform its bounded pre-query sync before exploration."
		if workspaceStatus.DirtyReason != "" {
			message += " reason=" + workspaceStatus.DirtyReason
		}
		projection.Diagnostics = []instructioncontext.IntegrationDiagnostic{{
			ID:      "CodeGraph",
			Source:  projectContextSource,
			State:   "stale",
			Message: message,
		}}
	}
	return projection, nil
}

func unavailableProjectContextProjection(message string) projectcontext.IntegrationProjection {
	return projectcontext.IntegrationProjection{
		Diagnostics: []instructioncontext.IntegrationDiagnostic{{
			ID:      "CodeGraph",
			Source:  projectContextSource,
			State:   "unavailable",
			Message: message,
		}},
	}
}
