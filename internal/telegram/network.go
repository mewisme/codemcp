package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

const (
	inputUpstreamAdd         = "upstream.add"
	inputUpstreamConfigure   = "upstream.configure"
	inputTunnelConfigure     = "tunnel.configure"
	inputTunnelAdminKey      = "tunnel.admin.key"
	inputTunnelAdminScope    = "tunnel.admin.scope"
	inputUpstreamOAuthLogin  = "upstream.oauth.login"
	inputManagedTunnelCreate = "tunnel.managed.create"
	inputManagedTunnelUpdate = "tunnel.managed.update"
)

func (ui *Interface) handleNetwork(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteNetwork, Back: RouteHome})
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) networkScreen(owner ViewOwner) (Screen, error) {
	tunnelButton, err := ui.stateButton(owner, "Secure MCP Tunnel", CallbackOpen, ActionState{Route: RouteTunnel, Back: RouteNetwork, Operation: capability.TunnelStatus})
	if err != nil {
		return Screen{}, err
	}
	upstreamButton, err := ui.stateButton(owner, "Upstreams", CallbackOpen, ActionState{Route: RouteUpstreams, Back: RouteNetwork, Operation: capability.UpstreamServerList})
	if err != nil {
		return Screen{}, err
	}
	interfaces, err := ui.stateButton(owner, "Network interfaces", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteNetwork, Operation: capability.NetworkInterfacesList})
	if err != nil {
		return Screen{}, err
	}
	back, err := ui.backButton(owner, RouteHome)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Network", Text: "Tunnels, upstream servers, and network interfaces"},
		RichBlock{Kind: RichDetails, Title: "OpenAI connectivity", Text: "Secure MCP Tunnel is a single configured OpenAI-profile connection, not a collection of local tunnel instances."},
	)
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{tunnelButton, upstreamButton}, Secondary: []Button{interfaces}, Navigation: []Button{back}})}, nil
}

func (ui *Interface) tunnelScreen(ctx context.Context, owner ViewOwner) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.TunnelStatus, nil)
	if err != nil {
		return Screen{}, err
	}
	view, ok := value.(application.TunnelView)
	if !ok {
		return Screen{}, errors.New("tunnel status returned an unexpected result")
	}
	blocks := tunnelViewBlocks(view)
	buttons := []Button{}
	enableOperation, enableLabel := capability.TunnelEnable, "Enable"
	if view.Enabled {
		enableOperation, enableLabel = capability.TunnelDisable, "Disable"
	}
	toggle, err := ui.stateButton(owner, enableLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: enableOperation})
	if err != nil {
		return Screen{}, err
	}
	if view.Enabled {
		toggle.Role = ButtonRoleDestructive
	} else {
		toggle.Role = ButtonRolePositive
	}
	buttons = append(buttons, toggle)
	syncButton, err := ui.stateButton(owner, "Sync metadata", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: capability.TunnelSync})
	if err != nil {
		return Screen{}, err
	}
	buttons = append(buttons, syncButton)
	configure, err := ui.stateButton(owner, "Runtime config", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: capability.TunnelConfigure, InputKind: inputTunnelConfigure})
	if err != nil {
		return Screen{}, err
	}
	adminKey, err := ui.stateButton(owner, "Set admin key", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: capability.TunnelAdminKeySet, InputKind: inputTunnelAdminKey})
	if err != nil {
		return Screen{}, err
	}
	managed, err := ui.stateButton(owner, "Managed tunnels", CallbackOpen, ActionState{Route: RouteManagedTunnels, Back: RouteTunnel, Operation: capability.TunnelList})
	if err != nil {
		return Screen{}, err
	}
	adminScope, err := ui.stateButton(owner, "Set admin scope", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: capability.TunnelConfigure, InputKind: inputTunnelAdminScope})
	if err != nil {
		return Screen{}, err
	}
	adminEnabled := !view.Admin.Enabled
	adminToggleLabel := "Enable admin"
	if view.Admin.Enabled {
		adminToggleLabel = "Disable admin"
	}
	adminToggle, err := ui.stateButton(owner, adminToggleLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: capability.TunnelConfigure, Input: application.TunnelConfigureInput{AdminEnabled: &adminEnabled}})
	if err != nil {
		return Screen{}, err
	}
	keyControls := []Button{adminKey}
	if view.Admin.KeyConfigured {
		verify, verifyErr := ui.stateButton(owner, "Verify admin", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: capability.TunnelAdminKeyVerify})
		if verifyErr != nil {
			return Screen{}, verifyErr
		}
		keyControls = append(keyControls, verify)
		remove, removeErr := ui.stateButton(owner, "Remove admin key", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteTunnel, Operation: capability.TunnelAdminKeyRemove})
		if removeErr != nil {
			return Screen{}, removeErr
		}
		remove.Role = ButtonRoleDestructive
		keyControls = append(keyControls, remove)
	}
	back, err := ui.backButton(owner, RouteNetwork)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{
		Primary:     append(buttons, managed),
		Secondary:   []Button{configure, adminScope, adminToggle},
		Destructive: keyControls, Navigation: []Button{back, home},
	})}, nil
}

