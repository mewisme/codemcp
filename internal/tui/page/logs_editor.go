package page

import (
	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/tui/component"
)

func (page *LogsPage) initFilterEditor() {
	if page == nil || page.editor != nil {
		return
	}
	editor, data := newLogsFilterEditor(page.options, page.visibility)
	page.editor, page.filterForm = &editor, data
	page.resizeFilterEditor()
}

func (page *LogsPage) closeFilterEditor() tea.Cmd {
	if page == nil {
		return nil
	}
	page.editor, page.filterForm, page.action = nil, nil, ""
	return func() tea.Msg { return NavigateMsg{Path: []string{"logs"}, Replace: true, PreservePage: true} }
}

func (page *LogsPage) filterEditorView(width, height int) string {
	if page == nil || page.editor == nil {
		return ""
	}
	page.width, page.height = width, height
	page.resizeFilterEditor()
	return page.editor.View()
}

func (page *LogsPage) resizeFilterEditor() {
	if page == nil || page.editor == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	page.editor.Resize(page.width, page.height)
}

func (page *LogsPage) filterEditorMouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil || page.editor == nil {
		return nil
	}
	return page.editor.MouseTargets(originX, originY, z)
}
