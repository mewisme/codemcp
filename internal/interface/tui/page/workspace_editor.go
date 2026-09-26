package page

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

func (page *WorkspacePage) initWorkspaceEditor() error {
	if page == nil {
		return nil
	}
	command, err := page.workspaceEditorCommand()
	if err != nil {
		return err
	}
	page.command, page.targetID, page.value, page.members = command, page.resourceID, "", nil
	var section component.EditorSection
	primary := "save"
	switch command {
	case WorkspaceRegister:
		if cwd, err := os.Getwd(); err == nil {
			page.value = cwd
		}
		field := component.NewPathField("Workspace path", &page.value, component.PathFieldOptions{Kind: component.PathKindDirectory, Validate: requiredValue("workspace path")})
		section = component.EditorSection{ID: "workspace", Title: "Workspace", Description: "Choose the project root to register. Use Ctrl+O to switch between the filesystem picker and manual path input.", Form: component.NewEditorForm(component.Group(field))}
		primary = "register"
	case WorkspaceRelocate:
		item, err := page.manager.Get(page.targetID)
		if err != nil {
			return err
		}
		page.value = item.Path
		field := component.NewPathField("New workspace path", &page.value, component.PathFieldOptions{Kind: component.PathKindDirectory, Validate: requiredValue("workspace path")})
		section = component.EditorSection{ID: "workspace", Title: "Relocate", Description: "Rebind this workspace after its project directory was renamed or moved. The previous workspace ID remains a legacy alias.", Form: component.NewEditorForm(component.Group(field))}
		primary = "relocate"
	case WorkspaceAccessAdd:
		if _, err := page.manager.Get(page.targetID); err != nil {
			return err
		}
		field := component.NewPathField("Additional directory", &page.value, component.PathFieldOptions{Kind: component.PathKindDirectory, Validate: requiredValue("directory")})
		section = component.EditorSection{ID: "access", Title: "Access", Description: "Grant this workspace access to one additional directory.", Form: component.NewEditorForm(component.Group(field))}
		primary = "add"
	case WorkspaceAccessRemove:
		item, err := page.manager.Get(page.targetID)
		if err != nil {
			return err
		}
		if len(item.AllowDirs) == 0 {
			return fmt.Errorf("workspace has no additional directories")
		}
		page.value = item.AllowDirs[0]
		options := make([]huh.Option[string], 0, len(item.AllowDirs))
		for _, value := range item.AllowDirs {
			options = append(options, huh.NewOption(value, value))
		}
		section = component.EditorSection{ID: "access", Title: "Access", Description: "Choose the additional directory to revoke from this workspace.", Form: component.NewEditorForm(component.Group(component.Select("Directory to remove", &page.value, options...)))}
		primary = "remove"
	case WorkspaceContainerCreate:
		section = component.EditorSection{ID: "container", Title: "Container", Description: "Create a workspace container for grouping registered workspaces.", Form: component.NewEditorForm(component.Group(component.Input("Container name", &page.value).Validate(requiredValue("container name"))))}
		primary = "create"
	case WorkspaceContainerRename:
		item, err := page.manager.GetContainer(page.targetID)
		if err != nil {
			return err
		}
		page.value = item.Name
		section = component.EditorSection{ID: "container", Title: "Container", Description: "Change the display name of this workspace container.", Form: component.NewEditorForm(component.Group(component.Input("Container name", &page.value).Validate(requiredValue("container name"))))}
	case WorkspaceContainerMembers:
		item, err := page.manager.GetContainer(page.targetID)
		if err != nil {
			return err
		}
		items, err := page.manager.List()
		if err != nil {
			return err
		}
		page.members = append([]string(nil), item.WorkspaceIDs...)
		options := make([]huh.Option[string], 0, len(items))
		for _, workspaceItem := range items {
			options = append(options, huh.NewOption(workspaceMemberLabel(workspaceItem), workspaceItem.ID))
		}
		field := component.MultiSelect("Container workspaces", &page.members, options...).Filterable(true).Height(workspaceMemberPickerHeight(len(items), page.height))
		field.DescriptionFunc(func() string { return fmt.Sprintf("%d selected / %d available", len(page.members), len(items)) }, &page.members)
		section = component.EditorSection{ID: "workspaces", Title: "Workspaces", Description: "Select the registered workspaces that belong to this container.", Form: component.NewEditorForm(component.Group(field))}
	default:
		return fmt.Errorf("unsupported workspace editor action: %s", command)
	}
	editor := component.NewEditor(primary, section)
	page.editor = &editor
	page.resizeWorkspaceEditor()
	return nil
}

