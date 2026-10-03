package page

import (
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type managedAgentSpawnFormData struct {
	WorkspaceID     string
	Prompt          string
	Backend         string
	Model           string
	ReasoningEffort string
}

func newManagedAgentSpawnEditor() (component.Editor, *managedAgentSpawnFormData) {
	data := &managedAgentSpawnFormData{}
	form := component.NewEditorForm(component.Group(
		component.Input("Workspace ID", &data.WorkspaceID).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("workspace ID is required")
			}
			return nil
		}),
		component.Text("Prompt", &data.Prompt).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("prompt is required")
			}
			return nil
		}),
		component.Input("Backend", &data.Backend),
		component.Input("Model", &data.Model),
		component.Input("Reasoning effort", &data.ReasoningEffort),
	))
	editor := component.NewEditor("spawn", component.EditorSection{
		ID: "agent", Title: "Spawn managed agent",
		Description: "Delegate one meaningful, independently scoped task to the running CodeMCP runtime.",
		Form:        form,
	})
	return editor, data
}

func (data *managedAgentSpawnFormData) Input() application.ManagedAgentSpawnInput {
	if data == nil {
		return application.ManagedAgentSpawnInput{}
	}
	return application.ManagedAgentSpawnInput{
		WorkspaceID:     strings.TrimSpace(data.WorkspaceID),
		Prompt:          strings.TrimSpace(data.Prompt),
		Backend:         strings.TrimSpace(data.Backend),
		Model:           strings.TrimSpace(data.Model),
		ReasoningEffort: strings.TrimSpace(data.ReasoningEffort),
	}
}

type managedAgentSendFormData struct {
	Message string
}

func newManagedAgentSendEditor(agentID string) (component.Editor, *managedAgentSendFormData) {
	data := &managedAgentSendFormData{}
	form := component.NewEditorForm(component.Group(
		component.Text("Message", &data.Message).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("message is required")
			}
			return nil
		}),
	))
	editor := component.NewEditor("send", component.EditorSection{
		ID: "message", Title: "Send follow-up",
		Description: strings.TrimSpace(agentID) + " · only live idle agents accept follow-up messages.",
		Form:        form,
	})
	return editor, data
}

func (data *managedAgentSendFormData) Input(agentID string) application.ManagedAgentSendInput {
	if data == nil {
		return application.ManagedAgentSendInput{AgentID: strings.TrimSpace(agentID)}
	}
	return application.ManagedAgentSendInput{
		AgentID: strings.TrimSpace(agentID),
		Message: strings.TrimSpace(data.Message),
	}
}
