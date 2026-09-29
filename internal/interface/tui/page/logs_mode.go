package page

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/interface/tui/component"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

type logsScopeState struct {
	mode             executionScopeMode
	workspaceID      string
	containerID      string
	containerName    string
	containerMembers map[string]struct{}
	stale            bool
}

type logsModeStage string

const (
	logsModeStageMode             logsModeStage = "mode"
	logsModeStageWorkspace        logsModeStage = "workspace"
	logsModeStageContainer        logsModeStage = "container"
	logsModeStageProcessWorkspace logsModeStage = "process-workspace"
	logsModeStageProcess          logsModeStage = "process"
)

type logsModeOption struct {
	label string
	value string
}

type logsModeDialog struct {
	stage       logsModeStage
	index       int
	options     []logsModeOption
	workspaceID string
}

type logsModeProcessesMsg struct {
	workspaceID string
	processes   []shellruntime.ProcessInfo
	err         error
}

func newLogsScopeState() logsScopeState {
	return logsScopeState{mode: executionScopeCombined, containerMembers: map[string]struct{}{}}
}

func (page *LogsPage) openLogsModeDialog() tea.Cmd {
	options := []logsModeOption{{label: "Combined", value: string(executionScopeCombined)}, {label: "Workspace", value: string(executionScopeWorkspace)}, {label: "Container", value: string(executionScopeContainer)}}
	if page.tab == logsTabCommandExec {
		options = append(options, logsModeOption{label: "Process", value: "process"})
	}
	page.modeDialog = &logsModeDialog{stage: logsModeStageMode, options: options}
	return nil
}

func (page *LogsPage) updateLogsModeDialog(msg tea.KeyPressMsg) tea.Cmd {
	if page.modeDialog == nil {
		return nil
	}
	dialog := page.modeDialog
	switch msg.String() {
	case "esc":
		page.modeDialog = nil
		return nil
	case "up", "k":
		if len(dialog.options) > 0 {
			dialog.index = (dialog.index - 1 + len(dialog.options)) % len(dialog.options)
		}
		return nil
	case "down", "j":
		if len(dialog.options) > 0 {
			dialog.index = (dialog.index + 1) % len(dialog.options)
		}
		return nil
	case "enter":
		if len(dialog.options) == 0 {
			return nil
		}
		option := dialog.options[dialog.index]
		switch dialog.stage {
		case logsModeStageMode:
			switch option.value {
			case string(executionScopeCombined):
				page.applyLogsScope(executionScopeCombined, "", "")
				page.modeDialog = nil
				return page.logsTimelineDeferredWorkCmd()
			case string(executionScopeWorkspace):
				return page.loadLogsWorkspaceOptions(logsModeStageWorkspace)
			case string(executionScopeContainer):
				return page.loadLogsContainerOptions()
			case "process":
				return page.loadLogsWorkspaceOptions(logsModeStageProcessWorkspace)
			}
		case logsModeStageWorkspace:
			page.applyLogsScope(executionScopeWorkspace, option.value, "")
			page.modeDialog = nil
			return page.logsTimelineDeferredWorkCmd()
		case logsModeStageContainer:
			page.applyLogsScope(executionScopeContainer, "", option.value)
			page.modeDialog = nil
			return page.logsTimelineDeferredWorkCmd()
		case logsModeStageProcessWorkspace:
			dialog.workspaceID = option.value
			return func() tea.Msg {
				processes, err := runtimecontrol.ListProcesses(page.ctx, option.value)
				return logsModeProcessesMsg{workspaceID: option.value, processes: processes, err: err}
			}
		case logsModeStageProcess:
			page.applyExecutionProcess(dialog.workspaceID, option.value)
			page.modeDialog = nil
			return page.logsTimelineDeferredWorkCmd()
		}
	}
	return nil
}

func (page *LogsPage) logsTimelineDeferredWorkCmd() tea.Cmd {
	if page == nil || page.view != logsViewTimeline || page.resourceID != "" {
		return nil
	}
	switch page.tab {
	case logsTabCommandExec:
		return page.executionTimelineRenderCmd()
	case logsTabToolCalls:
		return page.toolCallTimelineHydrateCmd()
	default:
		return nil
	}
}

func (page *LogsPage) loadLogsWorkspaceOptions(stage logsModeStage) tea.Cmd {
	manager := workspace.NewManager(workspace.DefaultStorePath())
	items, err := manager.List()
	if err != nil {
		page.err = err
		return nil
	}
	options := make([]logsModeOption, 0, len(items))
	for _, item := range items {
		options = append(options, logsModeOption{label: item.Path + " · " + item.ID, value: item.ID})
	}
	page.modeDialog = &logsModeDialog{stage: stage, options: options}
	return nil
}

func (page *LogsPage) loadLogsContainerOptions() tea.Cmd {
	manager := workspace.NewManager(workspace.DefaultStorePath())
	items, err := manager.ListContainers()
	if err != nil {
		page.err = err
		return nil
	}
	options := make([]logsModeOption, 0, len(items))
	for _, item := range items {
		options = append(options, logsModeOption{label: fmt.Sprintf("%s · %s · %d workspaces", item.Name, item.ID, len(item.WorkspaceIDs)), value: item.ID})
	}
	page.modeDialog = &logsModeDialog{stage: logsModeStageContainer, options: options}
	return nil
}

