package telegram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
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
		button, buttonErr := ui.stateButton(owner, previousNavigationLabel("Newer"), CallbackOpen, previous)
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		nav = append(nav, button)
	}
	if page+1 < pages {
		next := state
		next.Page = page + 1
		button, buttonErr := ui.stateButton(owner, nextNavigationLabel("Older"), CallbackOpen, next)
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		nav = append(nav, button)
	}
	nav = append(nav, back)
	subtitle := fmt.Sprintf("%d setting(s)", len(items))
	if strings.TrimSpace(query) != "" {
		subtitle += " · " + query
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: title, Text: subtitle},
		RichBlock{Kind: RichList, Items: list},
		RichBlock{Kind: RichDetails, Title: "Config import/export", Text: "Portable exports exclude managed secrets. Import requires the managed runtime to be stopped."},
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
		RichBlock{Kind: RichHeading, Title: "Config tools", Text: "Configuration, validation, export, and notification tools"},
		RichBlock{Kind: RichDetails, Title: "Import", Text: "Import is unavailable while the managed runtime is active. Stop the runtime before importing configuration locally."},
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
		rows = append(rows, []string{"Configuration", configuredLabel(*result.Configured)})
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: spec.Label, Text: spec.Description},
		{Kind: RichCopy, Title: "Key", Text: spec.Key, CopyText: spec.Key},
		{Kind: RichFields, Title: "Setting", Rows: rows},
	}
	if state.Detail && strings.TrimSpace(spec.Details) != "" {
		blocks = append(blocks, expandableTextBlock("Details", spec.Details))
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
		items = append(items, entry.label+"\n"+displayState(summary))
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
	keyboard := make([][]Button, 0, (len(buttons)+maxActionButtonsPerRow-1)/maxActionButtonsPerRow+1)
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
		RichBlock{Kind: RichHeading, Title: "Integrations", Text: "Integration status, installation, and configuration"},
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
		active, mode := "", ""
		for _, item := range items {
			switch {
			case strings.HasSuffix(item.Spec.Key, ".active"):
				active = stateLabel(strings.EqualFold(item.Value, "true"), "Enabled", "Disabled")
			case strings.HasSuffix(item.Spec.Key, ".mode"):
				mode = displayState(item.Value)
			}
		}
		return strings.Trim(strings.Join([]string{active, mode}, " · "), " ·"), nil
	case "rtk":
		value, err := ui.dispatch(ctx, capability.IntegrationRTKStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(rtk.Status)
		return fmt.Sprintf("%s · %s", boolState(status.Enabled), displayState(string(status.Source))), nil
	case "codegraph":
		value, err := ui.dispatch(ctx, capability.IntegrationCodeGraphStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(codegraph.Status)
		return fmt.Sprintf("%s · %s", boolState(status.Enabled), displayState(string(status.Resolution.Source))), nil
	case "cf":
		value, err := ui.dispatch(ctx, capability.IntegrationCFStatus, nil)
		if err != nil {
			return "", err
		}
		status, ok := value.(cftunnel.Status)
		if !ok {
			return "", errors.New("cf-tunnel status returned an unexpected result")
		}
		return fmt.Sprintf("%s · %s", displayState(cfTunnelState(status)), displayState(string(status.Source))), nil
	case "typesafe":
		value, err := ui.dispatch(ctx, capability.IntegrationTypeSafeStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(application.TypeSafeStatus)
		return fmt.Sprintf("%s · %s", boolState(status.Enabled), displayState(string(status.State))), nil
	case "telemetry":
		value, err := ui.dispatch(ctx, capability.TelemetryStatus, nil)
		if err != nil {
			return "", err
		}
		status := value.(application.TelemetryStatus)
		return fmt.Sprintf("%s · %s", stateLabel(status.EffectiveEnabled, "Enabled", "Disabled"), displayState(string(status.Source))), nil
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
		display := item.Value
		if strings.HasSuffix(item.Spec.Key, ".active") {
			active = strings.EqualFold(item.Value, "true")
			display = stateLabel(active, "Enabled", "Disabled")
		} else if strings.HasSuffix(item.Spec.Key, ".mode") {
			display = displayState(item.Value)
		}
		rows = append(rows, []string{item.Spec.Label, display})
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
	return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: title}, StateBlock(stateTone(active), stateLabel(active, "Enabled", "Disabled"), ""), RichBlock{Kind: RichFields, Title: "Configuration", Rows: rows}),
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
		RichBlock{Kind: RichHeading, Title: "RTK"},
		StateBlock(stateTone(status.Enabled), stateLabel(status.Enabled, "Enabled", "Disabled"), ""),
		FieldsBlock("Installation",
			[]string{"Source", displayState(string(status.Source))},
			[]string{"Version", status.Version},
			[]string{"Platform", status.Platform},
			[]string{"Managed package", stateLabel(status.ManagedInstalled, "Installed", "Not installed")},
			[]string{"Verification", stateLabel(status.Verified, "Verified", "Not verified")},
		),
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
		RichBlock{Kind: RichHeading, Title: "CodeGraph"},
		StateBlock(stateTone(status.Enabled), stateLabel(status.Enabled, "Enabled", "Disabled"), ""),
		FieldsBlock("Installation",
			[]string{"Source", displayState(string(status.Resolution.Source))},
			[]string{"Platform", status.Platform},
			[]string{"Pinned version", status.PinnedVersion},
			[]string{"Managed package", stateLabel(status.ManagedInstalled, "Installed", "Not installed")},
			[]string{"Verification", stateLabel(status.Resolution.Verified, "Verified", "Not verified")},
		),
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
		{"Source", displayState(string(status.Source))}, {"Managed version", status.Version},
		{"Platform", status.Platform}, {"Verification", stateLabel(status.Verified, "Verified", "Not verified")}, {"Managed package", stateLabel(status.ManagedInstalled, "Installed", "Not installed")}, {"Consumer", status.Consumer},
	}
	if strings.TrimSpace(reportedVersion) != "" {
		rows = append(rows, []string{"Reported version", reportedVersion})
	}
	if strings.TrimSpace(status.Path) != "" {
		rows = append(rows, []string{"Executable", status.Path})
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: "Cloudflare Quick Tunnel"},
		StateBlock(statusTone(state), displayState(state), ""),
		{Kind: RichFields, Title: "Installation", Rows: rows},
		{Kind: RichDetails, Title: "Usage", Text: "This integration provides the temporary public URL used by the Telegram Logs App. Secure MCP Tunnel remains separate."},
	}
	if status.Source == cftunnel.SourceUnavailable {
		blocks = append(blocks, NoticeBlock(ToneWarning, "Unavailable", "Install the managed asset with cm integration cf install, or install cf-tunnel globally."))
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
		RichBlock{Kind: RichHeading, Title: "TypeSafe"},
		StateBlock(statusTone(string(status.State)), displayState(string(status.State)), ""),
		FieldsBlock("Configuration",
			[]string{"Enabled", boolState(status.Enabled)},
			[]string{"API key", keyPreview},
			[]string{"Model", status.Model},
			[]string{"Timeout", fmt.Sprintf("%d ms", status.TimeoutMS)},
		),
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
		{"Configured", stateLabel(status.PersistedEnabled, "Enabled", "Disabled")},
		{"Effective", stateLabel(status.EffectiveEnabled, "Enabled", "Disabled")},
		{"Source", displayState(string(status.Source))},
		{"Environment override", stateLabel(status.EnvironmentOverride, "Active", "Inactive")},
		{"Transport", stateLabel(status.EndpointAvailable, "Available", "Unavailable")},
		{"Product", status.Product},
		{"Identity", stateLabel(status.IdentityPresent, "Present", "Not present")},
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Product telemetry", Text: "Usage telemetry configuration"},
		StateBlock(stateTone(status.EffectiveEnabled), stateLabel(status.EffectiveEnabled, "Enabled", "Disabled"), ""),
		RichBlock{Kind: RichFields, Title: "Telemetry", Rows: rows},
		RichBlock{Kind: RichDetails, Title: "Precedence", Text: "CM_TELEMETRY can override the saved preference. Telegram changes only the saved setting."},
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
		RichBlock{Kind: RichHeading, Title: "Authentication", Text: "Private administration access"},
		FieldsBlock("Credentials",
			[]string{"MCP auth", boolState(authStatus.MCPEnabled)}, []string{"MCP credential", configuredLabel(authStatus.MCPConfigured)},
			[]string{"Admin auth", boolState(authStatus.AdminEnabled)}, []string{"Admin credential", configuredLabel(authStatus.AdminConfigured)},
			[]string{"Bot token", token.Value}, []string{"Authorized users", fmt.Sprint(len(userIDs))},
		),
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
		RichBlock{Kind: RichDetails, Title: "Authorization", Text: "Add a numeric Telegram user ID. Access changes only after confirmation and a successful settings update."},
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
	case inputSettingsSearch:
		return "Search settings", "Reply with text to search setting keys, labels, descriptions, and owners.", "Search settings"
	default:
		return "", "", ""
	}
}

