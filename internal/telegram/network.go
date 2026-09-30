package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

const (
	inputUpstreamAdd         = "upstream.add.json"
	inputUpstreamConfigure   = "upstream.configure.json"
	inputTunnelConfigure     = "tunnel.configure.json"
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
	screen, err := ui.networkScreen(owner)
	if err != nil {
		screen = ErrorScreen(err)
	}
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
		RichBlock{Kind: RichHeading, Title: "Network", Text: "Canonical connectivity administration"},
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
		{Kind: RichHeading, Title: "OpenAI Secure MCP Tunnel", Text: state},
		{Kind: RichTable, Rows: [][]string{
			{"Enabled", fmt.Sprint(view.Enabled)}, {"Configured", fmt.Sprint(view.Configured)},
			{"Runtime key", view.RuntimeKeyPreview}, {"Admin key", view.Admin.KeyPreview},
			{"Admin verified", fmt.Sprint(view.Admin.Verified)}, {"Read / Manage", fmt.Sprintf("%t / %t", view.Admin.Access.Read, view.Admin.Access.Manage)},
		}},
	}
	if strings.TrimSpace(view.ID) != "" {
		blocks = append(blocks, RichBlock{Kind: RichCopy, Title: "Tunnel ID", Text: view.ID, CopyText: view.ID})
	}
	if text := tunnelScopeText(view.Admin.Scope); text != "" {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Admin scope", Text: text})
	}
	if strings.TrimSpace(view.Status.LastError) != "" {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Runtime error", Text: compactPresentationValue(tracepkg.SanitizeText(view.Status.LastError))})
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
		list = append(list, fmt.Sprintf("%s — %s — %s", redacted.Name, redacted.Transport, status))
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
		{Kind: RichHeading, Title: redacted.Name, Text: redacted.Transport + " Upstream server"},
		{Kind: RichCopy, Title: "ID", Text: redacted.ID, CopyText: redacted.ID},
		{Kind: RichTable, Rows: [][]string{{"Enabled", fmt.Sprint(redacted.Enabled)}, {"Auth", redacted.Auth.Type}, {"Expose", redacted.Expose}, {"Tool prefix", redacted.ToolPrefix}}},
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
		return "configured"
	}
	return "not configured"
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

func networkInputPrompt(kind string) (title, prompt, placeholder string, secret bool) {
	switch kind {
	case inputUpstreamAdd:
		return "Add Upstream server", "Reply with a JSON Upstream server object. The reply is deleted after processing because configuration may contain credentials.", "JSON Upstream server", true
	case inputUpstreamConfigure:
		return "Configure Upstream server", "Reply with the complete JSON Upstream server object. The server ID cannot change. The reply is deleted after processing.", "JSON Upstream server", true
	case inputTunnelConfigure:
		return "Configure tunnel runtime", "Reply with JSON containing any of: enabled, id, api_key, control_plane_base_url, organization_id. This does not verify admin access.", "JSON tunnel runtime config", true
	case inputTunnelAdminKey:
		return "Set tunnel admin key", "Reply with the OpenAI admin key. The reply is protected and deleted after processing. Verification is a separate explicit action.", "admin key", true
	case inputTunnelAdminScope:
		return "Set tunnel admin scope", "Reply with exactly one scope: organization:<id>, workspace:<id>, or tenant:<id>.", "workspace:ws_...", false
	case inputUpstreamOAuthLogin:
		return "Start Upstream OAuth", "Reply with the redirect origin used for the OAuth callback. HTTPS is required except for loopback HTTP origins.", "https://admin.example.com", false
	case inputManagedTunnelCreate:
		return "Create managed tunnel", "Reply with a JSON create request containing name, description and optional organization/workspace/tenant IDs.", "JSON tunnel create request", false
	case inputManagedTunnelUpdate:
		return "Update managed tunnel", "Reply with a JSON update request. Only supplied fields are changed.", "JSON tunnel update request", false
	default:
		return "", "", "", false
	}
}

