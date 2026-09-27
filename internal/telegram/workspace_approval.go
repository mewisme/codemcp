package telegram

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/capability"
)

const domainPageSize = 3

const (
	inputWorkspaceRegister        = "workspace.register.path"
	inputWorkspaceRelocate        = "workspace.relocate.path"
	inputWorkspaceAccessAdd       = "workspace.access.add.path"
	inputWorkspaceContainerCreate = "workspace.container.create.name"
	inputWorkspaceContainerRename = "workspace.container.rename.name"
)

func (ui *Interface) dispatch(ctx context.Context, operation capability.ID, input any) (any, error) {
	if ui == nil || ui.dispatcher == nil {
		return nil, errors.New("canonical operation dispatcher is unavailable")
	}
	result, err := ui.dispatcher.Dispatch(application.WithOperationInterface(ctx, application.OperationInterfaceTelegram), application.DispatchRequest{Operation: operation, Input: input})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (ui *Interface) handleWorkspaces(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	state := ActionState{Route: RouteWorkspaces, Back: RouteHome, Operation: capability.WorkspaceList}
	screen, err := ui.workspaceListScreen(ctx, owner, state)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleRequests(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	state := ActionState{Route: RouteRequests, Back: RouteHome, Operation: capability.RequestList}
	screen, err := ui.requestListScreen(ctx, owner, state)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_, _ = ui.runtime.SendRichMessageToTopic(ctx, owner.ChatID, TopicRequests, screen, RichMessageOptions{})
}

func (ui *Interface) workspaceListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.WorkspaceList, nil)
	if err != nil {
		return Screen{}, err
	}
	items, ok := value.([]application.WorkspaceView)
	if !ok {
		return Screen{}, errors.New("workspace list returned an unexpected result")
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Path) < strings.ToLower(items[j].Path) })
	start, end, page, pages := PageBounds(len(items), state.Page, domainPageSize)
	buttons := make([]Button, 0, end-start)
	list := make([]string, 0, end-start)
	for _, item := range items[start:end] {
		label := item.ID
		if label == "" {
			label = item.Path
		}
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(label), CallbackOpen, ActionState{
			Route: RouteWorkspace, Back: RouteWorkspaces, Operation: capability.WorkspaceShow,
			ResourceID: item.ID, Input: application.WorkspaceIDInput{ID: item.ID},
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = ButtonRoleResource
		buttons = append(buttons, button)
		availability := "available"
		if !item.Available {
			availability = "unavailable"
		}
		list = append(list, fmt.Sprintf("%s — %s — %s", item.ID, item.Path, availability))
	}
	register, err := ui.stateButton(owner, "Register", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteWorkspaces, Operation: capability.WorkspaceRegister, InputKind: inputWorkspaceRegister})
	if err != nil {
		return Screen{}, err
	}
	register.Role = ButtonRolePositive
	containers, err := ui.stateButton(owner, "Containers", CallbackOpen, ActionState{Route: RouteContainers, Back: RouteWorkspaces, Operation: capability.WorkspaceContainerList})
	if err != nil {
		return Screen{}, err
	}
	navigation, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Workspaces", Text: fmt.Sprintf("%d registered · page %d/%d", len(items), page+1, max(1, pages))},
		RichBlock{Kind: RichList, Items: list},
	)
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{register, containers}, Secondary: buttons, Navigation: navigation})}, nil
}