func tunnelViewBlocks(view application.TunnelView) []RichBlock {
	state := "disabled"
	if view.Status.Ready {
		state = "connected"
	} else if view.Status.Restarting {
		state = "reconnecting"
	} else if view.Enabled && view.Status.Running {
		state = "connecting"
	} else if view.Enabled {
		state = "enabled"
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: "OpenAI Secure MCP Tunnel"},
		StateBlock(statusTone(state), displayState(state), ""),
		FieldsBlock("Runtime",
			[]string{"Enabled", boolState(view.Enabled)},
			[]string{"Configured", configuredLabel(view.Configured)},
			[]string{"Runtime key", view.RuntimeKeyPreview},
		),
		FieldsBlock("Administration",
			[]string{"Admin key", view.Admin.KeyPreview},
			[]string{"Verification", stateLabel(view.Admin.Verified, "Verified", "Not verified")},
			[]string{"Read access", stateLabel(view.Admin.Access.Read, "Allowed", "Not allowed")},
			[]string{"Manage access", stateLabel(view.Admin.Access.Manage, "Allowed", "Not allowed")},
		),
	}
	if strings.TrimSpace(view.ID) != "" {
		blocks = append(blocks, RichBlock{Kind: RichCopy, Title: "Tunnel ID", Text: view.ID, CopyText: view.ID})
	}
	if text := tunnelScopeText(view.Admin.Scope); text != "" {
		blocks = append(blocks, expandableTextBlock("Admin scope", text))
	}
	if strings.TrimSpace(view.Status.LastError) != "" {
		blocks = append(blocks, NoticeBlock(ToneWarning, "Runtime error", compactPresentationValue(tracepkg.SanitizeText(view.Status.LastError))))
	}
	return blocks
}

func (ui *Interface) upstreamListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.UpstreamServerList, nil)
	if err != nil {
		return Screen{}, err
	}
	servers, ok := value.([]upstream.Server)
	if !ok {
		return Screen{}, errors.New("upstream list returned an unexpected result")
	}
	sort.Slice(servers, func(i, j int) bool { return strings.ToLower(servers[i].Name) < strings.ToLower(servers[j].Name) })
	start, end, page, pages := PageBounds(len(servers), state.Page, domainPageSize)
	list, buttons := []string{}, []Button{}
	for _, server := range servers[start:end] {
		redacted := application.RedactUpstreamServer(server)
		status := "disabled"
		if redacted.Enabled {
			status = "enabled"
		}
		list = append(list, fmt.Sprintf("%s\n%s · %s", redacted.Name, redacted.Transport, displayState(status)))
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(redacted.Name), CallbackOpen, ActionState{
			Route: RouteUpstream, Back: RouteUpstreams, Operation: capability.UpstreamServerShow,
			ResourceID: redacted.ID, Input: application.UpstreamIDInput{ID: redacted.ID},
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = ButtonRoleResource
		buttons = append(buttons, button)
	}
	add, err := ui.stateButton(owner, "Add server", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstreams, Operation: capability.UpstreamServerAdd, InputKind: inputUpstreamAdd})
	if err != nil {
		return Screen{}, err
	}
	add.Role = ButtonRolePositive
	nav, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Upstream servers", Text: fmt.Sprintf("%d configured", len(servers))}, RichBlock{Kind: RichList, Items: list})
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{add}, Secondary: buttons, Navigation: nav})}, nil
}

