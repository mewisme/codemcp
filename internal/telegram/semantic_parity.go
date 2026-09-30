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
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tunnel"
)

func (ui *Interface) grantListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.RequestGrantList, application.RuntimeGrantInput{})
	if err != nil {
		return Screen{}, err
	}
	grants, ok := value.([]approval.Request)
	if !ok {
		return Screen{}, errors.New("runtime grant list returned an unexpected result")
	}
	sort.SliceStable(grants, func(i, j int) bool { return grants[i].GrantExpiresAt.After(grants[j].GrantExpiresAt) })
	start, end, page, pages := PageBounds(len(grants), state.Page, domainPageSize)
	items := make([]string, 0, end-start)
	buttons := make([]Button, 0, end-start)
	for _, grant := range grants[start:end] {
		label := strings.TrimSpace(grant.Title)
		if label == "" {
			label = grant.TargetTool
		}
		items = append(items, fmt.Sprintf("%s — %s — %s", grant.ID, grant.WorkspaceID, grant.GrantExpiresAt.UTC().Format("2006-01-02 15:04Z")))
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(label), CallbackOpen, ActionState{Route: RouteGrant, Back: RouteGrants, ResourceID: grant.ID})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		buttons = append(buttons, button)
	}
	nav, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Runtime grants", Text: fmt.Sprintf("%d active session grant(s)", len(grants))},
		RichBlock{Kind: RichList, Items: items},
	), Keyboard: BoundedActionGroups(ActionGroups{Secondary: buttons, Navigation: nav})}, nil
}

func (ui *Interface) grantDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.RequestGrantList, application.RuntimeGrantInput{})
	if err != nil {
		return Screen{}, err
	}
	grants, ok := value.([]approval.Request)
	if !ok {
		return Screen{}, errors.New("runtime grant list returned an unexpected result")
	}
	var grant *approval.Request
	for index := range grants {
		if grants[index].ID == strings.TrimSpace(state.ResourceID) {
			grant = &grants[index]
			break
		}
	}
	if grant == nil {
		return Screen{}, errors.New("runtime grant is no longer active")
	}
	revoke, err := ui.stateButton(owner, "Revoke grant", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteGrants, Operation: capability.RequestGrantRevoke,
		ResourceID: grant.ID, Input: application.RuntimeGrantInput{ID: grant.ID}, ForceConfirm: true,
	})
	if err != nil {
		return Screen{}, err
	}
	revoke.Role = ButtonRoleDestructive
	back, _ := ui.backButton(owner, RouteGrants)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Runtime grant", Text: string(grant.Status)},
		RichBlock{Kind: RichCopy, Title: "Request ID", Text: grant.ID, CopyText: grant.ID},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Workspace", grant.WorkspaceID}, {"Tool", grant.TargetTool}, {"Expires", grant.GrantExpiresAt.UTC().Format("2006-01-02 15:04:05Z")}}},
		RichBlock{Kind: RichDetails, Title: "Scope", Text: "Revocation is handled by the canonical runtime grant authority; Telegram does not maintain a separate grant store."},
	), Keyboard: BoundedActionGroups(ActionGroups{Destructive: []Button{revoke}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) processListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	workspaceID := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.ProcessList, application.ProcessInput{WorkspaceID: workspaceID})
	if err != nil {
		return Screen{}, err
	}
	processes, ok := value.([]shellruntime.ProcessInfo)
	if !ok {
		return Screen{}, errors.New("process list returned an unexpected result")
	}
	start, end, page, pages := PageBounds(len(processes), state.Page, domainPageSize)
	items := make([]string, 0, end-start)
	buttons := make([]Button, 0, end-start)
	for _, process := range processes[start:end] {
		status := "finished"
		if process.Running {
			status = "running"
		}
		items = append(items, fmt.Sprintf("%s — %s — %s", process.ID, status, compactPresentationValue(process.Command)))
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(process.ID), CallbackOpen, ActionState{
			Route: RouteProcess, Back: RouteProcesses, ResourceID: process.ID, ParentID: workspaceID,
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		buttons = append(buttons, button)
	}
	navState := state
	navState.ResourceID = workspaceID
	nav, err := ui.domainPaginationButtons(owner, navState, page, pages)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Background processes", Text: fmt.Sprintf("%d process(es)", len(processes))},
		RichBlock{Kind: RichCopy, Title: "Workspace", Text: workspaceID, CopyText: workspaceID},
		RichBlock{Kind: RichList, Items: items},
	), Keyboard: BoundedActionGroups(ActionGroups{Secondary: buttons, Navigation: nav})}, nil
}