func (ui *Interface) workspaceDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.TrimSpace(state.ResourceID)
	if id == "" {
		if input, ok := state.Input.(application.WorkspaceIDInput); ok {
			id = strings.TrimSpace(input.ID)
		}
	}
	value, err := ui.dispatch(ctx, capability.WorkspaceShow, application.WorkspaceIDInput{ID: id})
	if err != nil {
		return Screen{}, err
	}
	item, ok := value.(application.WorkspaceView)
	if !ok {
		return Screen{}, errors.New("workspace view returned an unexpected result")
	}
	relocate, err := ui.stateButton(owner, "Relocate", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteWorkspaces, Operation: capability.WorkspaceRelocate, ResourceID: item.ID, InputKind: inputWorkspaceRelocate})
	if err != nil {
		return Screen{}, err
	}
	access, err := ui.stateButton(owner, "Access", CallbackOpen, ActionState{Route: RouteAccess, Back: RouteWorkspaces, Operation: capability.WorkspaceAccessList, ResourceID: item.ID, Input: application.WorkspaceIDInput{ID: item.ID}})
	if err != nil {
		return Screen{}, err
	}
	containers, err := ui.stateButton(owner, "Containers", CallbackOpen, ActionState{Route: RouteContainers, Back: RouteWorkspaces, Operation: capability.WorkspaceContainerMembershipList, ResourceID: item.ID})
	if err != nil {
		return Screen{}, err
	}
	unregister, err := ui.stateButton(owner, "Unregister", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteWorkspaces, Operation: capability.WorkspaceUnregister, ResourceID: item.ID, Input: application.WorkspaceIDInput{ID: item.ID}})
	if err != nil {
		return Screen{}, err
	}
	unregister.Role = ButtonRoleDestructive
	purge, err := ui.stateButton(owner, "Purge", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteWorkspaces, Operation: capability.WorkspacePurge, ResourceID: item.ID, Input: application.WorkspacePurgeInput{Target: item.ID, Confirm: true}})
	if err != nil {
		return Screen{}, err
	}
	purge.Role = ButtonRoleDestructive
	back, err := ui.backButton(owner, RouteWorkspaces)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	secondary := []Button{access, containers}
	if copyID, ok := CopyValueButton("Copy ID", item.ID); ok {
		secondary = append(secondary, copyID)
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: item.ID, Text: "Registered workspace"},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Path", item.Path}, {"Available", fmt.Sprint(item.Available)}, {"Allowed roots", fmt.Sprint(len(item.AllowDirs))}}},
	)
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{relocate}, Secondary: secondary, Destructive: []Button{unregister, purge}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) workspaceAccessScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.WorkspaceAccessList, application.WorkspaceIDInput{ID: id})
	if err != nil {
		return Screen{}, err
	}
	paths, ok := value.([]string)
	if !ok {
		return Screen{}, errors.New("workspace access list returned an unexpected result")
	}
	sort.Strings(paths)
	start, end, page, pages := PageBounds(len(paths), state.Page, domainPageSize)
	removeButtons := make([]Button, 0, end-start)
	list := make([]string, 0, end-start)
	for _, path := range paths[start:end] {
		button, buttonErr := ui.stateButton(owner, "Remove "+CompactResourceLabel(path), CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteWorkspaces, Operation: capability.WorkspaceAccessRemove,
			ResourceID: id, Input: application.WorkspaceAccessInput{ID: id, Path: path},
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = ButtonRoleDestructive
		removeButtons = append(removeButtons, button)
		list = append(list, path)
	}
	add, err := ui.stateButton(owner, "Add root", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteWorkspaces, Operation: capability.WorkspaceAccessAdd, ResourceID: id, InputKind: inputWorkspaceAccessAdd})
	if err != nil {
		return Screen{}, err
	}
	add.Role = ButtonRolePositive
	navigation, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Workspace access", Text: id}, RichBlock{Kind: RichList, Items: list})
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{add}, Destructive: removeButtons, Navigation: navigation})}, nil
}