func (ui *Interface) upstreamDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.UpstreamServerShow, application.UpstreamIDInput{ID: id})
	if err != nil {
		return Screen{}, err
	}
	server, ok := value.(upstream.Server)
	if !ok {
		return Screen{}, errors.New("upstream view returned an unexpected result")
	}
	fingerprint := application.UpstreamFingerprint(server)
	redacted := application.RedactUpstreamServer(server)
	blocks := []RichBlock{
		{Kind: RichHeading, Title: redacted.Name, Text: redacted.Transport + " upstream server"},
		StateBlock(stateTone(redacted.Enabled), stateLabel(redacted.Enabled, "Enabled", "Disabled"), ""),
		{Kind: RichCopy, Title: "ID", Text: redacted.ID, CopyText: redacted.ID},
		FieldsBlock("Connection", []string{"Auth", redacted.Auth.Type}),
	}
	if state.Detail {
		blocks = append(blocks, upstreamDetailBlocks(redacted)...)
	}
	status, err := ui.stateButton(owner, "Status", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstreams, Operation: capability.UpstreamServerStatus, ResourceID: id, Input: application.UpstreamStatusInput{ID: id, Refresh: true}})
	if err != nil {
		return Screen{}, err
	}
	tools, err := ui.stateButton(owner, "Tools", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstreams, Operation: capability.UpstreamServerTools, ResourceID: id, Input: application.UpstreamStatusInput{ID: id, Refresh: true}})
	if err != nil {
		return Screen{}, err
	}
	oauthStatus, err := ui.stateButton(owner, "OAuth status", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstream, Operation: capability.UpstreamAuthStatus, ResourceID: id, Input: application.UpstreamOAuthInput{ID: id}})
	if err != nil {
		return Screen{}, err
	}
	oauthLogin, err := ui.stateButton(owner, "OAuth login", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstream, Operation: capability.UpstreamAuthLogin, ResourceID: id, InputKind: inputUpstreamOAuthLogin})
	if err != nil {
		return Screen{}, err
	}
	oauthLogout, err := ui.stateButton(owner, "OAuth logout", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstream, Operation: capability.UpstreamAuthLogout, ResourceID: id, Input: application.UpstreamOAuthInput{ID: id}, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	oauthLogout.Role = ButtonRoleDestructive
	config, err := ui.stateButton(owner, "Configure", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstreams, Operation: capability.UpstreamServerConfigure, ResourceID: id, ExpectedVersion: fingerprint, InputKind: inputUpstreamConfigure})
	if err != nil {
		return Screen{}, err
	}
	detail, err := ui.stateButton(owner, "Config details", CallbackOpen, ActionState{Route: RouteUpstream, Back: RouteUpstreams, ResourceID: id, Detail: !state.Detail})
	if err != nil {
		return Screen{}, err
	}
	toggleOp, toggleLabel := capability.UpstreamServerEnable, "Enable"
	if server.Enabled {
		toggleOp, toggleLabel = capability.UpstreamServerDisable, "Disable"
	}
	toggle, err := ui.stateButton(owner, toggleLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstreams, Operation: toggleOp, ResourceID: id, Input: application.UpstreamIDInput{ID: id, ExpectedFingerprint: fingerprint}})
	if err != nil {
		return Screen{}, err
	}
	if server.Enabled {
		toggle.Role = ButtonRoleDestructive
	} else {
		toggle.Role = ButtonRolePositive
	}
	remove, err := ui.stateButton(owner, "Remove", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteUpstreams, Operation: capability.UpstreamServerRemove, ResourceID: id, Input: application.UpstreamIDInput{ID: id, ExpectedFingerprint: fingerprint}})
	if err != nil {
		return Screen{}, err
	}
	remove.Role = ButtonRoleDestructive
	back, err := ui.backButton(owner, RouteUpstreams)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{
		Primary:     []Button{status, tools, oauthStatus},
		Secondary:   []Button{config, detail, oauthLogin},
		Destructive: []Button{toggle, oauthLogout, remove}, Navigation: []Button{back, home},
	})}, nil
}

