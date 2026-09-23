package page

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/tui/component"
)

func (page *RuntimePage) initRuntimeEditor() error {
	if page == nil || page.action == "" || page.editor != nil {
		return nil
	}
	page.err, page.notice = nil, ""
	switch strings.TrimSpace(page.action) {
	case "install":
		editor, data := newInstallEditor()
		page.pending, page.editor, page.installForm = InstallRun, &editor, data
	case "update":
		editor, data := newUpdateEditor()
		page.pending, page.editor, page.updateForm = UpdateApply, &editor, data
	default:
		return nil
	}
	page.resizeRuntimeEditor()
	return nil
}

func (page *RuntimePage) submitRuntimeEditor() tea.Cmd {
	if page == nil || page.editor == nil || page.editor.Submitting() {
		return nil
	}
	if err := page.editor.Validate(); err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	page.editor.SetSubmitting(true)
	return page.startOperation(page.pending)
}

func (page *RuntimePage) runtimeEditorParentNavigation() tea.Cmd {
	return func() tea.Msg { return NavigateMsg{Path: []string{"runtime"}, Replace: true} }
}

func (page *RuntimePage) runtimeEditorView(width, height int) string {
	if page == nil || page.editor == nil {
		return ""
	}
	page.width, page.height = width, height
	page.resizeRuntimeEditor()
	return page.editor.View()
}

func (page *RuntimePage) resizeRuntimeEditor() {
	if page == nil || page.editor == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	page.editor.Resize(page.width, page.height)
}

func (page *RuntimePage) runtimeEditorMouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil || page.editor == nil {
		return nil
	}
	return page.editor.MouseTargets(originX, originY, z)
}
