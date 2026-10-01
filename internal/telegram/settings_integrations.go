package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/integrations/cftunnel"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/integrations/rtk"
)

const (
	inputSettingSet         = "setting.set"
	inputSettingsSearch     = "settings.search"
	inputSettingsApply      = "settings.apply"
	inputConfigPatch        = "config.patch"
	inputTelegramUserManual = "telegram.user.manual"
	settingsPageSize        = 3
)

func (ui *Interface) settingsScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	if state.Detail {
		return ui.configToolsScreen(owner, state)
	}
	prefix := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.ConfigList, application.ConfigListInput{Prefix: prefix})
	if err != nil {
		return Screen{}, err
	}
	items, ok := value.([]application.SettingResult)
	if !ok {
		return Screen{}, errors.New("setting list returned an unexpected result")
	}
	return ui.settingListScreen(owner, state, items, "Settings", prefix)
}

func (ui *Interface) settingListScreen(owner ViewOwner, state ActionState, items []application.SettingResult, title, query string) (Screen, error) {
	start, end, page, pages := PageBounds(len(items), state.Page, settingsPageSize)
	list := make([]string, 0, end-start)
	buttons := make([]Button, 0, end-start)
	for _, item := range items[start:end] {
		value := item.Value
		if item.Spec.Secret && item.Configured != nil && !*item.Configured {
			value = "not configured"
		}
		list = append(list, fmt.Sprintf("%s — %s", item.Spec.Key, compactPresentationValue(value)))
		button, err := ui.stateButton(owner, CompactResourceLabel(item.Spec.Label), CallbackOpen, ActionState{
			Route: RouteSetting, Back: RouteSettings, ResourceID: item.Spec.Key,
		})
		if err != nil {
			return Screen{}, err
		}
		button.Role = ButtonRoleResource
		buttons = append(buttons, button)
	}
	search, err := ui.stateButton(owner, "Search", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteSettings, Operation: capability.ConfigList, InputKind: inputSettingsSearch,
	})
	if err != nil {
		return Screen{}, err
	}
	batch, err := ui.stateButton(owner, "Apply batch", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteSettings, Operation: capability.ConfigSet, InputKind: inputSettingsApply, SecretInput: true,
	})
	if err != nil {
		return Screen{}, err
	}
	tools, err := ui.stateButton(owner, "Config tools", CallbackOpen, ActionState{Route: RouteSettings, Back: state.Back, Detail: true})
	if err != nil {
		return Screen{}, err
	}
	backRoute := state.Back
	if backRoute == "" {
		backRoute = RouteHome
	}
	back, err := ui.backButton(owner, backRoute)
	if err != nil {
		return Screen{}, err
	}
	nav := []Button{}
	if page > 0 {
		previous := state
		previous.Page = page - 1
		button, buttonErr := ui.stateButton(owner, "Newer", CallbackOpen, previous)
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		nav = append(nav, button)
	}
	if page+1 < pages {
		next := state
		next.Page = page + 1
		button, buttonErr := ui.stateButton(owner, "Older", CallbackOpen, next)
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		nav = append(nav, button)
	}
	nav = append(nav, back)
	subtitle := fmt.Sprintf("%d canonical setting(s)", len(items))
	if strings.TrimSpace(query) != "" {
		subtitle += " · " + query
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: title, Text: subtitle},
		RichBlock{Kind: RichList, Items: list},
		RichBlock{Kind: RichDetails, Title: "Config import/export", Text: "Portable export remains secret-free. Import is intentionally unavailable while the Telegram-managed runtime is active because the canonical importer requires a stopped runtime."},
	)
	return Screen{Rich: rich, Keyboard: BoundedActionGroups(ActionGroups{
		Primary:    []Button{search, batch, tools},
		Secondary:  buttons,
		Navigation: nav,
	})}, nil
}

func (ui *Interface) configToolsScreen(owner ViewOwner, state ActionState) (Screen, error) {
	patch, err := ui.stateButton(owner, "Patch", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSettings, Operation: capability.ConfigPatch, InputKind: inputConfigPatch, SecretInput: true})
	if err != nil {
		return Screen{}, err
	}
	export, err := ui.stateButton(owner, "Export config", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSettings, Operation: capability.ConfigExport})
	if err != nil {
		return Screen{}, err
	}
	snapshot, err := ui.stateButton(owner, "Snapshot", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSettings, Operation: capability.ConfigSnapshotRead})
	if err != nil {
		return Screen{}, err
	}
	verify, err := ui.stateButton(owner, "Verify", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSettings, Operation: capability.ConfigVerify})
	if err != nil {
		return Screen{}, err
	}
	path, err := ui.stateButton(owner, "Config path", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSettings, Operation: capability.ConfigPath})
	if err != nil {
		return Screen{}, err
	}
	notifications, err := ui.stateButton(owner, "Notifications", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSettings, Operation: capability.NotificationStatus})
	if err != nil {
		return Screen{}, err
	}
	backState := ActionState{Route: RouteSettings, Back: state.Back}
	back, err := ui.stateButton(owner, "Back", CallbackBack, backState)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Config tools", Text: "Canonical configuration and notification operations"},
		RichBlock{Kind: RichDetails, Title: "Import", Text: "Import remains unavailable while the Telegram-managed runtime is active; stop the runtime and use the canonical local importer."},
	), Keyboard: BoundedActionGroups(ActionGroups{
		Primary:    []Button{patch, export, snapshot},
		Secondary:  []Button{verify, path, notifications},
		Navigation: []Button{back, home},
	})}, nil
}

