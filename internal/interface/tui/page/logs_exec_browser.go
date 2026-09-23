package page

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/interface/tui/component"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

type logsExecutionDetailMsg struct {
	id       string
	snapshot shellruntime.ExecutionSnapshot
	err      error
}

func (page *LogsPage) rebuildExecutionBrowser() {
	if page == nil || page.tab != logsTabCommandExec || page.resourceID != "" {
		return
	}
	latest := map[string]shellruntime.ExecutionInfo{}
	sequence := map[string]uint64{}
	for _, event := range page.visibleExecutionEvents() {
		if event.Execution != nil {
			latest[event.ExecutionID] = *event.Execution
		}
		if event.Sequence > sequence[event.ExecutionID] {
			sequence[event.ExecutionID] = event.Sequence
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool { return sequence[ids[i]] < sequence[ids[j]] })
	rows := make([]component.Row, 0, len(ids))
	for _, id := range ids {
		info := latest[id]
		title := compactParts(info.ID, info.Tool, info.Status)
		description := strings.TrimSpace(info.Command)
		meta := compactParts(info.WorkspaceID, info.Shell)
		search := compactParts(info.ID, info.Tool, info.Command, info.CWD, info.WorkspaceID, info.Status, info.CallID, info.Shell)
		rows = append(rows, component.Row{ID: info.ID, Title: title, Description: description, Meta: meta, Search: search})
	}
	selected := ""
	if row, ok := page.browser.Selected(); ok {
		selected = row.ID
	}
	if !page.exec.paused {
		selected = ""
	}
	_ = page.browser.ReplaceRows(rows, selected)
	if !page.exec.paused {
		page.browser.SelectLast()
	}
}

func (page *LogsPage) executionBrowserBody(width, height int) string {
	updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: max(1, width), Height: max(1, height)})
	page.browser = updated.(component.Browser)
	if len(page.visibleExecutionEvents()) == 0 {
		return component.Muted("Waiting for command executions")
	}
	return page.browser.BodyContent()
}

func (page *LogsPage) loadExecutionDetailCmd() tea.Cmd {
	id := strings.TrimSpace(page.resourceID)
	if id == "" {
		return nil
	}
	return func() tea.Msg {
		snapshot, err := runtimecontrol.GetExecution(page.ctx, id)
		return logsExecutionDetailMsg{id: id, snapshot: snapshot, err: err}
	}
}

func (page *LogsPage) finishExecutionDetail(msg logsExecutionDetailMsg) {
	if page.resourceID == "" || msg.id != page.resourceID {
		return
	}
	if msg.err != nil {
		page.err = msg.err
		page.detail = component.NewDetailPage("Execution · "+msg.id, "unavailable", component.Muted("Execution detail unavailable.")).WithTitleVisible(false)
		return
	}
	data, err := json.MarshalIndent(msg.snapshot, "", "  ")
	if err != nil {
		page.err = fmt.Errorf("encode execution detail: %w", err)
		return
	}
	content := component.RenderCodeBlock(string(data), "json", max(20, page.width))
	info := msg.snapshot.Execution
	meta := compactParts(info.Tool, info.Status, info.WorkspaceID, info.Shell)
	page.detail = component.NewDetailPage("Execution · "+info.ID, meta, content).WithTitleVisible(false)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
	page.err = nil
}
