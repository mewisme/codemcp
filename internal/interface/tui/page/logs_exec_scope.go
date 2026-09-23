package page

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

type executionScopeMode string

const (
	executionScopeCombined  executionScopeMode = "combined"
	executionScopeWorkspace executionScopeMode = "workspace"
	executionScopeContainer executionScopeMode = "container"
)

type executionWorkspaceView string

const (
	executionWorkspaceCommands executionWorkspaceView = "commands"
	executionWorkspaceProcess  executionWorkspaceView = "process"
)

func normalizeExecutionWorkspaceView(view executionWorkspaceView) executionWorkspaceView {
	if view == executionWorkspaceProcess {
		return view
	}
	return executionWorkspaceCommands
}

func normalizeExecutionScopeMode(mode executionScopeMode) executionScopeMode {
	switch mode {
	case executionScopeWorkspace, executionScopeContainer:
		return mode
	default:
		return executionScopeCombined
	}
}

var deleteFinishedProcess = runtimecontrol.DeleteFinishedProcess

func cleanupFinishedProcessCmd(ctx context.Context, workspaceID, processID, executionID string, running bool) tea.Cmd {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(processID) == "" || strings.TrimSpace(executionID) == "" || running {
		return nil
	}
	return func() tea.Msg {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = deleteFinishedProcess(cleanupCtx, workspaceID, processID)
		return nil
	}
}

func (page *LogsPage) detachSelectedProcessCmd() tea.Cmd {
	if page == nil || page.exec.workspaceView != executionWorkspaceProcess {
		return nil
	}
	cmd := cleanupFinishedProcessCmd(page.ctx, page.exec.workspaceID, page.exec.processID, page.exec.processExecutionID, page.exec.processRunning)
	page.exec.workspaceView, page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = executionWorkspaceCommands, "", "", false
	page.refreshExecutionViewport()
	return cmd
}

func (page *LogsPage) cleanupSelectedFinishedProcess() {
	if page == nil || page.exec.workspaceView != executionWorkspaceProcess || page.exec.processRunning || page.exec.workspaceID == "" || page.exec.processID == "" || page.exec.processExecutionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = deleteFinishedProcess(ctx, page.exec.workspaceID, page.exec.processID)
}

func (page *LogsPage) refreshExecutionScope() {
	if page == nil {
		return
	}
	mode := normalizeExecutionScopeMode(page.exec.scopeMode)
	page.exec.scopeMode = mode
	page.exec.scopeStale, page.exec.scopeNotice = false, ""
	if mode == executionScopeCombined {
		return
	}
	manager := workspace.NewManager(workspace.DefaultStorePath())
	if mode == executionScopeWorkspace {
		item, err := manager.Get(page.exec.workspaceID)
		if err != nil {
			page.exec.scopeStale = true
			page.exec.scopeNotice = "Selected workspace unavailable: " + page.exec.workspaceID
			return
		}
		page.exec.workspaceID = item.ID
		return
	}
	context, err := manager.ResolveContainer(page.exec.containerID)
	if err != nil {
		page.exec.scopeStale = true
		page.exec.scopeNotice = "Selected workspace container unavailable: " + page.exec.containerID
		page.exec.containerMembers = map[string]struct{}{}
		return
	}
	page.exec.containerID, page.exec.containerName = context.Container.ID, context.Container.Name
	page.exec.containerMembers = make(map[string]struct{}, len(context.Workspaces))
	for _, item := range context.Workspaces {
		page.exec.containerMembers[item.ID] = struct{}{}
	}
}

func (page *LogsPage) visibleExecutionEvents() []shellruntime.ExecutionFeedEvent {
	if page == nil || len(page.exec.events) == 0 {
		return nil
	}
	if page.exec.scopeStale {
		return nil
	}
	result := make([]shellruntime.ExecutionFeedEvent, 0, len(page.exec.events))
	for _, event := range page.exec.events {
		if page.executionEventVisible(event) {
			result = append(result, event)
		}
	}
	return result
}

func (page *LogsPage) executionEventVisible(event shellruntime.ExecutionFeedEvent) bool {
	if page == nil || page.exec.scopeStale {
		return false
	}
	switch normalizeExecutionScopeMode(page.exec.scopeMode) {
	case executionScopeWorkspace:
		if event.WorkspaceID != page.exec.workspaceID {
			return false
		}
		if normalizeExecutionWorkspaceView(page.exec.workspaceView) == executionWorkspaceProcess {
			return page.exec.processExecutionID != "" && event.ExecutionID == page.exec.processExecutionID
		}
		return !executionEventIsProcess(event)
	case executionScopeContainer:
		_, ok := page.exec.containerMembers[event.WorkspaceID]
		return ok && !executionEventIsProcess(event)
	default:
		return !executionEventIsProcess(event)
	}
}

func executionEventIsProcess(event shellruntime.ExecutionFeedEvent) bool {
	return event.Execution != nil && event.Execution.Tool == "start_process"
}

func (page *LogsPage) executionScopeLabel() string {
	if page == nil {
		return string(executionScopeCombined)
	}
	switch normalizeExecutionScopeMode(page.exec.scopeMode) {
	case executionScopeWorkspace:
		if normalizeExecutionWorkspaceView(page.exec.workspaceView) == executionWorkspaceProcess {
			return "workspace · " + page.exec.workspaceID + " · process · " + page.exec.processID
		}
		return "workspace · " + page.exec.workspaceID + " · commands"
	case executionScopeContainer:
		name := strings.TrimSpace(page.exec.containerName)
		if name == "" {
			name = page.exec.containerID
		}
		return fmt.Sprintf("container · %s · %d workspaces", name, len(page.exec.containerMembers))
	default:
		return string(executionScopeCombined)
	}
}