func (ui *Interface) settingDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	key := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: key})
	if err != nil {
		return Screen{}, err
	}
	result, ok := value.(application.SettingResult)
	if !ok {
		return Screen{}, errors.New("setting read returned an unexpected result")
	}
	spec := result.Spec
	backRoute := state.Back
	if backRoute == "" {
		backRoute = RouteSettings
	}
	rows := [][]string{
		{"Value", result.Value},
		{"Kind", string(spec.Kind)},
		{"Owner", spec.ApplicationOwner},
		{"Role", string(spec.ValueRole)},
	}
	if result.Configured != nil {
		rows = append(rows, []string{"Configured", fmt.Sprint(*result.Configured)})
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: spec.Label, Text: spec.Description},
		{Kind: RichCopy, Title: "Key", Text: spec.Key, CopyText: spec.Key},
		{Kind: RichTable, Rows: rows},
	}
	if state.Detail && strings.TrimSpace(spec.Details) != "" {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Details", Text: spec.Details})
	}
	primary := []Button{}
	secondary := []Button{}
	destructive := []Button{}
	if spec.Writable {
		set, setErr := ui.stateButton(owner, "Set", CallbackOpen, ActionState{
			Route: RouteOperation, Back: backRoute, Operation: capability.ConfigSet, ResourceID: spec.Key,
			InputKind: inputSettingSet, SecretInput: spec.Secret,
		})
		if setErr != nil {
			return Screen{}, setErr
		}
		secondary = append(secondary, set)
	}
	if spec.Clearable || spec.DefaultReset {
		clear, clearErr := ui.stateButton(owner, "Clear", CallbackOpen, ActionState{
			Route: RouteOperation, Back: backRoute, Operation: capability.ConfigSet, ResourceID: spec.Key,
			Input: application.ConfigSetInput{Action: "unset", Key: spec.Key}, ForceConfirm: true,
		})
		if clearErr != nil {
			return Screen{}, clearErr
		}
		clear.Role = ButtonRoleDestructive
		destructive = append(destructive, clear)
	}
	if spec.Rotatable {
		rotate, rotateErr := ui.stateButton(owner, "Rotate", CallbackOpen, ActionState{
			Route: RouteOperation, Back: backRoute, Operation: capability.ConfigSet,
			Input: application.ConfigSetInput{Action: "rotate", Key: spec.Key}, ForceConfirm: true, SecretInput: true,
		})
		if rotateErr != nil {
			return Screen{}, rotateErr
		}
		destructive = append(destructive, rotate)
	}
	if spec.Revealable {
		reveal, revealErr := ui.stateButton(owner, "Reveal", CallbackOpen, ActionState{
			Route: RouteOperation, Back: backRoute, Operation: capability.ConfigGet,
			Input: application.ConfigGetInput{Key: spec.Key, Reveal: true}, ForceConfirm: true, SecretInput: true,
		})
		if revealErr != nil {
			return Screen{}, revealErr
		}
		destructive = append(destructive, reveal)
	}
	if spec.Verifiable {
		verify, verifyErr := ui.stateButton(owner, "Verify", CallbackOpen, ActionState{
			Route: RouteOperation, Back: backRoute, Operation: capability.ConfigSet,
			Input: application.ConfigSetInput{Action: "verify", Key: spec.Key},
		})
		if verifyErr != nil {
			return Screen{}, verifyErr
		}
		primary = append(primary, verify)
	}
	detail, err := ui.stateButton(owner, "Details", CallbackOpen, ActionState{Route: RouteSetting, Back: RouteSettings, ResourceID: spec.Key, Detail: !state.Detail})
	if err != nil {
		return Screen{}, err
	}
	secondary = append(secondary, detail)
	back, err := ui.backButton(owner, backRoute)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{
		Primary: primary, Secondary: secondary, Destructive: destructive, Navigation: []Button{back, home},
	})}, nil
}

func (ui *Interface) integrationsScreen(ctx context.Context, owner ViewOwner) (Screen, error) {
	entries := []struct {
		id    string
		label string
	}{
		{"ponytail", "Ponytail"}, {"caveman", "Caveman"}, {"rtk", "RTK"},
		{"codegraph", "CodeGraph"}, {"cf", "Cloudflare Quick Tunnel"}, {"typesafe", "TypeSafe"}, {"telemetry", "Telemetry"},
	}
	items := make([]string, 0, len(entries))
	buttons := make([]Button, 0, len(entries))
	for _, entry := range entries {
		summary, err := ui.integrationSummary(ctx, entry.id)
		if err != nil {
			summary = "unavailable"
		}
		items = append(items, entry.label+" — "+summary)
		button, buttonErr := ui.stateButton(owner, entry.label, CallbackOpen, ActionState{Route: RouteIntegration, Back: RouteIntegrations, ResourceID: entry.id})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		buttons = append(buttons, button)
	}
	back, err := ui.backButton(owner, RouteHome)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	maxResourceButtons := (maxActionGroupRows - 1) * maxActionButtonsPerRow
	if len(buttons) > maxResourceButtons {
		return Screen{}, fmt.Errorf("integration controls exceed bounded keyboard capacity: %d > %d", len(buttons), maxResourceButtons)
	}
	keyboard := make([][]Button, 0, maxActionGroupRows)
	for start := 0; start < len(buttons); start += maxActionButtonsPerRow {
		end := min(start+maxActionButtonsPerRow, len(buttons))
		row := append([]Button(nil), buttons[start:end]...)
		for index := range row {
			row[index].Text = CompactResourceLabel(row[index].Text)
			row[index].Role = ButtonRoleResource
			row[index].Style = ""
		}
		keyboard = append(keyboard, row)
	}
	keyboard = append(keyboard, []Button{back, home})
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Integrations", Text: "Canonical integration status and lifecycle"},
		RichBlock{Kind: RichList, Items: items},
	), Keyboard: keyboard}, nil
}

