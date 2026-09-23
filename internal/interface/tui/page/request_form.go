package page

import (
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type requestCreateFormData struct {
	WorkspaceID string
	Title       string
	Command     string
}

type requestResolveFormData struct {
	Reason string
}

func newRequestCreateEditor() (component.Editor, *requestCreateFormData) {
	data := &requestCreateFormData{WorkspaceID: "ws_dummy", Title: "Allow test command", Command: "echo test approval"}
	form := component.NewEditorForm(component.Group(
		component.Input("Workspace ID (synthetic request label)", &data.WorkspaceID),
		component.Input("Title (human-readable approval title)", &data.Title),
		component.Input("Command (displayed only; not executed)", &data.Command),
	))
	editor := component.NewEditor("create", component.EditorSection{ID: "request", Title: "Test Request", Description: "Create a synthetic approval request for testing the approval workflow. The command is displayed only and is never executed.", Form: form})
	return editor, data
}

func newRequestResolveEditor(request approval.Request, approve bool) (component.Editor, *requestResolveFormData) {
	data := &requestResolveFormData{}
	action, title := "deny", "Deny Request"
	if approve {
		action, title = "approve", "Approve Request"
	}
	form := component.NewEditorForm(component.Group(component.Input("Reason (optional)", &data.Reason)))
	description := request.ID + " · " + request.TargetTool
	editor := component.NewEditor(action, component.EditorSection{ID: "resolution", Title: title, Description: description, Form: form})
	return editor, data
}