func (ui *Interface) settingsInputFlow(ctx context.Context, state ActionState) (inputFlowDescriptor, bool, error) {
	switch state.InputKind {
	case inputSettingSet:
		value, err := ui.dispatch(ctx, capability.ConfigGet, application.ConfigGetInput{Key: state.ResourceID})
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		result, ok := value.(application.SettingResult)
		if !ok {
			return inputFlowDescriptor{}, true, errors.New("setting view returned an unexpected result")
		}
		field, err := settingInputFlowField(result)
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		return inputFlowDescriptor{
			Title: result.Spec.Label, Description: result.Spec.Description, SubmitLabel: "Save setting",
			Fields: []inputFlowField{field},
			Build: func(data inputFlowData) (any, error) {
				return application.ConfigSetInput{Action: "set", Key: state.ResourceID, Value: data.Value("value"), SecretSource: "telegram"}, nil
			},
		}, true, nil
	case inputTelegramUserManual:
		field := inputFlowField{
			Key: "user_id", Label: "Telegram user ID", Description: "Numeric Telegram user ID to authorize. Access is granted only after the operation succeeds.",
			Kind: inputFlowText, Required: true, Placeholder: "123456789", Example: "123456789", Accepted: "Positive integer Telegram user ID.",
			Validate: func(value string) error {
				parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
				if err != nil || parsed <= 0 {
					return errors.New("telegram user ID must be a positive integer")
				}
				return nil
			},
		}
		return inputFlowDescriptor{
			Title: "Add Telegram user", Description: "Authorize another Telegram account for this private bot.", SubmitLabel: "Authorize user",
			Fields: []inputFlowField{field},
			Build: func(data inputFlowData) (any, error) {
				return application.ConfigSetInput{
					Action: "set", Key: "telegram.allowed_user_ids",
					Value:         appendSettingList(state.ExpectedVersion, data.Value("user_id")),
					ExpectedValue: state.ExpectedVersion, CheckExpected: true,
				}, nil
			},
		}, true, nil
	case inputSettingsApply, inputConfigPatch:
		title, description, submit := "Apply settings", "Apply multiple settings as one validated batch.", "Apply settings"
		if state.InputKind == inputConfigPatch {
			title, description, submit = "Patch configuration", "Apply multiple configuration changes as one validated batch.", "Apply patch"
		}
		field := inputFlowField{
			Key: "changes", Label: "Setting changes",
			Description: "Enter one change per line as key=value. Prefix a key with ! to unset it. The reply is protected because a batch may contain secret values.",
			Kind:        inputFlowMultiline, Required: true, Secret: true, Placeholder: "key=value",
			Example: "http.mcp.port=4000\nintegrations.ponytail.mode=full\n!optional.key",
			Validate: func(value string) error {
				_, err := parseSettingFlowChanges(value)
				return err
			},
		}
		return inputFlowDescriptor{
			Title: title, Description: description, SubmitLabel: submit, Fields: []inputFlowField{field},
			Build: func(data inputFlowData) (any, error) {
				changes, err := parseSettingFlowChanges(data.Value("changes"))
				if err != nil {
					return nil, err
				}
				if state.InputKind == inputConfigPatch {
					return application.ConfigPatchInput{Changes: changes}, nil
				}
				return application.ConfigSetInput{Action: "apply", Changes: changes}, nil
			},
		}, true, nil
	default:
		return inputFlowDescriptor{}, false, nil
	}
}

