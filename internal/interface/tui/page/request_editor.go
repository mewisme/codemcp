package page

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

func (page *RequestsPage) initCreateEditor() {
	if page == nil || page.editor != nil {
		return
	}
	editor, data := newRequestCreateEditor()
	page.editor, page.createForm = &editor, data
	page.resizeRequestEditor()
}

func (page *RequestsPage) initResolveEditor(request approval.Request, approve bool) error {
	if page == nil {
		return fmt.Errorf("approval inbox unavailable")
	}
	if err := validateResolvableRequest(request, time.Now()); err != nil {
		return err
	}
	editor, data := newRequestResolveEditor(request, approve)
	page.editor, page.resolveForm = &editor, data
	page.resolveApprove, page.resolveID = approve, request.ID
	page.resizeRequestEditor()
	return nil
}

func (page *RequestsPage) requestEditorView(width, height int) string {
	if page == nil || page.editor == nil {
		return ""
	}
	page.width, page.height = width, height
	page.resizeRequestEditor()
	return page.editor.View()
}

func (page *RequestsPage) resizeRequestEditor() {
	if page == nil || page.editor == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	page.editor.Resize(page.width, page.height)
}

func (page *RequestsPage) requestEditorMouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil || page.editor == nil {
		return nil
	}
	return page.editor.MouseTargets(originX, originY, z)
}

func (page *RequestsPage) closeRequestEditor() tea.Cmd {
	if page == nil {
		return nil
	}
	mode, id := page.mode, page.resourceID
	page.editor, page.createForm, page.resolveForm = nil, nil, nil
	page.resolveID, page.action = "", ""
	if id == "" {
		return requestNavigateCmd(mode, "", "", true)
	}
	return requestNavigateCmd(mode, id, "", true)
}

func validateResolvableRequest(request approval.Request, now time.Time) error {
	if request.Status != approval.StatusPending {
		return fmt.Errorf("request %s is %s and cannot be resolved", request.ID, request.Status)
	}
	if !request.ExpiresAt.IsZero() && !now.Before(request.ExpiresAt) {
		return fmt.Errorf("request %s has expired and cannot be resolved", request.ID)
	}
	return nil
}

func requestResolveRoute(mode requestMode, id string, approve bool) tea.Cmd {
	action := "deny"
	if approve {
		action = "approve"
	}
	return func() tea.Msg { return NavigateMsg{Path: append(requestRoutePath(mode, id, ""), action)} }
}

func requestEditorError(editor *component.Editor, err error) {
	if editor != nil {
		editor.SetSubmitting(false)
		editor.SetFeedback("", err)
	}
}

func requestReason(data *requestResolveFormData) string {
	if data == nil {
		return ""
	}
	return strings.TrimSpace(data.Reason)
}