func upstreamDetailBlocks(server upstream.Server) []RichBlock {
	rows := [][]string{}
	if server.Transport == "http" {
		rows = append(rows, []string{"URL", server.URL})
	} else {
		rows = append(rows, []string{"Command", server.Command}, []string{"Args", strings.Join(server.Args, " ")}, []string{"CWD", server.CWD})
	}
	rows = append(rows, []string{"Env keys", strings.Join(sortedMapKeys(server.Env), ", ")}, []string{"Header keys", strings.Join(sortedMapKeys(server.Headers), ", ")})
	return []RichBlock{{Kind: RichDetails, Title: "Configuration", Text: "Sensitive values are redacted."}, {Kind: RichTable, Rows: rows}}
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func configuredLabel(value bool) string {
	if value {
		return "Configured"
	}
	return "Not configured"
}

func tunnelScopeText(scope tunnel.AdminScope) string {
	if scope.OrganizationID != "" {
		return "organization:" + scope.OrganizationID
	}
	if scope.WorkspaceID != "" {
		return "workspace:" + scope.WorkspaceID
	}
	if scope.TenantID != "" {
		return "tenant:" + scope.TenantID
	}
	return ""
}

func (ui *Interface) networkInputFlow(ctx context.Context, state ActionState) (inputFlowDescriptor, bool, error) {
	text := func(key, label, description, placeholder, example string, required bool) inputFlowField {
		return inputFlowField{Key: key, Label: label, Description: description, Kind: inputFlowText, Required: required, Placeholder: placeholder, Example: example}
	}
	boolField := func(key, label, description string, value bool) inputFlowField {
		return inputFlowField{Key: key, Label: label, Description: description, Kind: inputFlowBool, Required: true, HasDefault: true, Default: strconv.FormatBool(value), Options: inputFlowBoolOptions()}
	}
	validateOrigin := func(value string) error {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("redirect origin must be an absolute origin such as https://admin.example.com")
		}
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")) {
			return errors.New("redirect origin must use HTTPS except for loopback HTTP")
		}
		return nil
	}

	switch state.InputKind {
	case inputTunnelAdminKey:
		return inputFlowDescriptor{
			Title: "Set tunnel admin key", Description: "Configure the OpenAI admin credential used for managed tunnel administration.", SubmitLabel: "Save admin key",
			Fields: []inputFlowField{{Key: "key", Label: "Admin key", Description: "OpenAI admin key. The reply is protected and deleted after capture.", Kind: inputFlowSecret, Required: true, Secret: true, Placeholder: "admin key"}},
			Build: func(data inputFlowData) (any, error) {
				return application.TunnelAdminKeyInput{Key: data.Value("key"), KeySource: "telegram"}, nil
			},
		}, true, nil
	case inputTunnelAdminScope:
		return inputFlowDescriptor{
			Title: "Set tunnel admin scope", Description: "Select exactly one scope used for managed tunnel administration.", SubmitLabel: "Set scope",
			Fields: []inputFlowField{
				{Key: "kind", Label: "Scope type", Description: "Choose which OpenAI resource boundary this admin key manages.", Kind: inputFlowEnum, Required: true, Options: []inputFlowOption{
					{Label: "Organization", Value: "organization"}, {Label: "Workspace", Value: "workspace"}, {Label: "Tenant", Value: "tenant"},
				}},
				text("id", "Scope ID", "Exact ID for the selected scope.", "scope ID", "ws_...", true),
			},
			Build: func(data inputFlowData) (any, error) {
				scope := tunnel.AdminScope{}
				switch data.Value("kind") {
				case "organization":
					scope.OrganizationID = data.Value("id")
				case "workspace":
					scope.WorkspaceID = data.Value("id")
				case "tenant":
					scope.TenantID = data.Value("id")
				default:
					return nil, errors.New("scope type is required")
				}
				if err := tunnel.ValidateAdminScope(scope); err != nil {
					return nil, err
				}
				return application.TunnelConfigureInput{AdminScope: &scope}, nil
			},
		}, true, nil
	case inputUpstreamOAuthLogin:
		field := text("origin", "Redirect origin", "Origin used for the OAuth callback. HTTPS is required except for loopback HTTP.", "https://admin.example.com", "https://admin.example.com", true)
		field.Validate = validateOrigin
		return inputFlowDescriptor{
			Title: "Start Upstream OAuth", Description: "Start OAuth authorization for this upstream server.", SubmitLabel: "Start OAuth",
			Fields: []inputFlowField{field},
			Build: func(data inputFlowData) (any, error) {
				return application.UpstreamOAuthInput{ID: state.ResourceID, RedirectOrigin: data.Value("origin")}, nil
			},
		}, true, nil
	case inputTunnelConfigure:
		value, err := ui.dispatch(ctx, capability.TunnelStatus, nil)
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		view, ok := value.(application.TunnelView)
		if !ok {
			return inputFlowDescriptor{}, true, errors.New("tunnel status returned an unexpected result")
		}
		id := text("id", "Tunnel ID", "OpenAI Secure MCP Tunnel ID used by the runtime.", "tunnel_...", "tunnel_...", false)
		id.HasDefault, id.Default, id.CanClear = true, view.ID, true
		control := text("control_plane_base_url", "Control plane URL", "Optional OpenAI tunnel control-plane base URL.", "https://...", "https://api.openai.com", false)
		control.HasDefault, control.Default, control.CanClear = true, view.ControlPlaneBaseURL, true
		org := text("organization_id", "Organization ID", "Optional OpenAI organization binding for the runtime tunnel.", "org_...", "org_...", false)
		org.HasDefault, org.Default, org.CanClear = true, view.OrganizationID, true
		key := inputFlowField{Key: "api_key", Label: "Runtime API key", Description: "Runtime tunnel credential. Leave unchanged to keep the existing credential.", Kind: inputFlowSecret, Secret: true, Placeholder: "runtime API key"}
		if view.RuntimeKeyConfigured {
			key.HasDefault, key.Default = true, "configured"
		}
		return inputFlowDescriptor{
			Title: "Configure tunnel runtime", Description: "Update the Secure MCP Tunnel runtime connection.", SubmitLabel: "Save runtime config",
			Fields: []inputFlowField{
				boolField("enabled", "Runtime tunnel", "Enable or disable the Secure MCP Tunnel runtime.", view.Enabled),
				id, key, control, org,
			},
			Build: func(data inputFlowData) (any, error) {
				runtime := application.TunnelRuntimeInput{}
				if raw, explicit := data.Explicit("enabled"); explicit {
					enabled, err := inputFlowBoolValue(raw)
					if err != nil {
						return nil, err
					}
					runtime.Enabled = &enabled
				}
				if raw, explicit := data.Explicit("id"); explicit {
					value := raw
					runtime.ID = &value
				}
				if apiKey, explicit := data.Explicit("api_key"); explicit {
					runtime.APIKey = &apiKey
				}
				if raw, explicit := data.Explicit("control_plane_base_url"); explicit {
					value := raw
					runtime.ControlPlaneBaseURL = &value
				}
				if raw, explicit := data.Explicit("organization_id"); explicit {
					value := raw
					runtime.OrganizationID = &value
				}
				if runtime.Enabled == nil && runtime.ID == nil && runtime.APIKey == nil && runtime.ControlPlaneBaseURL == nil && runtime.OrganizationID == nil {
					return nil, errors.New("no tunnel runtime changes selected")
				}
				return application.TunnelConfigureInput{Runtime: &runtime}, nil
			},
		}, true, nil
	case inputManagedTunnelCreate:
		return managedTunnelInputFlow(state, tunnel.Metadata{}, false), true, nil
	case inputManagedTunnelUpdate:
		value, err := ui.dispatch(ctx, capability.TunnelGet, application.ManagedTunnelGetInput{ID: state.ResourceID})
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		metadata, ok := value.(tunnel.Metadata)
		if !ok {
			return inputFlowDescriptor{}, true, errors.New("managed tunnel view returned an unexpected result")
		}
		return managedTunnelInputFlow(state, metadata, true), true, nil
	case inputUpstreamAdd:
		return ui.upstreamInputFlow(ctx, state, upstream.Server{}, false)
	case inputUpstreamConfigure:
		value, err := ui.dispatch(ctx, capability.UpstreamServerShow, application.UpstreamIDInput{ID: state.ResourceID})
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		server, ok := value.(upstream.Server)
		if !ok {
			return inputFlowDescriptor{}, true, errors.New("upstream view returned an unexpected result")
		}
		return ui.upstreamInputFlow(ctx, state, server, true)
	default:
		return inputFlowDescriptor{}, false, nil
	}
}