func (ui *Interface) integrationSummary(ctx context.Context, id string) (string, error) {
	switch id {
	case "ponytail", "caveman":
		value, err := ui.dispatch(ctx, capability.ConfigList, application.ConfigListInput{Prefix: "integrations." + id})
		if err != nil {
			return "", err
		}
		items, ok := value.([]application.SettingResult)
		if !ok {
			return "", errors.New("integration setting status returned an unexpected result")
		}
		parts := []string{}
		for _, item := range items {
			parts = append(parts, item.Value)
		}
		return strings.Join(parts, " · "), nil
	case "rtk":
		value, err := ui.dispatch(ctx, capability.IntegrationRTKStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(rtk.Status)
		return fmt.Sprintf("%s · %s", boolState(status.Enabled), status.Source), nil
	case "codegraph":
		value, err := ui.dispatch(ctx, capability.IntegrationCodeGraphStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(codegraph.Status)
		return fmt.Sprintf("%s · %s", boolState(status.Enabled), status.Resolution.Source), nil
	case "cf":
		value, err := ui.dispatch(ctx, capability.IntegrationCFStatus, nil)
		if err != nil {
			return "", err
		}
		status, ok := value.(cftunnel.Status)
		if !ok {
			return "", errors.New("cf-tunnel status returned an unexpected result")
		}
		return fmt.Sprintf("%s · %s", cfTunnelState(status), status.Source), nil
	case "typesafe":
		value, err := ui.dispatch(ctx, capability.IntegrationTypeSafeStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(application.TypeSafeStatus)
		return fmt.Sprintf("%s · %s", boolState(status.Enabled), status.State), nil
	case "telemetry":
		value, err := ui.dispatch(ctx, capability.TelemetryStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(application.TelemetryStatus)
		return fmt.Sprintf("effective=%t · source=%s", status.EffectiveEnabled, status.Source), nil
	default:
		return "", errors.New("unknown integration")
	}
}

func (ui *Interface) integrationScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.ToLower(strings.TrimSpace(state.ResourceID))
	switch id {
	case "ponytail", "caveman":
		return ui.simpleSettingIntegrationScreen(ctx, owner, id)
	case "rtk":
		value, err := ui.dispatch(ctx, capability.IntegrationRTKStatus, nil)
		if err != nil {
			return Screen{}, err
		}
		status, ok := value.(rtk.Status)
		if !ok {
			return Screen{}, errors.New("RTK status returned an unexpected result")
		}
		return ui.rtkScreen(owner, status)
	case "codegraph":
		value, err := ui.dispatch(ctx, capability.IntegrationCodeGraphStatus, nil)
		if err != nil {
			return Screen{}, err
		}
		status, ok := value.(codegraph.Status)
		if !ok {
			return Screen{}, errors.New("CodeGraph status returned an unexpected result")
		}
		return ui.codeGraphScreen(owner, status)
	case "cf":
		value, err := ui.dispatch(ctx, capability.IntegrationCFStatus, nil)
		if err != nil {
			return Screen{}, err
		}
		status, ok := value.(cftunnel.Status)
		if !ok {
			return Screen{}, errors.New("cf-tunnel status returned an unexpected result")
		}
		return ui.cfTunnelIntegrationScreen(owner, status)
	case "typesafe":
		value, err := ui.dispatch(ctx, capability.IntegrationTypeSafeStatus, nil)
		if err != nil {
			return Screen{}, err
		}
		status, ok := value.(application.TypeSafeStatus)
		if !ok {
			return Screen{}, errors.New("TypeSafe status returned an unexpected result")
		}
		keyValue, err := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: "integrations.typesafe.api_key"})
		if err != nil {
			return Screen{}, err
		}
		key, ok := keyValue.(application.SettingResult)
		if !ok {
			return Screen{}, errors.New("TypeSafe credential status returned an unexpected result")
		}
		return ui.typeSafeScreen(owner, status, key.Value)
	case "telemetry":
		value, err := ui.dispatch(ctx, capability.TelemetryStatus, nil)
		if err != nil {
			return Screen{}, err
		}
		status, ok := value.(application.TelemetryStatus)
		if !ok {
			return Screen{}, errors.New("telemetry status returned an unexpected result")
		}
		return ui.telemetryScreen(owner, status)
	default:
		return Screen{}, errors.New("unknown integration")
	}
}

func (ui *Interface) simpleSettingIntegrationScreen(ctx context.Context, owner ViewOwner, id string) (Screen, error) {
	prefix := "integrations." + id
	value, err := ui.dispatch(ctx, capability.ConfigList, application.ConfigListInput{Prefix: prefix})
	if err != nil {
		return Screen{}, err
	}
	items := value.([]application.SettingResult)
	rows := [][]string{}
	active := false
	for _, item := range items {
		rows = append(rows, []string{item.Spec.Label, item.Value})
		if strings.HasSuffix(item.Spec.Key, ".active") {
			active = strings.EqualFold(item.Value, "true")
		}
	}
	toggleValue := "true"
	toggleLabel := "Enable"
	if active {
		toggleValue, toggleLabel = "false", "Disable"
	}
	toggle, err := ui.stateButton(owner, toggleLabel, CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteIntegrations, Operation: capability.ConfigSet,
		Input: application.ConfigSetInput{Action: "set", Key: prefix + ".active", Value: toggleValue},
	})
	if err != nil {
		return Screen{}, err
	}
	if active {
		toggle.Role = ButtonRoleDestructive
	} else {
		toggle.Role = ButtonRolePositive
	}
	mode, err := ui.stateButton(owner, "Mode", CallbackOpen, ActionState{
		Route: RouteSetting, Back: RouteIntegrations, ResourceID: prefix + ".mode",
	})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteIntegrations)
	home, _ := ui.homeButton(owner)
	title := strings.ToUpper(id[:1]) + id[1:]
	return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: title, Text: "Setting-owned integration"}, RichBlock{Kind: RichTable, Rows: rows}),
		Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{toggle}, Secondary: []Button{mode}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) rtkScreen(owner ViewOwner, status rtk.Status) (Screen, error) {
	toggleOp, toggleLabel := capability.IntegrationRTKEnable, "Enable"
	if status.Enabled {
		toggleOp, toggleLabel = capability.IntegrationRTKDisable, "Disable"
	}
	toggle, err := ui.stateButton(owner, toggleLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: toggleOp})
	if err != nil {
		return Screen{}, err
	}
	if status.Enabled {
		toggle.Role = ButtonRoleDestructive
	} else {
		toggle.Role = ButtonRolePositive
	}
	probe, err := ui.stateButton(owner, "Probe", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationRTKProbe})
	if err != nil {
		return Screen{}, err
	}
	install, err := ui.stateButton(owner, "Install managed", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationRTKInstall})
	if err != nil {
		return Screen{}, err
	}
	install.Role = ButtonRolePositive
	global, err := ui.stateButton(owner, "Check global", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationRTKInstallGlobal})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteIntegrations)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "RTK", Text: string(status.Source)},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Enabled", fmt.Sprint(status.Enabled)}, {"Version", status.Version}, {"Platform", status.Platform}, {"Managed installed", fmt.Sprint(status.ManagedInstalled)}, {"Verified", fmt.Sprint(status.Verified)}}},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{toggle, probe}, Secondary: []Button{install, global}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) codeGraphScreen(owner ViewOwner, status codegraph.Status) (Screen, error) {
	enabled := !status.Enabled
	toggleLabel := "Enable"
	if status.Enabled {
		toggleLabel = "Disable"
	}
	toggle, err := ui.stateButton(owner, toggleLabel, CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteIntegrations, Operation: capability.ConfigSet,
		Input: application.ConfigSetInput{Action: "set", Key: "integrations.codegraph.enabled", Value: strconv.FormatBool(enabled)},
	})
	if err != nil {
		return Screen{}, err
	}
	if status.Enabled {
		toggle.Role = ButtonRoleDestructive
	} else {
		toggle.Role = ButtonRolePositive
	}
	probe, err := ui.stateButton(owner, "Probe", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationCodeGraphProbe})
	if err != nil {
		return Screen{}, err
	}
	install, err := ui.stateButton(owner, "Install managed", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationCodeGraphInstall})
	if err != nil {
		return Screen{}, err
	}
	install.Role = ButtonRolePositive
	global, err := ui.stateButton(owner, "Check global", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationCodeGraphInstallGlobal})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteIntegrations)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "CodeGraph", Text: string(status.Resolution.Source)},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Enabled", fmt.Sprint(status.Enabled)}, {"Platform", status.Platform}, {"Pinned version", status.PinnedVersion}, {"Managed installed", fmt.Sprint(status.ManagedInstalled)}, {"Verified", fmt.Sprint(status.Resolution.Verified)}}},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{toggle, probe}, Secondary: []Button{install, global}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) cfTunnelIntegrationScreen(owner ViewOwner, status cftunnel.Status) (Screen, error) {
	probe, err := ui.stateButton(owner, "Probe", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationCFProbe})
	if err != nil {
		return Screen{}, err
	}
	primary := []Button{probe}
	if status.Source == cftunnel.SourceUnavailable && status.ManagedSupported {
		install, installErr := ui.stateButton(owner, "Install managed", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationCFInstall})
		if installErr != nil {
			return Screen{}, installErr
		}
		install.Role = ButtonRolePositive
		primary = append(primary, install)
	}
	secondary := []Button{}
	destructive := []Button{}
	if status.ManagedInstalled {
		update, updateErr := ui.stateButton(owner, "Update managed", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationCFUpdate})
		if updateErr != nil {
			return Screen{}, updateErr
		}
		secondary = append(secondary, update)
		remove, removeErr := ui.stateButton(owner, "Remove managed", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationCFRemove})
		if removeErr != nil {
			return Screen{}, removeErr
		}
		remove.Role = ButtonRoleDestructive
		destructive = append(destructive, remove)
	}
	back, _ := ui.backButton(owner, RouteIntegrations)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(cfTunnelStatusBlocks(status, "")...), Keyboard: BoundedActionGroups(ActionGroups{
		Primary: primary, Secondary: secondary, Destructive: destructive, Navigation: []Button{back, home},
	})}, nil
}