func settingInputFlowField(result application.SettingResult) (inputFlowField, error) {
	spec := result.Spec
	description := strings.TrimSpace(spec.Description)
	if guidance := strings.TrimSpace(spec.Guidance); guidance != "" {
		if description != "" {
			description += " "
		}
		description += guidance
	}
	field := inputFlowField{
		Key: "value", Label: spec.Label, Description: description, Required: true,
		CanClear: spec.Clearable, Secret: spec.Secret || spec.Sensitive,
	}
	if !field.Secret {
		field.HasDefault, field.Default = true, result.Value
	}
	switch spec.Kind {
	case config.FieldBool:
		field.Kind = inputFlowBool
		field.Options = inputFlowBoolOptions()
		field.Validate = func(value string) error { _, err := inputFlowBoolValue(value); return err }
	case config.FieldEnum:
		field.Kind = inputFlowEnum
		for _, item := range spec.Values {
			field.Options = append(field.Options, inputFlowOption{Label: displayState(item.Value), Value: item.Value, Description: item.Description})
		}
		if len(field.Options) == 0 {
			for _, option := range spec.Options {
				field.Options = append(field.Options, inputFlowOption{Label: displayState(option), Value: option})
			}
		}
		allowed := map[string]struct{}{}
		for _, option := range field.Options {
			allowed[option.Value] = struct{}{}
		}
		field.Validate = func(value string) error {
			if _, ok := allowed[value]; !ok {
				return fmt.Errorf("unsupported value %q", value)
			}
			return nil
		}
	case config.FieldInt:
		field.Kind = inputFlowText
		field.Accepted = "Integer"
		if spec.Input.HasMinInt || spec.Input.HasMaxInt {
			parts := []string{}
			if spec.Input.HasMinInt {
				parts = append(parts, fmt.Sprintf("minimum %d", spec.Input.MinInt))
			}
			if spec.Input.HasMaxInt {
				parts = append(parts, fmt.Sprintf("maximum %d", spec.Input.MaxInt))
			}
			field.Accepted += " · " + strings.Join(parts, " · ")
		}
		field.Validate = func(value string) error {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return errors.New("value must be an integer")
			}
			if spec.Input.HasMinInt && parsed < spec.Input.MinInt {
				return fmt.Errorf("value must be at least %d", spec.Input.MinInt)
			}
			if spec.Input.HasMaxInt && parsed > spec.Input.MaxInt {
				return fmt.Errorf("value must be at most %d", spec.Input.MaxInt)
			}
			return nil
		}
	case config.FieldList:
		field.Kind = inputFlowMultiline
		field.Description = strings.TrimSpace(field.Description + " Enter one item per line.")
		if field.HasDefault {
			field.Default = strings.ReplaceAll(field.Default, ",", "\n")
		}
	case config.FieldString:
		if field.Secret {
			field.Kind = inputFlowSecret
		} else {
			field.Kind = inputFlowText
		}
	default:
		return inputFlowField{}, fmt.Errorf("setting %s does not support interactive editing", spec.Key)
	}
	if field.Description == "" {
		field.Description = "Enter the new value for " + spec.Key + "."
	}
	field.Placeholder = spec.Label
	return field, nil
}