func managedTunnelInputFlow(state ActionState, current tunnel.Metadata, update bool) inputFlowDescriptor {
	name := inputFlowField{Key: "name", Label: "Name", Description: "Human-readable managed tunnel name.", Kind: inputFlowText, Required: !update, Placeholder: "MCP Tunnel", Example: "MCP Tunnel"}
	description := inputFlowField{Key: "description", Label: "Description", Description: "Optional description shown with the managed tunnel.", Kind: inputFlowText, Placeholder: "Tunnel description", CanClear: update}
	organizations := inputFlowField{Key: "organizations", Label: "Organization IDs", Description: "Optional organization IDs. Enter one per line or separate with commas.", Kind: inputFlowMultiline, Example: "org_..."}
	workspaces := inputFlowField{Key: "workspaces", Label: "Workspace IDs", Description: "Optional workspace IDs. Enter one per line or separate with commas.", Kind: inputFlowMultiline, Example: "ws_..."}
	tenants := inputFlowField{Key: "tenants", Label: "Tenant IDs", Description: "Optional tenant IDs. Enter one per line or separate with commas.", Kind: inputFlowMultiline, Example: "tenant_..."}
	if update {
		name.HasDefault, name.Default = true, current.Name
		description.HasDefault, description.Default = true, current.Description
		organizations.HasDefault, organizations.Default, organizations.CanClear = true, inputFlowListText(current.OrganizationIDs), true
		workspaces.HasDefault, workspaces.Default, workspaces.CanClear = true, inputFlowListText(current.WorkspaceIDs), true
		tenants.HasDefault, tenants.Default, tenants.CanClear = true, inputFlowListText(current.TenantIDs), true
	}
	descriptor := inputFlowDescriptor{
		Title: "Create managed tunnel", Description: "Create an OpenAI managed tunnel.", SubmitLabel: "Create tunnel",
		Fields: []inputFlowField{name, description, organizations, workspaces, tenants},
	}
	if update {
		descriptor.Title, descriptor.Description, descriptor.SubmitLabel = "Update managed tunnel", current.Name+" · managed tunnel", "Save changes"
	}
	descriptor.Build = func(data inputFlowData) (any, error) {
		if update {
			request := tunnel.UpdateRequest{}
			changed := false
			if raw, explicit := data.Explicit("name"); explicit {
				value := raw
				request.Name, changed = &value, true
			}
			if raw, explicit := data.Explicit("description"); explicit {
				value := raw
				request.Description, changed = &value, true
			}
			if raw, explicit := data.Explicit("organizations"); explicit {
				value := inputFlowList(raw)
				request.OrganizationIDs, changed = &value, true
			}
			if raw, explicit := data.Explicit("workspaces"); explicit {
				value := inputFlowList(raw)
				request.WorkspaceIDs, changed = &value, true
			}
			if raw, explicit := data.Explicit("tenants"); explicit {
				value := inputFlowList(raw)
				request.TenantIDs, changed = &value, true
			}
			if !changed {
				return nil, errors.New("no managed tunnel changes selected")
			}
			return application.ManagedTunnelUpdateInput{ID: state.ResourceID, Request: request}, nil
		}
		return application.ManagedTunnelCreateInput{Request: tunnel.CreateRequest{
			Name: data.Value("name"), Description: data.Value("description"),
			OrganizationIDs: inputFlowList(data.Value("organizations")), WorkspaceIDs: inputFlowList(data.Value("workspaces")), TenantIDs: inputFlowList(data.Value("tenants")),
		}}, nil
	}
	return descriptor
}