func cfTunnelState(status cftunnel.Status) string {
	if status.Source == cftunnel.SourceUnavailable {
		return "unavailable"
	}
	if status.Verified {
		return "ready"
	}
	return "unverified"
}

func cfTunnelStatusBlocks(status cftunnel.Status, reportedVersion string) []RichBlock {
	state := cfTunnelState(status)
	rows := [][]string{
		{"State", state}, {"Source", string(status.Source)}, {"Managed version", status.Version},
		{"Platform", status.Platform}, {"Verified", fmt.Sprint(status.Verified)}, {"Managed installed", fmt.Sprint(status.ManagedInstalled)}, {"Consumer", status.Consumer},
	}
	if strings.TrimSpace(reportedVersion) != "" {
		rows = append(rows, []string{"Reported version", reportedVersion})
	}
	if strings.TrimSpace(status.Path) != "" {
		rows = append(rows, []string{"Executable", status.Path})
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: "Cloudflare Quick Tunnel", Text: state},
		{Kind: RichTable, Rows: rows},
		{Kind: RichDetails, Title: "Ownership", Text: "cf-tunnel is an integration used only for ephemeral Telegram Logs Mini App ingress. OpenAI Secure MCP Tunnel remains the persistent MCP tunnel authority."},
	}
	if status.Source == cftunnel.SourceUnavailable {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Unavailable", Text: "Install the verified managed asset with cm integration cf install, or install cf-tunnel globally yourself."})
	}
	return blocks
}