func (ui *Interface) containerListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	operation := capability.WorkspaceContainerList
	var input any
	title := "Containers"
	if strings.TrimSpace(state.ResourceID) != "" {
		operation = capability.WorkspaceContainerMembershipList
		input = application.WorkspaceMembershipQueryInput{WorkspaceID: state.ResourceID}
		title = "Workspace containers"
	}
	value, err := ui.dispatch(ctx, operation, input)
	if err != nil {
		return Screen{}, err
	}
	items, ok := value.([]application.WorkspaceContainerView)
	if !ok {
		return Screen{}, errors.New("workspace container list returned an unexpected result")
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	start, end, page, pages := PageBounds(len(items), state.Page, domainPageSize)
	buttons := make([]Button, 0, end-start)
	list := make([]string, 0, end-start)
	for _, item := range items[start:end] {
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(item.Name), CallbackOpen, ActionState{Route: RouteContainer, Back: RouteContainers, Operation: capability.WorkspaceContainerShow, ResourceID: item.ID, Input: application.WorkspaceIDInput{ID: item.ID}})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = ButtonRoleResource
		buttons = append(buttons, button)
		list = append(list, fmt.Sprintf("%s — %d workspaces", item.Name, len(item.WorkspaceIDs)))
	}
	primary := []Button{}
	if state.ResourceID == "" {
		create, createErr := ui.stateButton(owner, "Create", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteContainers, Operation: capability.WorkspaceContainerCreate, InputKind: inputWorkspaceContainerCreate})
		if createErr != nil {
			return Screen{}, createErr
		}
		create.Role = ButtonRolePositive
		primary = append(primary, create)
	}
	navigation, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(RichBlock{Kind: RichHeading, Title: title, Text: fmt.Sprintf("%d items", len(items))}, RichBlock{Kind: RichList, Items: list})
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Primary: primary, Secondary: buttons, Navigation: navigation})}, nil
}

func (ui *Interface) containerDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.WorkspaceContainerShow, application.WorkspaceIDInput{ID: id})
	if err != nil {
		return Screen{}, err
	}
	item, ok := value.(application.WorkspaceContainerView)
	if !ok {
		return Screen{}, errors.New("workspace container view returned an unexpected result")
	}
	if state.Detail {
		return ui.containerMembersScreen(ctx, owner, state, item)
	}
	rename, err := ui.stateButton(owner, "Rename", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteContainers, Operation: capability.WorkspaceContainerRename, ResourceID: id, InputKind: inputWorkspaceContainerRename})
	if err != nil {
		return Screen{}, err
	}
	members, err := ui.stateButton(owner, "Members", CallbackOpen, ActionState{Route: RouteContainer, Back: RouteContainers, Operation: capability.WorkspaceContainerMembershipList, ResourceID: id, Detail: true})
	if err != nil {
		return Screen{}, err
	}
	remove, err := ui.stateButton(owner, "Delete", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteContainers, Operation: capability.WorkspaceContainerDelete, ResourceID: id, Input: application.WorkspaceIDInput{ID: id}})
	if err != nil {
		return Screen{}, err
	}
	remove.Role = ButtonRoleDestructive
	back, err := ui.backButton(owner, RouteContainers)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(RichBlock{Kind: RichHeading, Title: item.Name, Text: "Workspace container"}, RichBlock{Kind: RichTable, Rows: [][]string{{"ID", item.ID}, {"Members", fmt.Sprint(len(item.WorkspaceIDs))}}})
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{rename}, Secondary: []Button{members}, Destructive: []Button{remove}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) containerMembersScreen(ctx context.Context, owner ViewOwner, state ActionState, container application.WorkspaceContainerView) (Screen, error) {
	membersValue, err := ui.dispatch(ctx, capability.WorkspaceContainerMembershipList, application.WorkspaceMembershipQueryInput{ContainerID: container.ID})
	if err != nil {
		return Screen{}, err
	}
	members, ok := membersValue.([]application.WorkspaceView)
	if !ok {
		return Screen{}, errors.New("container membership list returned an unexpected result")
	}
	allValue, err := ui.dispatch(ctx, capability.WorkspaceList, nil)
	if err != nil {
		return Screen{}, err
	}
	all, ok := allValue.([]application.WorkspaceView)
	if !ok {
		return Screen{}, errors.New("workspace list returned an unexpected result")
	}
	memberSet := map[string]bool{}
	for _, item := range members {
		memberSet[item.ID] = true
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	start, end, page, pages := PageBounds(len(all), state.Page, domainPageSize)
	buttons := make([]Button, 0, end-start)
	list := make([]string, 0, end-start)
	for _, workspace := range all[start:end] {
		member := memberSet[workspace.ID]
		op := capability.WorkspaceContainerAdd
		label := "Add " + CompactResourceLabel(workspace.ID)
		role := ButtonRolePositive
		if member {
			op = capability.WorkspaceContainerRemove
			label = "Remove " + CompactResourceLabel(workspace.ID)
			role = ButtonRoleDestructive
		}
		button, buttonErr := ui.stateButton(owner, label, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteContainers, Operation: op, ResourceID: container.ID, Input: application.WorkspaceContainerMembershipInput{ContainerID: container.ID, WorkspaceIDs: []string{workspace.ID}}})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = role
		buttons = append(buttons, button)
		status := "not a member"
		if member {
			status = "member"
		}
		list = append(list, workspace.ID+" — "+status)
	}
	navigation, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(RichBlock{Kind: RichHeading, Title: container.Name + " members", Text: fmt.Sprintf("%d current members", len(members))}, RichBlock{Kind: RichList, Items: list})
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Secondary: buttons, Navigation: navigation})}, nil
}