func parseSettingFlowChanges(value string) ([]application.SettingChange, error) {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	changes := make([]application.SettingChange, 0, len(lines))
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "!") {
			key := strings.TrimSpace(strings.TrimPrefix(line, "!"))
			if key == "" {
				return nil, fmt.Errorf("line %d: unset key is required", index+1)
			}
			changes = append(changes, application.SettingChange{Key: key, Unset: true})
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("line %d: expected key=value or !key", index+1)
		}
		changes = append(changes, application.SettingChange{Key: key, Value: strings.TrimSpace(raw)})
	}
	if len(changes) == 0 {
		return nil, errors.New("at least one setting change is required")
	}
	return changes, nil
}

func settingsActionInput(state ActionState, text string) (any, bool, error) {
	switch state.InputKind {
	case inputSettingsSearch:
		return application.ConfigListInput{Query: text}, true, nil
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
			{Kind: RichHeading, Title: result.Spec.Label},
			StateBlock(ToneSuccess, "Updated", ""),
			{Kind: RichCopy, Title: "Key", Text: result.Spec.Key, CopyText: result.Spec.Key},
			FieldsBlock("Result",
				[]string{"Value", result.Value},
				[]string{"Runtime", stateLabel(result.RuntimeReloaded, "Reloaded", "No reload needed")},
			),
		}
		screen := Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}
		if state.SecretInput && result.Spec.Secret {
			if _, sendErr := ui.runtime.SendRichMessage(ctx, owner.ChatID, withRouteBreadcrumb(screen, state), RichMessageOptions{ProtectContent: true}); sendErr != nil {
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
			RichBlock{Kind: RichHeading, Title: "Settings applied"},
			StateBlock(ToneSuccess, fmt.Sprintf("%d change(s) applied", len(result.Results)), ""),
			RichBlock{Kind: RichList, Items: items},
			FieldsBlock("Runtime", []string{"Configuration", stateLabel(result.RuntimeReloaded, "Reloaded", "No reload needed")}),
		), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case application.AuthRotationResult:
		protected := Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Generated credential", Text: "Copy this credential now and store it securely."},
			RichBlock{Kind: RichCopy, Title: "Credential", Text: result.Token, CopyText: result.Token},
		)}
		if _, sendErr := ui.runtime.SendRichMessage(ctx, owner.ChatID, withRouteBreadcrumb(protected, state), RichMessageOptions{ProtectContent: true}); sendErr != nil {
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
			RichBlock{Kind: RichHeading, Title: "Configuration exported"},
			StateBlock(ToneSuccess, "Export sent", "The portable configuration was sent as a protected document."),
			RichBlock{Kind: RichDetails, Title: "Secret policy", Text: "Managed secrets and runtime-only state are excluded from portable exports."},
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
		blocks := []RichBlock{
			{Kind: RichHeading, Title: "Managed cf-tunnel"},
			StateBlock(ToneSuccess, stateLabel(result.Removed, "Removed", "Already absent"), ""),
			FieldsBlock("Current state",
				[]string{"Availability", displayState(cfTunnelState(result.Status))},
				[]string{"Source", displayState(string(result.Status.Source))},
			),
		}
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
	state := "Not installed globally"
	rows := [][]string{}
	if available {
		state = "Global executable detected"
		rows = append(rows, []string{"Path", path})
	}
	blocks := []RichBlock{{Kind: RichHeading, Title: name}, StateBlock(stateTone(available), state, "")}
	if len(rows) > 0 {
		blocks = append(blocks, FieldsBlock("Executable", rows...))
	}
	if !available && managedRecommended {
		blocks = append(blocks, NoticeBlock(ToneWarning, "Managed install available", "Use the verified managed asset instead: "+managedCommand))
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