func (page *WorkspacePage) workspaceEditorCommand() (WorkspaceCommand, error) {
	if page.containers {
		switch {
		case page.action == "create" && page.resourceID == "":
			return WorkspaceContainerCreate, nil
		case page.action == "edit" && page.resourceID != "" && page.section == "":
			return WorkspaceContainerRename, nil
		case page.action == "edit" && page.resourceID != "" && page.section == "workspaces":
			return WorkspaceContainerMembers, nil
		}
		return "", fmt.Errorf("unsupported container editor route")
	}
	switch {
	case page.action == "register" && page.resourceID == "":
		return WorkspaceRegister, nil
	case page.action == "relocate" && page.resourceID != "" && page.section == "":
		return WorkspaceRelocate, nil
	case page.action == "add" && page.resourceID != "" && page.section == "access":
		return WorkspaceAccessAdd, nil
	case page.action == "remove" && page.resourceID != "" && page.section == "access":
		return WorkspaceAccessRemove, nil
	default:
		return "", fmt.Errorf("unsupported workspace editor route")
	}
}

func (page *WorkspacePage) submitWorkspaceEditor() tea.Cmd {
	if page == nil || page.editor == nil {
		return nil
	}
	if err := page.editor.Validate(); err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	err := page.applyWorkspaceEditor()
	if err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	page.editor.Accept()
	message := workspaceSuccess(page.command)
	return tea.Batch(page.workspaceEditorParentNavigation(), func() tea.Msg { return ToastMsg{Title: "Workspace", Message: message, Tone: component.ToneSuccess} })
}

func (page *WorkspacePage) applyWorkspaceEditor() error {
	if page == nil {
		return nil
	}
	var err error
	operations := page.workspaceOperations()
	switch page.command {
	case WorkspaceRegister:
		_, err = operations.Register(page.ctx, page.value)
	case WorkspaceRelocate:
		var relocated application.Result[application.WorkspaceRelocation]
		relocated, err = operations.Relocate(page.ctx, application.WorkspaceRelocateRequest{ID: page.targetID, Path: page.value})
		if err == nil {
			page.targetID = relocated.Value.After.ID
		}
	case WorkspaceAccessAdd:
		_, err = operations.AddAllowDir(page.ctx, page.targetID, page.value)
	case WorkspaceAccessRemove:
		_, err = operations.RemoveAllowDir(page.ctx, page.targetID, page.value)
	case WorkspaceContainerCreate:
		_, err = operations.CreateContainer(page.ctx, page.value)
	case WorkspaceContainerRename:
		_, err = operations.RenameContainer(page.ctx, page.targetID, page.value)
	case WorkspaceContainerMembers:
		return page.updateMembers()
	default:
		err = fmt.Errorf("unsupported workspace editor action: %s", page.command)
	}
	if err != nil {
		return err
	}
	return nil
}

func (page *WorkspacePage) workspaceEditorNavigation(command WorkspaceCommand, resourceID string) tea.Cmd {
	path := []string{}
	switch command {
	case WorkspaceRegister:
		path = []string{"workspaces", "register"}
	case WorkspaceRelocate:
		path = []string{"workspaces", resourceID, "relocate"}
	case WorkspaceAccessAdd:
		path = []string{"workspaces", resourceID, "access", "add"}
	case WorkspaceAccessRemove:
		path = []string{"workspaces", resourceID, "access", "remove"}
	case WorkspaceContainerCreate:
		path = []string{"containers", "create"}
	case WorkspaceContainerRename:
		path = []string{"containers", resourceID, "edit"}
	case WorkspaceContainerMembers:
		path = []string{"containers", resourceID, "workspaces", "edit"}
	default:
		return nil
	}
	return func() tea.Msg { return NavigateMsg{Path: path} }
}

func (page *WorkspacePage) workspaceEditorParentNavigation() tea.Cmd {
	if page == nil {
		return nil
	}
	path := []string{"workspaces"}
	switch page.command {
	case WorkspaceRelocate:
		path = []string{"workspaces", page.targetID}
	case WorkspaceAccessAdd, WorkspaceAccessRemove:
		path = []string{"workspaces", page.targetID, "access"}
	case WorkspaceContainerCreate:
		path = []string{"containers"}
	case WorkspaceContainerRename:
		path = []string{"containers", page.targetID}
	case WorkspaceContainerMembers:
		path = []string{"containers", page.targetID, "workspaces"}
	}
	return func() tea.Msg { return NavigateMsg{Path: path, Replace: true} }
}

func (page *WorkspacePage) workspaceEditorView(width, height int) string {
	if page == nil || page.editor == nil {
		return ""
	}
	page.width, page.height = width, height
	page.resizeWorkspaceEditor()
	return page.editor.View()
}

func (page *WorkspacePage) resizeWorkspaceEditor() {
	if page == nil || page.editor == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	page.editor.Resize(page.width, page.height)
}

func (page *WorkspacePage) workspaceEditorMouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil || page.editor == nil {
		return nil
	}
	return page.editor.MouseTargets(originX, originY, z)
}