func (ui *Interface) requestListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.RequestList, nil)
	if err != nil {
		return Screen{}, err
	}
	all, ok := value.([]approval.Request)
	if !ok {
		return Screen{}, errors.New("approval request list returned an unexpected result")
	}
	pending := make([]approval.Request, 0, len(all))
	for _, request := range all {
		if request.Status == approval.StatusPending {
			pending = append(pending, request)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.After(pending[j].CreatedAt) })
	start, end, page, pages := PageBounds(len(pending), state.Page, domainPageSize)
	buttons := make([]Button, 0, end-start)
	list := make([]string, 0, end-start)
	for _, request := range pending[start:end] {
		label := request.Title
		if strings.TrimSpace(label) == "" {
			label = request.TargetTool
		}
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(label), CallbackOpen, ActionState{Route: RouteRequest, Back: RouteRequests, Operation: capability.RequestView, ResourceID: request.ID, Input: application.RequestIDInput{ID: request.ID}})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = ButtonRoleResource
		buttons = append(buttons, button)
		list = append(list, fmt.Sprintf("%s — %s — %s", request.ID, request.TargetTool, request.WorkspaceID))
	}
	navigation, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Approval requests", Text: fmt.Sprintf("%d pending", len(pending))}, RichBlock{Kind: RichList, Items: list})
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Secondary: buttons, Navigation: navigation})}, nil
}

func (ui *Interface) requestDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.RequestView, application.RequestIDInput{ID: id})
	if err != nil {
		return Screen{}, err
	}
	request, ok := value.(approval.Request)
	if !ok {
		return Screen{}, errors.New("approval request view returned an unexpected result")
	}
	return ui.requestCard(owner, request)
}