func (ui *Interface) processDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	workspaceID := strings.TrimSpace(state.ParentID)
	id := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.ProcessView, application.ProcessInput{WorkspaceID: workspaceID, ID: id})
	if err != nil {
		return Screen{}, err
	}
	process, ok := value.(shellruntime.ProcessInfo)
	if !ok {
		return Screen{}, errors.New("process view returned an unexpected result")
	}
	destructive := []Button{}
	if !process.Running {
		clear, buttonErr := ui.stateButton(owner, "Clear finished process", CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteProcesses, Operation: capability.ProcessClear,
			ResourceID: id, ParentID: workspaceID, Input: application.ProcessInput{WorkspaceID: workspaceID, ID: id}, ForceConfirm: true,
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		clear.Role = ButtonRoleDestructive
		destructive = append(destructive, clear)
	}
	back, _ := ui.stateButton(owner, "Back", CallbackBack, ActionState{Route: RouteProcesses, ResourceID: workspaceID})
	home, _ := ui.homeButton(owner)
	rows := [][]string{{"PID", fmt.Sprint(process.PID)}, {"Running", fmt.Sprint(process.Running)}, {"Started", process.StartedAt}, {"CWD", process.CWD}}
	if process.ExitCode != nil {
		rows = append(rows, []string{"Exit code", fmt.Sprint(*process.ExitCode)})
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Process", Text: process.ID},
		RichBlock{Kind: RichCode, Title: "Command", Text: process.Command},
		RichBlock{Kind: RichTable, Rows: rows},
	), Keyboard: BoundedActionGroups(ActionGroups{Destructive: destructive, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) codeGraphWorkspaceScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	workspaceID := strings.TrimSpace(state.ResourceID)
	input := application.CodeGraphWorkspaceInput{WorkspaceID: workspaceID}
	value, err := ui.dispatch(ctx, capability.IntegrationCodeGraphWorkspaceStatus, input)
	if err != nil {
		return Screen{}, err
	}
	initButton, err := ui.stateButton(owner, "Initialize", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteCodeGraphWS, Operation: capability.IntegrationCodeGraphWorkspaceInit,
		ResourceID: workspaceID, Input: input, ForceConfirm: true,
	})
	if err != nil {
		return Screen{}, err
	}
	syncButton, err := ui.stateButton(owner, "Sync", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteCodeGraphWS, Operation: capability.IntegrationCodeGraphWorkspaceSync,
		ResourceID: workspaceID, Input: input,
	})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.stateButton(owner, "Back", CallbackBack, ActionState{Route: RouteWorkspace, ResourceID: workspaceID})
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "CodeGraph workspace", Text: workspaceID},
		RichBlock{Kind: RichDetails, Title: "Status", Text: compactAny(value)},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{syncButton}, Secondary: []Button{initButton}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) managedTunnelListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.TunnelList, nil)
	if err != nil {
		return Screen{}, err
	}
	items, ok := value.([]tunnel.Metadata)
	if !ok {
		return Screen{}, errors.New("managed tunnel list returned an unexpected result")
	}
	start, end, page, pages := PageBounds(len(items), state.Page, domainPageSize)
	list := make([]string, 0, end-start)
	buttons := make([]Button, 0, end-start)
	for _, item := range items[start:end] {
		list = append(list, fmt.Sprintf("%s — %s", item.Name, item.ID))
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(item.Name), CallbackOpen, ActionState{Route: RouteManagedTunnel, Back: RouteManagedTunnels, ResourceID: item.ID})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		buttons = append(buttons, button)
	}
	create, err := ui.stateButton(owner, "Create", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteManagedTunnels, Operation: capability.TunnelCreate, InputKind: inputManagedTunnelCreate})
	if err != nil {
		return Screen{}, err
	}
	create.Role = ButtonRolePositive
	adminStatus, err := ui.stateButton(owner, "Admin key status", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteManagedTunnels, Operation: capability.TunnelAdminKeyStatus})
	if err != nil {
		return Screen{}, err
	}
	configRead, err := ui.stateButton(owner, "Tunnel config", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteManagedTunnels, Operation: capability.TunnelConfigRead})
	if err != nil {
		return Screen{}, err
	}
	nav, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Managed tunnels", Text: fmt.Sprintf("%d tunnel(s)", len(items))},
		RichBlock{Kind: RichList, Items: list},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{create, adminStatus, configRead}, Secondary: buttons, Navigation: nav})}, nil
}

func (ui *Interface) managedTunnelDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.TunnelGet, application.ManagedTunnelGetInput{ID: id})
	if err != nil {
		return Screen{}, err
	}
	result, ok := value.(application.ManagedTunnelResult)
	if !ok {
		return Screen{}, errors.New("managed tunnel view returned an unexpected result")
	}
	metadata := result.Metadata
	use, err := ui.stateButton(owner, "Use locally", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteManagedTunnels, Operation: capability.TunnelUse,
		ResourceID: id, Input: application.ManagedTunnelUseInput{ID: id, AutoGenerateRuntimeKey: true}, ForceConfirm: true,
	})
	if err != nil {
		return Screen{}, err
	}
	update, err := ui.stateButton(owner, "Update", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteManagedTunnels, Operation: capability.TunnelUpdate, ResourceID: id, InputKind: inputManagedTunnelUpdate})
	if err != nil {
		return Screen{}, err
	}
	remove, err := ui.stateButton(owner, "Delete", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteManagedTunnels, Operation: capability.TunnelDelete,
		ResourceID: id, Input: application.ManagedTunnelDeleteInput{ID: id}, ForceConfirm: true,
	})
	if err != nil {
		return Screen{}, err
	}
	remove.Role = ButtonRoleDestructive
	back, _ := ui.backButton(owner, RouteManagedTunnels)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: metadata.Name, Text: "Managed OpenAI tunnel"},
		RichBlock{Kind: RichCopy, Title: "ID", Text: metadata.ID, CopyText: metadata.ID},
		RichBlock{Kind: RichDetails, Title: "Description", Text: metadata.Description},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Organizations", strings.Join(metadata.OrganizationIDs, ", ")}, {"Workspaces", strings.Join(metadata.WorkspaceIDs, ", ")}, {"Tenants", strings.Join(metadata.TenantIDs, ", ")}}},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{use}, Secondary: []Button{update}, Destructive: []Button{remove}, Navigation: []Button{back, home}})}, nil
}