func (page *LogsPage) finishLogsModeProcesses(msg logsModeProcessesMsg) {
	if page.modeDialog == nil || page.modeDialog.stage != logsModeStageProcessWorkspace || page.modeDialog.workspaceID != msg.workspaceID {
		return
	}
	if msg.err != nil {
		page.err = msg.err
		page.modeDialog = nil
		return
	}
	options := make([]logsModeOption, 0, len(msg.processes))
	for i := len(msg.processes) - 1; i >= 0; i-- {
		item := msg.processes[i]
		status := "running"
		if !item.Running {
			status = "exited"
			if item.ExitCode != nil {
				status = fmt.Sprintf("exited %d", *item.ExitCode)
			}
		}
		options = append(options, logsModeOption{label: fmt.Sprintf("%s · pid %d · %s", status, item.PID, strings.TrimSpace(item.Command)), value: item.ID})
	}
	page.modeDialog.stage, page.modeDialog.options, page.modeDialog.index = logsModeStageProcess, options, 0
}

func (page *LogsPage) applyLogsScope(mode executionScopeMode, workspaceID, containerID string) {
	mode = normalizeExecutionScopeMode(mode)
	switch page.tab {
	case logsTabCommandExec:
		page.detachSelectedProcessCmd()
		page.exec.scopeMode, page.exec.workspaceID, page.exec.containerID = mode, strings.TrimSpace(workspaceID), strings.TrimSpace(containerID)
		page.exec.workspaceView, page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = executionWorkspaceCommands, "", "", false
		page.refreshExecutionScope()
	case logsTabToolCalls:
		page.tools.scope.mode, page.tools.scope.workspaceID, page.tools.scope.containerID = mode, strings.TrimSpace(workspaceID), strings.TrimSpace(containerID)
		page.refreshToolCallScope()
	default:
		page.runtimeScope.mode, page.runtimeScope.workspaceID, page.runtimeScope.containerID = mode, strings.TrimSpace(workspaceID), strings.TrimSpace(containerID)
		page.refreshRuntimeScope()
	}
	page.invalidateActiveTimelineWindow()
	page.refreshActiveLogsView()
}

func (page *LogsPage) applyExecutionProcess(workspaceID, processID string) {
	processes, err := runtimecontrol.ListProcesses(page.ctx, workspaceID)
	if err != nil {
		page.err = err
		return
	}
	for _, item := range processes {
		if item.ID != processID {
			continue
		}
		page.exec.scopeMode, page.exec.workspaceID, page.exec.workspaceView = executionScopeWorkspace, workspaceID, executionWorkspaceProcess
		page.exec.processID, page.exec.processExecutionID, page.exec.processRunning = item.ID, item.ExecutionID, item.Running
		page.refreshExecutionScope()
		page.exec.window.invalidate()
		page.refreshActiveLogsView()
		return
	}
	page.err = fmt.Errorf("process not found: %s", processID)
}

func (page *LogsPage) refreshRuntimeScope() {
	refreshLogsScope(&page.runtimeScope)
}

func (page *LogsPage) refreshToolCallScope() {
	refreshLogsScope(&page.tools.scope)
}

func refreshLogsScope(scope *logsScopeState) {
	if scope == nil {
		return
	}
	scope.stale = false
	scope.containerName = ""
	scope.containerMembers = map[string]struct{}{}
	if scope.mode != executionScopeContainer || scope.containerID == "" {
		return
	}
	manager := workspace.NewManager(workspace.DefaultStorePath())
	containers, err := manager.ListContainers()
	if err != nil {
		scope.stale = true
		return
	}
	for _, item := range containers {
		if item.ID != scope.containerID {
			continue
		}
		scope.containerName = item.Name
		for _, workspaceID := range item.WorkspaceIDs {
			scope.containerMembers[workspaceID] = struct{}{}
		}
		return
	}
	scope.stale = true
}

func (page *LogsPage) matchLogsScope(workspaceID string) bool {
	return matchScope(page.runtimeScope, workspaceID)
}

func matchScope(scope logsScopeState, workspaceID string) bool {
	switch normalizeExecutionScopeMode(scope.mode) {
	case executionScopeWorkspace:
		return scope.workspaceID != "" && workspaceID == scope.workspaceID
	case executionScopeContainer:
		_, ok := scope.containerMembers[workspaceID]
		return !scope.stale && ok
	default:
		return true
	}
}

func logsScopeLabel(scope logsScopeState) string {
	switch normalizeExecutionScopeMode(scope.mode) {
	case executionScopeWorkspace:
		return compactParts("workspace", scope.workspaceID)
	case executionScopeContainer:
		return compactParts("container", scope.containerName, scope.containerID)
	default:
		return "combined"
	}
}

func (page *LogsPage) logsModeDialogView(width int) string {
	if page.modeDialog == nil {
		return ""
	}
	title := "Stream Mode"
	switch page.modeDialog.stage {
	case logsModeStageWorkspace:
		title = "Workspace"
	case logsModeStageContainer:
		title = "Container"
	case logsModeStageProcessWorkspace:
		title = "Process Workspace"
	case logsModeStageProcess:
		title = "Process"
	}
	lines := []string{component.Title(title), ""}
	if len(page.modeDialog.options) == 0 {
		lines = append(lines, component.Muted("No options available"))
	} else {
		for index, option := range page.modeDialog.options {
			prefix := "  "
			if index == page.modeDialog.index {
				prefix = "> "
			}
			lines = append(lines, prefix+option.label)
		}
	}
	lines = append(lines, "", component.Muted("↑/↓ select   Enter apply   Esc cancel"))
	return component.WrapModalBody(strings.Join(lines, "\n"), overlayWidth(width, 72))
}

func (page *LogsPage) modeDialogMouseTargets(originX, originY, z int) []component.MouseTarget {
	return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
}