func (ui *Interface) requestCard(owner ViewOwner, request approval.Request) (Screen, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = request.TargetTool
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: title, Text: "Approval request · " + string(request.Status)},
		{Kind: RichTable, Rows: [][]string{{"ID", request.ID}, {"Workspace", request.WorkspaceID}, {"Tool", request.TargetTool}, {"Guard", string(request.GuardCode)}, {"Expires", request.ExpiresAt.UTC().Format("2006-01-02 15:04:05Z")}}},
	}
	if strings.TrimSpace(request.GuardReason) != "" {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Policy reason", Text: request.GuardReason})
	}
	if strings.TrimSpace(request.Command) != "" {
		blocks = append(blocks, RichBlock{Kind: RichCode, Title: "Command", Text: request.Command})
	}
	primary, secondary, destructive := []Button{}, []Button{}, []Button{}
	if request.Status == approval.StatusPending {
		approve, err := ui.stateButton(owner, "Approve once", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteRequests, Operation: capability.RequestApprove, ResourceID: request.ID, Input: application.RequestResolutionInput{ID: request.ID}})
		if err != nil {
			return Screen{}, err
		}
		approve.Role = ButtonRolePositive
		primary = append(primary, approve)
		deny, err := ui.stateButton(owner, "Deny", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteRequests, Operation: capability.RequestDeny, ResourceID: request.ID, Input: application.RequestResolutionInput{ID: request.ID}})
		if err != nil {
			return Screen{}, err
		}
		deny.Role = ButtonRoleDestructive
		destructive = append(destructive, deny)
		if strings.TrimSpace(request.SimilarCommandPattern) != "" {
			allow, err := ui.stateButton(owner, "Allow similar", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteRequests, Operation: capability.RequestApprove, ResourceID: request.ID, Input: application.RequestResolutionInput{ID: request.ID, AllowSimilar: true}})
			if err != nil {
				return Screen{}, err
			}
			allow.Role = ButtonRolePositive
			secondary = append(secondary, allow)
		}
	}
	if copyID, ok := CopyValueButton("Copy ID", request.ID); ok {
		secondary = append(secondary, copyID)
	}
	back, err := ui.backButton(owner, RouteRequests)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Primary: primary, Secondary: secondary, Destructive: destructive, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) domainOperationResultScreen(owner ViewOwner, state ActionState, spec capability.Spec, value any) (Screen, bool, error) {
	switch result := value.(type) {
	case approval.Request:
		screen, err := ui.requestCard(owner, result)
		return screen, true, err
	case application.WorkspaceView:
		keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, value)
		if err != nil {
			return Screen{}, true, err
		}
		rich := BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: result.ID, Text: "Workspace operation completed"},
			RichBlock{Kind: RichTable, Rows: [][]string{{"Path", result.Path}, {"Available", fmt.Sprint(result.Available)}, {"Allowed roots", fmt.Sprint(len(result.AllowDirs))}}},
		)
		return Screen{Rich: rich, Keyboard: keyboard}, true, nil
	case application.WorkspaceRelocation:
		keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, value)
		if err != nil {
			return Screen{}, true, err
		}
		rich := BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: result.After.ID, Text: "Workspace relocated"},
			RichBlock{Kind: RichTable, Rows: [][]string{{"Previous root", result.Before.Path}, {"Current root", result.After.Path}, {"Available", fmt.Sprint(result.After.Available)}}},
		)
		return Screen{Rich: rich, Keyboard: keyboard}, true, nil
	case application.WorkspaceContainerView:
		keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, value)
		if err != nil {
			return Screen{}, true, err
		}
		rich := BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: result.Name, Text: "Workspace container operation completed"},
			RichBlock{Kind: RichTable, Rows: [][]string{{"ID", result.ID}, {"Members", fmt.Sprint(len(result.WorkspaceIDs))}}},
		)
		return Screen{Rich: rich, Keyboard: keyboard}, true, nil
	default:
		return Screen{}, false, nil
	}
}

func (ui *Interface) beginActionInput(ctx context.Context, owner ViewOwner, state ActionState) error {
	if ui == nil || ui.inputs == nil || ui.runtime == nil {
		return errors.New("telegram input workflow is unavailable")
	}
	title, prompt, placeholder := inputPrompt(state.InputKind)
	if title == "" {
		return errors.New("telegram action input is unsupported")
	}
	screen, err := ui.inputPromptScreen(owner, state, title, prompt)
	if err != nil {
		return err
	}
	promptID, err := ui.runtime.SendRichMessage(ctx, owner.ChatID, screen, RichMessageOptions{ForceReplyPlaceholder: placeholder})
	if err != nil {
		return err
	}
	if err := ui.inputs.PutAction(owner, promptID, false, state); err != nil {
		_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, promptID)
		return err
	}
	return nil
}

func (ui *Interface) handleActionInput(ctx context.Context, update Update) bool {
	if ui == nil || ui.runtime == nil || ui.inputs == nil || update.Message == nil {
		return false
	}
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return false
	}
	pending, ok := ui.inputs.Match(owner, *update.Message)
	if !ok {
		return false
	}
	if pending.Action == nil {
		return true
	}
	value, err := InputValue(*update.Message)
	if err != nil || value.Document != nil {
		if err == nil {
			err = errors.New("this operation requires text input")
		}
		_ = ui.editInputFailure(ctx, owner, pending.PromptMessageID, *pending.Action, err)
		return true
	}
	state := *pending.Action
	state.Input, err = actionInput(state, value.Text)
	if err != nil {
		_ = ui.editInputFailure(ctx, owner, pending.PromptMessageID, state, err)
		return true
	}
	state.InputKind = ""
	screen, err := ui.operationScreen(ctx, owner, state)
	if err != nil {
		screen, _ = ui.operationErrorScreen(owner, state, err)
	}
	_ = ui.completeInput(ctx, owner, pending.PromptMessageID, update.Message.MessageID, false, screen)
	return true
}