func (ui *Interface) upstreamInputFlow(_ context.Context, state ActionState, current upstream.Server, update bool) (inputFlowDescriptor, bool, error) {
	if !update {
		current.Enabled, current.Transport, current.Expose, current.IdleTimeoutSec = true, "http", "all", 600
		current.Auth.Type = "auto"
	}
	field := func(key, label, description, value string) inputFlowField {
		return inputFlowField{Key: key, Label: label, Description: description, Kind: inputFlowText, HasDefault: update, Default: value, CanClear: update}
	}
	id := inputFlowField{Key: "id", Label: "Server ID", Description: "Stable identifier for this upstream server.", Kind: inputFlowText, Required: true, Placeholder: "server-id", Example: "github"}
	name := field("name", "Name", "Display name. Empty uses the server ID.", current.Name)
	name.Required = false
	transport := inputFlowField{Key: "transport", Label: "Transport", Description: "How CodeMCP connects to this upstream server.", Kind: inputFlowEnum, Required: true, HasDefault: true, Default: current.Transport, Options: []inputFlowOption{{Label: "HTTP", Value: "http", Description: "Remote Streamable HTTP server."}, {Label: "stdio", Value: "stdio", Description: "Local command launched as a subprocess."}}}
	enabled := inputFlowField{Key: "enabled", Label: "Server state", Description: "Whether this upstream is enabled.", Kind: inputFlowBool, Required: true, HasDefault: true, Default: strconv.FormatBool(current.Enabled), Options: inputFlowBoolOptions()}
	stdio := func(data inputFlowData) bool { return data.Value("transport") == "stdio" }
	httpTransport := func(data inputFlowData) bool { return data.Value("transport") == "http" }
	command := field("command", "Command", "Executable used to start a stdio upstream.", current.Command)
	command.Required, command.When = true, stdio
	args := inputFlowField{Key: "args", Label: "Arguments", Description: "Command arguments. Enter one argument per line.", Kind: inputFlowMultiline, HasDefault: update, Default: inputFlowListText(current.Args), CanClear: true, When: stdio, Example: "--flag\nvalue"}
	cwd := field("cwd", "Working directory", "Optional working directory for the stdio process.", current.CWD)
	cwd.When = stdio
	urlField := field("url", "URL", "HTTPS upstream URL. Loopback HTTP is allowed when private network access is enabled.", current.URL)
	urlField.Required, urlField.When, urlField.Placeholder, urlField.Example = true, httpTransport, "https://mcp.example.com", "https://mcp.example.com"
	private := inputFlowField{Key: "private", Label: "Private network", Description: "Allow private or loopback network destinations.", Kind: inputFlowBool, Required: true, HasDefault: true, Default: strconv.FormatBool(current.AllowPrivateNetwork), Options: inputFlowBoolOptions(), When: httpTransport}
	auth := inputFlowField{Key: "auth", Label: "Authentication", Description: "Authentication strategy for HTTP upstreams.", Kind: inputFlowEnum, Required: true, HasDefault: true, Default: current.Auth.Type, When: httpTransport, Options: []inputFlowOption{{Label: "Auto", Value: "auto"}, {Label: "OAuth", Value: "oauth"}, {Label: "None", Value: "none"}}}
	authScope := field("auth_scope", "OAuth scope", "Optional OAuth scope requested from the upstream.", current.Auth.Scope)
	authScope.When = httpTransport
	bearerEnv := field("bearer_env", "Bearer token env var", "Optional environment variable containing a bearer token.", current.BearerTokenEnvVar)
	bearerEnv.When = httpTransport
	envField := inputFlowField{Key: "env", Label: "Environment", Description: "Environment variables as KEY=VALUE, one per line. Existing values remain unchanged unless you edit this field.", Kind: inputFlowMultiline, Secret: true, HasDefault: update && len(current.Env) > 0, Default: stateLabel(len(current.Env) > 0, "configured", ""), CanClear: update, When: stdio, Example: "TOKEN=secret\nMODE=production"}
	headerField := inputFlowField{Key: "headers", Label: "Headers", Description: "HTTP headers as NAME=VALUE, one per line. Existing values remain unchanged unless you edit this field.", Kind: inputFlowMultiline, Secret: true, HasDefault: update && len(current.Headers) > 0, Default: stateLabel(len(current.Headers) > 0, "configured", ""), CanClear: update, When: httpTransport, Example: "Authorization=Bearer ...\nX-Team=platform"}
	prefix := field("prefix", "Tool prefix", "Prefix applied to proxied tool names. Empty defaults from the server ID.", current.ToolPrefix)
	expose := inputFlowField{Key: "expose", Label: "Tool exposure", Description: "Which upstream tools become available through CodeMCP.", Kind: inputFlowEnum, Required: true, HasDefault: true, Default: current.Expose, Options: []inputFlowOption{{Label: "All", Value: "all"}, {Label: "None", Value: "none"}, {Label: "Metadata only", Value: "meta_only"}, {Label: "Allowlist", Value: "allowlist"}}}
	allowlist := inputFlowField{Key: "tools", Label: "Allowed tools", Description: "Tool names to expose. Enter one per line.", Kind: inputFlowMultiline, HasDefault: update, Default: inputFlowListText(current.Tools), CanClear: true, When: func(data inputFlowData) bool { return data.Value("expose") == "allowlist" }}
	disabled := inputFlowField{Key: "disabled_tools", Label: "Disabled tools", Description: "Tool names to suppress even when otherwise exposed. Enter one per line.", Kind: inputFlowMultiline, HasDefault: update, Default: inputFlowListText(current.DisabledTools), CanClear: true}
	timeout := field("timeout", "Idle timeout", "Idle connection timeout in seconds.", strconv.Itoa(current.IdleTimeoutSec))
	timeout.Required, timeout.Accepted, timeout.HasDefault, timeout.Default = true, "Positive integer seconds.", true, strconv.Itoa(current.IdleTimeoutSec)
	timeout.Validate = func(value string) error { _, err := inputFlowIntValue(value, 1, 0); return err }
	fields := []inputFlowField{}
	if !update {
		fields = append(fields, id)
	}
	fields = append(fields, name, transport, enabled, command, args, cwd, urlField, private, auth, authScope, bearerEnv, envField, headerField, prefix, expose, allowlist, disabled, timeout)
	descriptor := inputFlowDescriptor{Title: "Add Upstream server", Description: "Configure an MCP upstream server without pasting a complete JSON object.", SubmitLabel: "Add server", Fields: fields}
	if update {
		descriptor.Title, descriptor.Description, descriptor.SubmitLabel = "Configure Upstream server", current.Name+" · "+current.Transport+" upstream", "Save changes"
	}
	descriptor.Build = func(data inputFlowData) (any, error) {
		server := current
		if !update {
			server.ID = data.Value("id")
		}
		server.Name, server.Transport = data.Value("name"), data.Value("transport")
		enabledValue, err := inputFlowBoolValue(data.Value("enabled"))
		if err != nil {
			return nil, err
		}
		server.Enabled = enabledValue
		server.ToolPrefix, server.Expose = data.Value("prefix"), data.Value("expose")
		server.Tools, server.DisabledTools = inputFlowList(data.Value("tools")), inputFlowList(data.Value("disabled_tools"))
		server.IdleTimeoutSec, err = inputFlowIntValue(data.Value("timeout"), 1, 0)
		if err != nil {
			return nil, err
		}
		if server.Transport == "stdio" {
			server.Command, server.Args, server.CWD = data.Value("command"), inputFlowLines(data.Value("args")), data.Value("cwd")
			server.URL, server.Headers, server.BearerTokenEnvVar = "", map[string]string{}, ""
			server.Auth = upstream.AuthConfig{Type: "none"}
			if raw, explicit := data.Explicit("env"); explicit {
				assignments, parseErr := upstream.ParseAssignments(inputFlowLines(raw), "environment")
				if parseErr != nil {
					return nil, parseErr
				}
				server.Env = assignments
			} else if !update {
				server.Env = map[string]string{}
			}
		} else {
			server.Command, server.Args, server.CWD, server.Env = "", []string{}, "", map[string]string{}
			server.URL = data.Value("url")
			server.AllowPrivateNetwork, err = inputFlowBoolValue(data.Value("private"))
			if err != nil {
				return nil, err
			}
			server.Auth = upstream.AuthConfig{Type: data.Value("auth"), Scope: data.Value("auth_scope")}
			server.BearerTokenEnvVar = data.Value("bearer_env")
			if raw, explicit := data.Explicit("headers"); explicit {
				assignments, parseErr := upstream.ParseAssignments(inputFlowLines(raw), "headers")
				if parseErr != nil {
					return nil, parseErr
				}
				server.Headers = assignments
			} else if !update {
				server.Headers = map[string]string{}
			}
		}
		normalized, err := upstream.NormalizeServer(server)
		if err != nil {
			return nil, err
		}
		if update {
			return application.UpstreamUpdateInput{ID: state.ResourceID, Server: normalized, ExpectedFingerprint: state.ExpectedVersion}, nil
		}
		return application.UpstreamServerInput{Server: normalized}, nil
	}
	return descriptor, true, nil
}