func networkActionInput(state ActionState, text string) (any, bool, error) {
	text = strings.TrimSpace(text)
	switch state.InputKind {
	case inputUpstreamAdd:
		var server upstream.Server
		if err := json.Unmarshal([]byte(text), &server); err != nil {
			return nil, true, fmt.Errorf("invalid upstream JSON: %w", err)
		}
		return application.UpstreamServerInput{Server: server}, true, nil
	case inputUpstreamConfigure:
		var server upstream.Server
		if err := json.Unmarshal([]byte(text), &server); err != nil {
			return nil, true, fmt.Errorf("invalid upstream JSON: %w", err)
		}
		return application.UpstreamUpdateInput{ID: state.ResourceID, Server: server, ExpectedFingerprint: state.ExpectedVersion}, true, nil
	case inputTunnelConfigure:
		var input application.TunnelRuntimeInput
		if err := json.Unmarshal([]byte(text), &input); err != nil {
			return nil, true, fmt.Errorf("invalid tunnel runtime JSON: %w", err)
		}
		if input.Enabled == nil && input.ID == nil && input.APIKey == nil && input.ControlPlaneBaseURL == nil && input.OrganizationID == nil {
			return nil, true, errors.New("tunnel runtime JSON did not contain a supported field")
		}
		return application.TunnelConfigureInput{Runtime: &input}, true, nil
	case inputTunnelAdminKey:
		if text == "" {
			return nil, true, errors.New("admin key must not be empty")
		}
		return application.TunnelAdminKeyInput{Key: text, KeySource: "telegram"}, true, nil
	case inputTunnelAdminScope:
		kind, value, ok := strings.Cut(text, ":")
		if !ok || strings.TrimSpace(value) == "" {
			return nil, true, errors.New("scope must be organization:<id>, workspace:<id>, or tenant:<id>")
		}
		scope := tunnel.AdminScope{}
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "organization":
			scope.OrganizationID = strings.TrimSpace(value)
		case "workspace":
			scope.WorkspaceID = strings.TrimSpace(value)
		case "tenant":
			scope.TenantID = strings.TrimSpace(value)
		default:
			return nil, true, errors.New("unsupported tunnel admin scope")
		}
		return application.TunnelConfigureInput{AdminScope: &scope}, true, nil
	case inputUpstreamOAuthLogin:
		return application.UpstreamOAuthInput{ID: state.ResourceID, RedirectOrigin: text}, true, nil
	case inputManagedTunnelCreate:
		var request tunnel.CreateRequest
		if err := json.Unmarshal([]byte(text), &request); err != nil {
			return nil, true, fmt.Errorf("invalid managed tunnel create JSON: %w", err)
		}
		return application.ManagedTunnelCreateInput{Request: request}, true, nil
	case inputManagedTunnelUpdate:
		var request tunnel.UpdateRequest
		if err := json.Unmarshal([]byte(text), &request); err != nil {
			return nil, true, fmt.Errorf("invalid managed tunnel update JSON: %w", err)
		}
		return application.ManagedTunnelUpdateInput{ID: state.ResourceID, Request: request}, true, nil
	default:
		return nil, false, nil
	}
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
			RichBlock{Kind: RichTable, Rows: [][]string{{"Expires", result.ExpiresAt.UTC().Format("2006-01-02 15:04:05Z")}}},
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
			RichBlock{Kind: RichTable, Rows: [][]string{
				{"Configured", fmt.Sprint(result.Configured)}, {"Expired", fmt.Sprint(result.Expired)},
				{"Refresh token", fmt.Sprint(result.HasRefreshToken)}, {"Expires", expires},
			}},
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
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Tunnel admin", Text: configuredLabel(result.Configured)}, RichBlock{Kind: RichTable, Rows: [][]string{{"Enabled", fmt.Sprint(result.Enabled)}, {"Key", result.KeyPreview}, {"Verified", fmt.Sprint(result.Verified)}, {"Read / Manage", fmt.Sprintf("%t / %t", result.Access.Read, result.Access.Manage)}}}), Keyboard: keyboard}, true, nil
	case application.TunnelVerifyResult:
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Tunnel admin verified", Text: fmt.Sprintf("%d visible tunnel(s)", result.Count)}, RichBlock{Kind: RichDetails, Title: "Scope", Text: tunnelScopeText(result.Scope)}), Keyboard: keyboard}, true, nil
	case tunnel.Metadata:
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Tunnel metadata synced", Text: result.Name}, RichBlock{Kind: RichCopy, Title: "Tunnel ID", Text: result.ID, CopyText: result.ID}), Keyboard: keyboard}, true, nil
	case upstream.Server:
		server := application.RedactUpstreamServer(result)
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: server.Name, Text: "Upstream operation completed"}, RichBlock{Kind: RichCopy, Title: "ID", Text: server.ID, CopyText: server.ID}, RichBlock{Kind: RichTable, Rows: [][]string{{"Transport", server.Transport}, {"Enabled", fmt.Sprint(server.Enabled)}, {"Auth", server.Auth.Type}}}), Keyboard: keyboard}, true, nil
	case upstream.Status:
		blocks := []RichBlock{RichBlock{Kind: RichHeading, Title: result.Name, Text: string(result.Health)}, RichBlock{Kind: RichTable, Rows: [][]string{{"Enabled", fmt.Sprint(result.Enabled)}, {"Connected", fmt.Sprint(result.Connected)}, {"Tools", fmt.Sprint(result.ToolCount)}, {"Expose", result.Expose}}}}
		if strings.TrimSpace(result.LastError) != "" {
			blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Last error", Text: compactPresentationValue(tracepkg.SanitizeText(result.LastError))})
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