func inputPrompt(kind string) (title, prompt, placeholder string) {
	switch kind {
	case inputWorkspaceRegister:
		return "Register workspace", "Reply with the absolute workspace directory.", "/path/to/workspace"
	case inputWorkspaceRelocate:
		return "Relocate workspace", "Reply with the new absolute workspace directory.", "/new/path"
	case inputWorkspaceAccessAdd:
		return "Add workspace root", "Reply with the absolute directory to allow.", "/allowed/path"
	case inputWorkspaceContainerCreate:
		return "Create container", "Reply with the new container name.", "Container name"
	case inputWorkspaceContainerRename:
		return "Rename container", "Reply with the new container name.", "Container name"
	default:
		return "", "", ""
	}
}

func actionInput(state ActionState, text string) (any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("input must not be empty")
	}
	switch state.InputKind {
	case inputWorkspaceRegister:
		return application.WorkspaceRegisterInput{Path: text}, nil
	case inputWorkspaceRelocate:
		return application.WorkspaceRelocateRequest{ID: state.ResourceID, Path: text}, nil
	case inputWorkspaceAccessAdd:
		return application.WorkspaceAccessInput{ID: state.ResourceID, Path: text}, nil
	case inputWorkspaceContainerCreate:
		return application.WorkspaceContainerInput{Name: text}, nil
	case inputWorkspaceContainerRename:
		return application.WorkspaceContainerInput{ID: state.ResourceID, Name: text}, nil
	default:
		return nil, errors.New("unsupported action input")
	}
}

func (ui *Interface) domainPaginationButtons(owner ViewOwner, state ActionState, page, pages int) ([]Button, error) {
	buttons := make([]Button, 0, 3)
	if page > 0 {
		previous := state
		previous.Page = page - 1
		button, err := ui.stateButton(owner, "Newer", CallbackOpen, previous)
		if err != nil {
			return nil, err
		}
		button.Role = ButtonRoleNavigation
		buttons = append(buttons, button)
	}
	if page+1 < pages {
		next := state
		next.Page = page + 1
		button, err := ui.stateButton(owner, "Older", CallbackOpen, next)
		if err != nil {
			return nil, err
		}
		button.Role = ButtonRoleNavigation
		buttons = append(buttons, button)
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return nil, err
	}
	buttons = append(buttons, home)
	return buttons, nil
}

func (ui *Interface) workspaceRelocationConflictScreen(owner ViewOwner, state ActionState, err error) (Screen, bool) {
	conflict, ok := application.WorkspaceRelocationConflictOf(err)
	if !ok {
		return Screen{}, false
	}
	resolutions := make([]string, 0, len(conflict.Resolutions))
	for _, resolution := range conflict.Resolutions {
		resolutions = append(resolutions, string(resolution))
	}
	back, backErr := ui.backButton(owner, RouteWorkspaces)
	if backErr != nil {
		return ErrorScreen(backErr), true
	}
	home, homeErr := ui.homeButton(owner)
	if homeErr != nil {
		return ErrorScreen(homeErr), true
	}
	secondary := []Button{}
	if copyID, copied := CopyValueButton("Copy ID", conflict.WorkspaceID); copied {
		secondary = append(secondary, copyID)
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Duplicate workspace identity", Text: conflict.WorkspaceID},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Registered root", conflict.RegisteredRoot}, {"Destination root", conflict.DestinationRoot}}},
		RichBlock{Kind: RichList, Title: "Local resolution choices", Items: resolutions},
		RichBlock{Kind: RichDetails, Title: "Local action required", Text: "Resolve this conflict locally with cm workspace relocate. Telegram does not choose destination, registered, or merge state on your behalf."},
	)
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Secondary: secondary, Navigation: []Button{back, home}})}, true
}