func (ui *Interface) networkOperationResultScreen(owner ViewOwner, state ActionState, spec capability.Spec, value any) (Screen, bool, error) {
	switch result := value.(type) {
	case mcpoauth.FlowSession:
		back, err := ui.stateButton(owner, "Back to Upstream", CallbackBack, ActionState{Route: RouteUpstream, Back: RouteUpstreams, ResourceID: state.ResourceID})
		if err != nil {
			return Screen{}, true, err
		}
		home, _ := ui.homeButton(owner)
		refresh, err := ui.stateButton(owner, "Refresh OAuth status", CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteUpstream, Operation: capability.UpstreamAuthStatus,
			ResourceID: state.ResourceID, Input: application.UpstreamOAuthInput{ID: state.ResourceID},
		})
		if err != nil {
			return Screen{}, true, err
		}
		open := Button{Text: "Open authorization", URL: result.AuthorizationURL, Role: ButtonRolePrimary}
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "OAuth authorization", Text: "Authorization URL is ready"},
			StateBlock(TonePending, "Authorization required", "Open the authorization page to continue."),
			FieldsBlock("Session", []string{"Expires", result.ExpiresAt.UTC().Format("2006-01-02 15:04:05Z")}),
			RichBlock{Kind: RichDetails, Title: "Credential policy", Text: "OAuth tokens are never rendered into Telegram."},
		), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{open}, Secondary: []Button{refresh}, Navigation: []Button{back, home}})}, true, nil
	case mcpoauth.Status:
		keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, value)
		if err != nil {
			return Screen{}, true, err
		}
		expires := "not set"
		if result.ExpiresAt != nil {
			expires = result.ExpiresAt.UTC().Format("2006-01-02 15:04:05Z")
		}
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Upstream OAuth", Text: result.ServerID},
			StateBlock(stateTone(result.Configured && !result.Expired), stateLabel(result.Configured && !result.Expired, "Ready", "Not ready"), ""),
			FieldsBlock("OAuth",
				[]string{"Configured", configuredLabel(result.Configured)},
				[]string{"Credential", stateLabel(result.Expired, "Expired", "Valid")},
				[]string{"Refresh token", stateLabel(result.HasRefreshToken, "Available", "Not available")},
				[]string{"Expires", expires},
			),
		), Keyboard: keyboard}, true, nil
	}
	keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, value)
	if err != nil {
		return Screen{}, true, err
	}
	switch result := value.(type) {
	case application.TunnelView:
		return Screen{Rich: BuildRichPresentation(tunnelViewBlocks(result)...), Keyboard: keyboard}, true, nil
	case application.TunnelAdminStatus:
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Tunnel admin"},
			StateBlock(stateTone(result.Enabled), stateLabel(result.Enabled, "Enabled", "Disabled"), ""),
			FieldsBlock("Access",
				[]string{"Configuration", configuredLabel(result.Configured)},
				[]string{"Key", result.KeyPreview},
				[]string{"Verification", stateLabel(result.Verified, "Verified", "Not verified")},
				[]string{"Read", stateLabel(result.Access.Read, "Allowed", "Not allowed")},
				[]string{"Manage", stateLabel(result.Access.Manage, "Allowed", "Not allowed")},
			),
		), Keyboard: keyboard}, true, nil
	case application.TunnelVerifyResult:
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Tunnel admin verified", Text: fmt.Sprintf("%d visible tunnel(s)", result.Count)}, expandableTextBlock("Scope", tunnelScopeText(result.Scope))), Keyboard: keyboard}, true, nil
	case tunnel.Metadata:
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Tunnel metadata synced", Text: result.Name}, RichBlock{Kind: RichCopy, Title: "Tunnel ID", Text: result.ID, CopyText: result.ID}), Keyboard: keyboard}, true, nil
	case upstream.Server:
		server := application.RedactUpstreamServer(result)
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: server.Name},
			StateBlock(ToneSuccess, "Upstream updated", ""),
			RichBlock{Kind: RichCopy, Title: "ID", Text: server.ID, CopyText: server.ID},
			FieldsBlock("Configuration", []string{"Transport", server.Transport}, []string{"State", boolState(server.Enabled)}, []string{"Auth", server.Auth.Type}),
		), Keyboard: keyboard}, true, nil
	case upstream.Status:
		blocks := []RichBlock{
			{Kind: RichHeading, Title: result.Name},
			StateBlock(statusTone(string(result.Health)), displayState(string(result.Health)), ""),
			FieldsBlock("Connection",
				[]string{"Enabled", boolState(result.Enabled)},
				[]string{"Connection", stateLabel(result.Connected, "Connected", "Disconnected")},
				[]string{"Tools", fmt.Sprint(result.ToolCount)},
				[]string{"Expose", result.Expose},
			),
		}
		if strings.TrimSpace(result.LastError) != "" {
			blocks = append(blocks, NoticeBlock(ToneWarning, "Last error", compactPresentationValue(tracepkg.SanitizeText(result.LastError))))
		}
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: keyboard}, true, nil
	case application.UpstreamToolsView:
		items := make([]string, 0, len(result.Tools))
		for _, tool := range result.Tools {
			name := tool.Name
			if tool.Title != "" {
				name += " — " + tool.Title
			}
			items = append(items, name)
		}
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: result.Server.Name + " tools", Text: fmt.Sprintf("%d discovered", len(result.Tools))}, RichBlock{Kind: RichList, Items: items}), Keyboard: keyboard}, true, nil
	default:
		return Screen{}, false, nil
	}
}