func (ui *Interface) typeSafeScreen(owner ViewOwner, status application.TypeSafeStatus, keyPreview string) (Screen, error) {
	toggleOp, toggleLabel := capability.IntegrationTypeSafeEnable, "Enable"
	if status.Enabled {
		toggleOp, toggleLabel = capability.IntegrationTypeSafeDisable, "Disable"
	}
	toggle, err := ui.stateButton(owner, toggleLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: toggleOp})
	if err != nil {
		return Screen{}, err
	}
	if status.Enabled {
		toggle.Role = ButtonRoleDestructive
	} else {
		toggle.Role = ButtonRolePositive
	}
	probe, err := ui.stateButton(owner, "Probe", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationTypeSafeProbe})
	if err != nil {
		return Screen{}, err
	}
	doctor, err := ui.stateButton(owner, "Doctor", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteIntegrations, Operation: capability.IntegrationTypeSafeDoctor,
		Input: application.TypeSafeDoctorInput{Probe: true},
	})
	if err != nil {
		return Screen{}, err
	}
	key, err := ui.stateButton(owner, "API key", CallbackOpen, ActionState{Route: RouteSetting, Back: RouteIntegrations, ResourceID: "integrations.typesafe.api_key"})
	if err != nil {
		return Screen{}, err
	}
	configButton, err := ui.stateButton(owner, "Model / timeout", CallbackOpen, ActionState{Route: RouteSettings, Back: RouteIntegrations, ResourceID: "integrations.typesafe"})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteIntegrations)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "TypeSafe", Text: string(status.State)},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Enabled", fmt.Sprint(status.Enabled)}, {"API key", keyPreview}, {"Model", status.Model}, {"Timeout", fmt.Sprintf("%d ms", status.TimeoutMS)}}},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{toggle, probe, doctor}, Secondary: []Button{key, configButton}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) telemetryScreen(owner ViewOwner, status application.TelemetryStatus) (Screen, error) {
	toggleOp, toggleLabel := capability.TelemetryEnable, "Enable"
	if status.PersistedEnabled {
		toggleOp, toggleLabel = capability.TelemetryDisable, "Disable"
	}
	toggle, err := ui.stateButton(owner, toggleLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: toggleOp})
	if err != nil {
		return Screen{}, err
	}
	if status.PersistedEnabled {
		toggle.Role = ButtonRoleDestructive
	} else {
		toggle.Role = ButtonRolePositive
	}
	show, err := ui.stateButton(owner, "Show details", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteIntegrations, Operation: capability.TelemetryShow})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteIntegrations)
	home, _ := ui.homeButton(owner)
	rows := [][]string{
		{"Configured", fmt.Sprint(status.PersistedEnabled)},
		{"Effective", fmt.Sprint(status.EffectiveEnabled)},
		{"Source", string(status.Source)},
		{"Environment override", fmt.Sprint(status.EnvironmentOverride)},
		{"Transport available", fmt.Sprint(status.EndpointAvailable)},
		{"Product", status.Product},
		{"Identity present", fmt.Sprint(status.IdentityPresent)},
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Product telemetry", Text: "Privacy-bounded operator status"},
		RichBlock{Kind: RichTable, Rows: rows},
		RichBlock{Kind: RichDetails, Title: "Precedence", Text: "CM_TELEMETRY can override the persisted preference; Telegram changes only the canonical persisted setting."},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{toggle}, Secondary: []Button{show}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) authScreen(ctx context.Context, owner ViewOwner) (Screen, error) {
	authValue, err := ui.dispatch(ctx, capability.AuthStatus, nil)
	if err != nil {
		return Screen{}, err
	}
	authStatus, ok := authValue.(application.AuthStatus)
	if !ok {
		return Screen{}, errors.New("authentication status returned an unexpected result")
	}
	tokenValue, err := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: "telegram.token"})
	if err != nil {
		return Screen{}, err
	}
	token := tokenValue.(application.SettingResult)
	usersValue, err := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: "telegram.allowed_user_ids"})
	if err != nil {
		return Screen{}, err
	}
	users := usersValue.(application.SettingResult)
	userIDs := parseSettingList(users.Value)
	usersButton, err := ui.stateButton(owner, "Authorized users", CallbackOpen, ActionState{Route: RouteAuthorizedUsers, Back: RouteAuth})
	if err != nil {
		return Screen{}, err
	}
	tokenButton, err := ui.stateButton(owner, "Bot token", CallbackOpen, ActionState{Route: RouteSetting, Back: RouteAuth, ResourceID: "telegram.token"})
	if err != nil {
		return Screen{}, err
	}
	mcpToggleOp, mcpToggleLabel := capability.AuthMCPEnable, "Enable MCP auth"
	if authStatus.MCPEnabled {
		mcpToggleOp, mcpToggleLabel = capability.AuthMCPDisable, "Disable MCP auth"
	}
	mcpToggle, err := ui.stateButton(owner, mcpToggleLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteAuth, Operation: mcpToggleOp, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	if authStatus.MCPEnabled {
		mcpToggle.Role = ButtonRoleDestructive
	} else {
		mcpToggle.Role = ButtonRolePositive
	}
	mcpRotate, err := ui.stateButton(owner, "Rotate MCP credential", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteAuth, Operation: capability.AuthMCPRotate, ForceConfirm: true, SecretInput: true})
	if err != nil {
		return Screen{}, err
	}
	adminToggleOp, adminToggleLabel := capability.AuthAdminEnable, "Enable admin auth"
	if authStatus.AdminEnabled {
		adminToggleOp, adminToggleLabel = capability.AuthAdminDisable, "Disable admin auth"
	}
	adminToggle, err := ui.stateButton(owner, adminToggleLabel, CallbackOpen, ActionState{Route: RouteOperation, Back: RouteAuth, Operation: adminToggleOp, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	if authStatus.AdminEnabled {
		adminToggle.Role = ButtonRoleDestructive
	} else {
		adminToggle.Role = ButtonRolePositive
	}
	adminRotate, err := ui.stateButton(owner, "Rotate admin credential", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteAuth, Operation: capability.AuthAdminRotate, ForceConfirm: true, SecretInput: true})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteHome)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Authentication", Text: "Telegram private administration boundary"},
		RichBlock{Kind: RichTable, Rows: [][]string{
			{"MCP auth", boolState(authStatus.MCPEnabled)}, {"MCP credential", configuredLabel(authStatus.MCPConfigured)},
			{"Admin auth", boolState(authStatus.AdminEnabled)}, {"Admin credential", configuredLabel(authStatus.AdminConfigured)},
			{"Bot token", token.Value}, {"Authorized users", fmt.Sprint(len(userIDs))},
		}},
		RichBlock{Kind: RichDetails, Title: "Credential handling", Text: "Generated credentials are sent as protected content and are never rendered into the ordinary administration message."},
	), Keyboard: BoundedActionGroups(ActionGroups{
		Primary:    []Button{mcpToggle, adminToggle, usersButton},
		Secondary:  []Button{mcpRotate, adminRotate, tokenButton},
		Navigation: []Button{back, home},
	})}, nil
}

func (ui *Interface) authorizedUsersScreen(ctx context.Context, owner ViewOwner) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: "telegram.allowed_user_ids"})
	if err != nil {
		return Screen{}, err
	}
	result := value.(application.SettingResult)
	ids := parseSettingList(result.Value)
	items := make([]string, 0, len(ids))
	removeButtons := make([]Button, 0, len(ids))
	for _, id := range ids {
		items = append(items, id)
		remaining := slices.DeleteFunc(append([]string(nil), ids...), func(value string) bool { return value == id })
		removeInput := application.ConfigSetInput{
			Action: "set", Key: "telegram.allowed_user_ids", Value: strings.Join(remaining, ","),
			ExpectedValue: result.Value, CheckExpected: true,
		}
		if len(remaining) == 0 {
			removeInput.Action = "apply"
			removeInput.Changes = []application.SettingChange{
				{Key: "telegram.allowed_user_ids", Value: ""},
				{Key: "telegram.enabled", Value: "false"},
			}
		}
		button, buttonErr := ui.stateButton(owner, "Remove "+id, CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteAuthorizedUsers, Operation: capability.ConfigSet, ForceConfirm: true,
			Input: removeInput,
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = ButtonRoleDestructive
		removeButtons = append(removeButtons, button)
	}
	manual, err := ui.stateButton(owner, "Add by ID", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteAuthorizedUsers, Operation: capability.ConfigSet,
		ResourceID: "telegram.allowed_user_ids", ExpectedVersion: result.Value, InputKind: inputTelegramUserManual, ForceConfirm: true,
	})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteAuth)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Authorized Telegram users", Text: fmt.Sprintf("%d user(s)", len(ids))},
		RichBlock{Kind: RichList, Items: items},
		RichBlock{Kind: RichDetails, Title: "Authorization", Text: "Add a numeric Telegram user ID. Authorization changes only after explicit confirmation and successful canonical setting mutation."},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{manual}, Destructive: removeButtons, Navigation: []Button{back, home}})}, nil
}

func parseSettingList(raw string) []string {
	values := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' })
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func appendSettingList(raw, value string) string {
	values := parseSettingList(raw)
	if !slices.Contains(values, value) {
		values = append(values, value)
	}
	return strings.Join(values, ",")
}

func settingsInputPrompt(state ActionState) (title, prompt, placeholder string) {
	switch state.InputKind {
	case inputSettingSet:
		return "Set setting", "Reply with the new value. Canonical SettingService metadata and validation decide whether the mutation is allowed.", "New value"
	case inputSettingsSearch:
		return "Search settings", "Reply with text to search canonical setting keys, labels, descriptions and owners.", "Search settings"
	case inputSettingsApply:
		return "Apply settings atomically", "Reply with a JSON array of changes. Example: [{\"key\":\"http.mcp.port\",\"value\":\"4000\"}]. The canonical transaction validates the whole batch before persisting.", "JSON setting changes"
	case inputConfigPatch:
		return "Patch configuration", "Reply with a JSON array of setting changes. The canonical patch validates the complete batch before persistence.", "JSON setting changes"
	case inputTelegramUserManual:
		return "Add Telegram user", "Reply with the numeric Telegram user ID. The candidate is not authorized until the confirmation succeeds.", "Telegram user ID"
	default:
		return "", "", ""
	}
}

func settingsActionInput(state ActionState, text string) (any, bool, error) {
	switch state.InputKind {
	case inputSettingSet:
		return application.ConfigSetInput{Action: "set", Key: state.ResourceID, Value: text, SecretSource: "telegram"}, true, nil
	case inputSettingsSearch:
		return application.ConfigListInput{Query: text}, true, nil
	case inputSettingsApply:
		var changes []application.SettingChange
		if err := json.Unmarshal([]byte(text), &changes); err != nil {
			return nil, true, fmt.Errorf("invalid setting-change JSON: %w", err)
		}
		if len(changes) == 0 {
			return nil, true, errors.New("at least one setting change is required")
		}
		return application.ConfigSetInput{Action: "apply", Changes: changes}, true, nil
	case inputConfigPatch:
		var changes []application.SettingChange
		if err := json.Unmarshal([]byte(text), &changes); err != nil {
			return nil, true, fmt.Errorf("invalid configuration patch JSON: %w", err)
		}
		if len(changes) == 0 {
			return nil, true, errors.New("at least one configuration change is required")
		}
		return application.ConfigPatchInput{Changes: changes}, true, nil
	case inputTelegramUserManual:
		return application.ConfigSetInput{
			Action: "set", Key: "telegram.allowed_user_ids",
			Value:         appendSettingList(state.ExpectedVersion, strings.TrimSpace(text)),
			ExpectedValue: state.ExpectedVersion, CheckExpected: true,
		}, true, nil
	default:
		return nil, false, nil
	}
}

func (ui *Interface) settingsOperationResultScreen(ctx context.Context, owner ViewOwner, state ActionState, spec capability.Spec, value any) (Screen, bool, error) {
	switch result := value.(type) {
	case []application.SettingResult:
		screen, err := ui.settingListScreen(owner, ActionState{Route: RouteSettings, Back: RouteHome}, result, "Settings search", "filtered")
		return screen, true, err
	case application.SettingResult:
		back, err := ui.backButton(owner, state.Back)
		if err != nil {
			return Screen{}, true, err
		}
		home, _ := ui.homeButton(owner)
		blocks := []RichBlock{
			{Kind: RichHeading, Title: result.Spec.Label, Text: "Canonical setting operation completed"},
			{Kind: RichCopy, Title: "Key", Text: result.Spec.Key, CopyText: result.Spec.Key},
			{Kind: RichTable, Rows: [][]string{{"Value", result.Value}, {"Runtime reloaded", fmt.Sprint(result.RuntimeReloaded)}}},
		}
		screen := Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}
		if state.SecretInput && result.Spec.Secret {
			if _, sendErr := ui.runtime.SendRichMessage(ctx, owner.ChatID, screen, RichMessageOptions{ProtectContent: true}); sendErr != nil {
				return Screen{}, true, sendErr
			}
			return Screen{Rich: BuildRichPresentation(
				RichBlock{Kind: RichHeading, Title: result.Spec.Label, Text: "Sensitive result sent separately"},
				RichBlock{Kind: RichDetails, Title: "History policy", Text: "The sensitive result was sent with Telegram protected-content handling instead of replacing this ordinary administration message."},
			), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
		}
		return screen, true, nil
	case application.SettingApplyResult:
		back, err := ui.backButton(owner, state.Back)
		if err != nil {
			return Screen{}, true, err
		}
		home, _ := ui.homeButton(owner)
		items := make([]string, 0, len(result.Results))
		for _, item := range result.Results {
			items = append(items, item.Spec.Key+" — "+item.Value)
		}
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Settings applied", Text: fmt.Sprintf("%d change(s)", len(result.Results))},
			RichBlock{Kind: RichList, Items: items},
			RichBlock{Kind: RichDetails, Title: "Runtime", Text: fmt.Sprintf("reloaded=%t", result.RuntimeReloaded)},
		), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case application.AuthRotationResult:
		protected := Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Generated credential", Text: "Copy this credential now and store it securely."},
			RichBlock{Kind: RichCopy, Title: "Credential", Text: result.Token, CopyText: result.Token},
		)}
		if _, sendErr := ui.runtime.SendRichMessage(ctx, owner.ChatID, protected, RichMessageOptions{ProtectContent: true}); sendErr != nil {
			return Screen{}, true, sendErr
		}
		back, err := ui.backButton(owner, RouteAuth)
		if err != nil {
			return Screen{}, true, err
		}
		home, _ := ui.homeButton(owner)
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Credential rotated", Text: "Sensitive credential sent separately"},
			RichBlock{Kind: RichDetails, Title: "History policy", Text: "The generated value was sent using Telegram protected-content handling and is omitted from this ordinary administration message."},
		), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case application.ConfigExportDocument:
		if err := ui.runtime.SendDocument(ctx, owner.ChatID, DocumentUpload{
			FileName: result.FileName, ContentType: "application/json", Data: result.Data,
			Caption: "Portable CodeMCP configuration export · managed secrets excluded", ProtectContent: true,
		}); err != nil {
			return Screen{}, true, err
		}
		back, err := ui.backButton(owner, RouteSettings)
		if err != nil {
			return Screen{}, true, err
		}
		home, _ := ui.homeButton(owner)
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Configuration exported", Text: "The safe portable envelope was sent as a protected document."},
			RichBlock{Kind: RichDetails, Title: "Secret policy", Text: "Managed secrets and runtime-only state are excluded by the canonical exporter."},
		), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case application.TelemetryStatus:
		screen, err := ui.telemetryScreen(owner, result)
		return screen, true, err
	case application.TypeSafeStatus:
		keyValue, getErr := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: "integrations.typesafe.api_key"})
		if getErr != nil {
			return Screen{}, true, getErr
		}
		key, ok := keyValue.(application.SettingResult)
		if !ok {
			return Screen{}, true, errors.New("TypeSafe credential status returned an unexpected result")
		}
		screen, err := ui.typeSafeScreen(owner, result, key.Value)
		return screen, true, err
	case rtk.ProbeResult:
		screen, err := ui.rtkScreen(owner, result.Status)
		return screen, true, err
	case rtk.InstallResult:
		screen, err := ui.rtkScreen(owner, result.Status)
		return screen, true, err
	case rtk.GlobalResolutionResult:
		return ui.globalExecutableResultScreen(owner, "RTK", result.Available, result.Path, result.ManagedRecommended, "cm integration rtk install")
	case codegraph.ProbeResult:
		screen, err := ui.codeGraphScreen(owner, result.Status)
		return screen, true, err
	case codegraph.InstallResult:
		screen, err := ui.codeGraphScreen(owner, result.Status)
		return screen, true, err
	case codegraph.GlobalResolutionResult:
		return ui.globalExecutableResultScreen(owner, "CodeGraph", result.Available, result.Path, result.ManagedRecommended, "cm integration codegraph install")
	case cftunnel.Status:
		screen, err := ui.cfTunnelIntegrationScreen(owner, result)
		return screen, true, err
	case cftunnel.ProbeResult:
		keyboard, navigationErr := ui.integrationResultNavigation(owner)
		if navigationErr != nil {
			return Screen{}, true, navigationErr
		}
		return Screen{Rich: BuildRichPresentation(cfTunnelStatusBlocks(result.Status, result.Version)...), Keyboard: keyboard}, true, nil
	case cftunnel.InstallResult:
		screen, err := ui.cfTunnelIntegrationScreen(owner, result.Status)
		return screen, true, err
	case cftunnel.RemoveResult:
		blocks := cfTunnelStatusBlocks(result.Status, "")
		blocks = append([]RichBlock{{Kind: RichHeading, Title: "Managed cf-tunnel removed", Text: fmt.Sprintf("removed=%t", result.Removed)}}, blocks...)
		keyboard, navigationErr := ui.integrationResultNavigation(owner)
		if navigationErr != nil {
			return Screen{}, true, navigationErr
		}
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: keyboard}, true, nil
	case application.TypeSafeProbeResult:
		keyValue, getErr := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: "integrations.typesafe.api_key"})
		if getErr != nil {
			return Screen{}, true, getErr
		}
		key := keyValue.(application.SettingResult)
		screen, err := ui.typeSafeScreen(owner, result.Status, key.Value)
		return screen, true, err
	case rtk.Status:
		screen, err := ui.rtkScreen(owner, result)
		return screen, true, err
	case codegraph.Status:
		screen, err := ui.codeGraphScreen(owner, result)
		return screen, true, err
	default:
		_ = ctx
		_ = spec
		return Screen{}, false, nil
	}
}

func (ui *Interface) integrationResultNavigation(owner ViewOwner) ([][]Button, error) {
	back, err := ui.backButton(owner, RouteIntegrations)
	if err != nil {
		return nil, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return nil, err
	}
	return BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}}), nil
}

func (ui *Interface) globalExecutableResultScreen(owner ViewOwner, name string, available bool, path string, managedRecommended bool, managedCommand string) (Screen, bool, error) {
	state := "not installed globally"
	rows := [][]string{{"Available", fmt.Sprint(available)}}
	if available {
		state = "global executable detected"
		rows = append(rows, []string{"Path", path})
	}
	blocks := []RichBlock{{Kind: RichHeading, Title: name, Text: state}, {Kind: RichTable, Rows: rows}}
	if !available && managedRecommended {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Recommended", Text: "Use the verified managed asset instead: " + managedCommand})
	}
	back, err := ui.backButton(owner, RouteIntegrations)
	if err != nil {
		return Screen{}, true, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, true, err
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
}
