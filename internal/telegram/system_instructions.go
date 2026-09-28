package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/doctor"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/tools"
	updatepkg "go.mewis.me/codemcp/internal/update"
)

const (
	inputInstructionPatch = "instructions.patch"
	inputProjectContext   = "instructions.project-context"
	inputPromptWorkspace  = "prompts.workspace"
	inputPromptCreate     = "prompts.create"
	inputPromptUpdate     = "prompts.update"
)

func (ui *Interface) handleSystem(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen, err := ui.systemScreen(ctx, owner)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_, _ = ui.runtime.SendRichMessageToTopic(ctx, owner.ChatID, TopicRuntime, screen, RichMessageOptions{})
}

func (ui *Interface) handleInstructions(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen, err := ui.instructionsScreen(ctx, owner)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) systemScreen(ctx context.Context, owner ViewOwner) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.VersionAbout, nil)
	if err != nil {
		return Screen{}, err
	}
	about, ok := value.(application.AboutInfo)
	if !ok {
		return Screen{}, errors.New("version/about returned an unexpected result")
	}
	doctorButton, err := ui.stateButton(owner, "Doctor", CallbackOpen, ActionState{Route: RouteDoctor, Back: RouteSystem})
	if err != nil {
		return Screen{}, err
	}
	toolsButton, err := ui.stateButton(owner, "Tools", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.ToolInventoryRead})
	if err != nil {
		return Screen{}, err
	}
	updateCheck, err := ui.stateButton(owner, "Check update", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.UpdateCheck})
	if err != nil {
		return Screen{}, err
	}
	updateApply, err := ui.stateButton(owner, "Apply update", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.UpdateApply, Input: application.UpdateApplyOptions{}, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	installCurrent, err := ui.stateButton(owner, "Install current", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.InstallRun, Input: application.InstallCurrentOptions{}, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	restart, err := ui.stateButton(owner, "Restart", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.RuntimeRestart, Input: application.RuntimeActionInput{}, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	down, err := ui.stateButton(owner, "Stop", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.RuntimeDown, Input: application.RuntimeActionInput{}, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	down.Role = ButtonRoleDestructive
	up, err := ui.stateButton(owner, "Start", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.RuntimeUp, Input: application.RuntimeActionInput{}, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	up.Role = ButtonRolePositive
	back, _ := ui.backButton(owner, RouteHome)
	home, _ := ui.homeButton(owner)
	runtimeAction := restart
	if !about.RuntimeRunning {
		runtimeAction = up
	}
	rows := [][]string{{"Version", about.Version}, {"Commit", compactPresentationValue(about.Commit)}, {"Runtime", boolState(about.RuntimeRunning)}, {"Install method", string(about.InstallMethod)}, {"Server uptime", about.ServerUptime.String()}}
	if about.MachineUptimeOK {
		rows = append(rows, []string{"Machine uptime", about.MachineUptime.String()})
	}
	destructive := []Button{}
	if about.RuntimeRunning {
		destructive = append(destructive, down)
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "System", Text: "Canonical runtime and diagnostic administration"},
		RichBlock{Kind: RichTable, Rows: rows},
		RichBlock{Kind: RichDetails, Title: "Remote-control boundary", Text: "Operations that require local elevation or another owner return an explicit external command or unavailable state. Telegram does not emulate them with shell execution."},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{doctorButton, updateCheck, runtimeAction}, Secondary: []Button{toolsButton, updateApply, installCurrent}, Destructive: destructive, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) doctorScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.DoctorRead, nil)
	if err != nil {
		return Screen{}, err
	}
	report, ok := value.(doctor.Report)
	if !ok {
		return Screen{}, errors.New("doctor returned an unexpected result")
	}
	if state.Detail {
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			return Screen{}, marshalErr
		}
		if len(data) > 512<<10 {
			return Screen{}, errors.New("doctor report is too large for Telegram document export")
		}
		if err := ui.runtime.SendDocumentToTopic(ctx, owner.ChatID, TopicRuntime, DocumentUpload{FileName: "codemcp-doctor.json", ContentType: "application/json", Data: data, Caption: "CodeMCP canonical doctor report", ProtectContent: true}); err != nil {
			return Screen{}, err
		}
	}
	items := make([]string, 0, len(report.Components))
	for _, component := range report.Components {
		items = append(items, fmt.Sprintf("%s — %s · %s", component.ID, component.State, compactPresentationValue(component.Summary)))
	}
	export, err := ui.stateButton(owner, "Export JSON", CallbackOpen, ActionState{Route: RouteDoctor, Back: RouteSystem, Detail: true})
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteDoctor, Back: RouteSystem})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteSystem)
	home, _ := ui.homeButton(owner)
	blocks := []RichBlock{{Kind: RichHeading, Title: "Doctor", Text: fmt.Sprintf("healthy=%t · warnings=%d · errors=%d · provider failures=%d", report.Healthy, report.Warnings, report.Errors, report.ProviderFailures)}, {Kind: RichList, Items: items}}
	if state.Detail {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Export", Text: "The bounded canonical doctor report was sent as a protected JSON document."})
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Secondary: []Button{export}, Navigation: []Button{back, home, refresh}})}, nil
}

func (ui *Interface) instructionsScreen(ctx context.Context, owner ViewOwner) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.InstructionSettingsRead, nil)
	if err != nil {
		return Screen{}, err
	}
	settings, ok := value.(application.InstructionSettings)
	if !ok {
		return Screen{}, errors.New("instruction settings returned an unexpected result")
	}
	sources := make([]string, 0, len(settings.DetectedSources))
	for _, source := range settings.DetectedSources {
		sources = append(sources, fmt.Sprintf("%s/%s — %s · count=%d · enabled=%t · loaded=%t", source.Provider, source.Kind, source.Scope, source.Count, source.Enabled, source.Loaded))
	}
	if len(sources) == 0 {
		sources = append(sources, "No provider-native sources detected")
	}
	projectContext, err := ui.stateButton(owner, "Project context", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteInstructions, Operation: capability.ProjectContextRead, InputKind: inputProjectContext})
	if err != nil {
		return Screen{}, err
	}
	prompts, err := ui.stateButton(owner, "Prompts", CallbackOpen, ActionState{Route: RoutePrompts, Back: RouteInstructions})
	if err != nil {
		return Screen{}, err
	}
	edit, err := ui.stateButton(owner, "Edit settings", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteInstructions, Operation: capability.InstructionSettingsWrite, InputKind: inputInstructionPatch, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteHome)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Instructions", Text: "Canonical instruction policy, project context and prompts"},
		RichBlock{Kind: RichTable, Rows: [][]string{{"Version", fmt.Sprint(settings.Version)}, {"Global context bytes", fmt.Sprint(len([]byte(settings.Context)))}, {"Global rules", fmt.Sprint(len(settings.Rules))}, {"Source policies", fmt.Sprint(len(settings.SourcePolicy))}}},
		RichBlock{Kind: RichList, Items: sources},
		RichBlock{Kind: RichDetails, Title: "Native authoring", Text: "Provider-native source trees are read-only provenance here. Rule/skill mutation is not exposed because no operator-owned remote capability currently authorizes Telegram to invoke the native authoring service; Telegram never writes those files directly."},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{projectContext, prompts}, Secondary: []Button{edit, {Text: "Author rule (unavailable)", Disabled: true, Role: ButtonRoleNeutral}, {Text: "Author skill (unavailable)", Disabled: true, Role: ButtonRoleNeutral}}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) promptsScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	workspaceID := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.PromptList, application.PromptListInput{WorkspaceID: workspaceID})
	if err != nil {
		return Screen{}, err
	}
	items, ok := value.([]instructioncontext.ScopedPrompt)
	if !ok {
		return Screen{}, errors.New("prompt list returned an unexpected result")
	}
	return ui.promptListScreen(owner, items, workspaceID)
}

func (ui *Interface) promptListScreen(owner ViewOwner, items []instructioncontext.ScopedPrompt, workspaceID string) (Screen, error) {
	list := make([]string, 0, len(items))
	buttons := make([]Button, 0, len(items))
	for _, item := range items {
		detail := strings.TrimSpace(item.Definition.Description)
		if detail == "" {
			detail = fmt.Sprintf("%d message(s)", len(item.Definition.Messages))
		}
		list = append(list, item.Definition.Name+" — "+compactPresentationValue(detail))
		button, err := ui.stateButton(owner, item.Definition.Name, CallbackOpen, ActionState{Route: RoutePrompt, Back: RoutePrompts, ResourceID: item.Definition.Name, ExpectedVersion: workspaceID})
		if err != nil {
			return Screen{}, err
		}
		buttons = append(buttons, button)
	}
	workspace, err := ui.stateButton(owner, "Workspace", CallbackOpen, ActionState{Route: RouteOperation, Back: RoutePrompts, Operation: capability.PromptList, InputKind: inputPromptWorkspace})
	if err != nil {
		return Screen{}, err
	}
	global, err := ui.stateButton(owner, "Global", CallbackOpen, ActionState{Route: RoutePrompts, Back: RouteInstructions})
	if err != nil {
		return Screen{}, err
	}
	create, err := ui.stateButton(owner, "Create", CallbackOpen, ActionState{Route: RouteOperation, Back: RoutePrompts, Operation: capability.PromptCreate, InputKind: inputPromptCreate, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteInstructions)
	home, _ := ui.homeButton(owner)
	title := "Global prompts"
	if workspaceID != "" {
		title = "Workspace prompts"
	}
	return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: title, Text: fmt.Sprintf("%d prompt(s)", len(items))}, RichBlock{Kind: RichList, Items: list}), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{workspace, global, create}, Secondary: buttons, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) promptScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	name := strings.TrimSpace(state.ResourceID)
	workspaceID := strings.TrimSpace(state.ExpectedVersion)
	value, err := ui.dispatch(ctx, capability.PromptGet, application.PromptGetInput{WorkspaceID: workspaceID, Name: name})
	if err != nil {
		return Screen{}, err
	}
	prompt, ok := value.(instructioncontext.ScopedPrompt)
	if !ok {
		return Screen{}, errors.New("prompt detail returned an unexpected result")
	}
	update, err := ui.stateButton(owner, "Update", CallbackOpen, ActionState{Route: RouteOperation, Back: RoutePrompts, Operation: capability.PromptUpdate, ResourceID: name, ExpectedVersion: workspaceID, InputKind: inputPromptUpdate, ForceConfirm: true})
	if err != nil {
		return Screen{}, err
	}
	scope := instructioncontext.PromptScopeGlobal
	if workspaceID != "" {
		scope = instructioncontext.PromptScopeWorkspace
	}
	remove, err := ui.stateButton(owner, "Delete", CallbackOpen, ActionState{Route: RouteOperation, Back: RoutePrompts, Operation: capability.PromptDelete, ForceConfirm: true, Input: application.PromptDeleteRequest{Scope: scope, WorkspaceID: workspaceID, Name: name}})
	if err != nil {
		return Screen{}, err
	}
	remove.Role = ButtonRoleDestructive
	back, _ := ui.backButton(owner, RoutePrompts)
	home, _ := ui.homeButton(owner)
	return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: prompt.Definition.Name, Text: prompt.Definition.Description}, RichBlock{Kind: RichTable, Rows: [][]string{{"Scope", string(prompt.Scope)}, {"Version", fmt.Sprint(prompt.Definition.Version)}, {"Arguments", fmt.Sprint(len(prompt.Definition.Arguments))}, {"Messages", fmt.Sprint(len(prompt.Definition.Messages))}}}), Keyboard: BoundedActionGroups(ActionGroups{Secondary: []Button{update}, Destructive: []Button{remove}, Navigation: []Button{back, home}})}, nil
}

func systemInputPrompt(state ActionState) (title, prompt, placeholder string) {
	switch state.InputKind {
	case inputInstructionPatch:
		return "Edit instruction settings", "Reply with a canonical InstructionSettingsPatch JSON object. Provider-native trees remain read-only.", "Instruction settings JSON"
	case inputProjectContext:
		return "Project context", "Reply with the registered workspace ID to inspect its canonical project context.", "ws_..."
	case inputPromptWorkspace:
		return "Workspace prompts", "Reply with the registered workspace ID whose prompt inventory you want to inspect.", "ws_..."
	case inputPromptCreate:
		return "Create prompt", "Reply with a canonical PromptWriteRequest JSON object. Global mutation remains subject to the application owner.", "Prompt request JSON"
	case inputPromptUpdate:
		return "Update prompt", "Reply with a canonical PromptDefinition JSON object. The selected scope and name stay bound to this prompt.", "Prompt definition JSON"
	default:
		return "", "", ""
	}
}

func systemActionInput(state ActionState, text string) (any, bool, error) {
	switch state.InputKind {
	case inputInstructionPatch:
		var patch application.InstructionSettingsPatch
		if err := json.Unmarshal([]byte(text), &patch); err != nil {
			return nil, true, fmt.Errorf("invalid instruction settings JSON: %w", err)
		}
		return patch, true, nil
	case inputProjectContext:
		options := projectcontext.DefaultOptions()
		options.IncludeGit = true
		options.IncludeSkills = true
		return application.ProjectContextInput{WorkspaceID: strings.TrimSpace(text), Options: options}, true, nil
	case inputPromptWorkspace:
		return application.PromptListInput{WorkspaceID: strings.TrimSpace(text)}, true, nil
	case inputPromptCreate:
		var request application.PromptWriteRequest
		if err := json.Unmarshal([]byte(text), &request); err != nil {
			return nil, true, fmt.Errorf("invalid prompt request JSON: %w", err)
		}
		request.Mode = "create"
		return request, true, nil
	case inputPromptUpdate:
		var definition instructioncontext.PromptDefinition
		if err := json.Unmarshal([]byte(text), &definition); err != nil {
			return nil, true, fmt.Errorf("invalid prompt definition JSON: %w", err)
		}
		if strings.TrimSpace(definition.Name) != strings.TrimSpace(state.ResourceID) {
			return nil, true, errors.New("prompt name cannot change during update")
		}
		scope := instructioncontext.PromptScopeGlobal
		if strings.TrimSpace(state.ExpectedVersion) != "" {
			scope = instructioncontext.PromptScopeWorkspace
		}
		return application.PromptWriteRequest{Scope: scope, WorkspaceID: state.ExpectedVersion, Mode: "update", Definition: definition}, true, nil
	default:
		return nil, false, nil
	}
}

func (ui *Interface) systemOperationResultScreen(ctx context.Context, owner ViewOwner, state ActionState, value any) (Screen, bool, error) {
	backRoute := state.Back
	if backRoute == "" {
		backRoute = RouteSystem
	}
	back, err := ui.backButton(owner, backRoute)
	if err != nil {
		return Screen{}, true, err
	}
	home, _ := ui.homeButton(owner)
	switch result := value.(type) {
	case application.AboutInfo:
		screen, err := ui.systemScreen(ctx, owner)
		return screen, true, err
	case application.RuntimeActionResult:
		blocks := []RichBlock{{Kind: RichHeading, Title: "Runtime action", Text: "Canonical runtime lifecycle result"}, {Kind: RichTable, Rows: [][]string{{"Action", result.Action}, {"Scope", string(result.Scope)}, {"Changed", fmt.Sprint(result.Changed)}, {"Service", result.Service.ID}, {"Backend", result.Service.Backend}, {"Running", fmt.Sprint(result.Service.Running)}}}}
		if result.External != nil {
			blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "External action required", Text: result.External.Reason}, RichBlock{Kind: RichCode, Title: "Command", Text: result.External.Command})
		}
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case updatepkg.CheckResult:
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Update check", Text: string(result.Status)}, RichBlock{Kind: RichTable, Rows: [][]string{{"Current", result.Current}, {"Latest", result.Latest}}}), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case application.UpdateApplyResult:
		blocks := []RichBlock{{Kind: RichHeading, Title: "Update", Text: result.Notice}, {Kind: RichTable, Rows: [][]string{{"Changed", fmt.Sprint(result.Result.Changed)}, {"Current", result.Result.Current}, {"Target", result.Result.Target}}}}
		if result.External != nil {
			blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "External action required", Text: result.External.Reason}, RichBlock{Kind: RichCode, Text: result.External.Command})
		}
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case install.Result:
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: "Install", Text: "Canonical installer completed"}, RichBlock{Kind: RichTable, Rows: [][]string{{"Version", result.Version}, {"Already installed", fmt.Sprint(result.AlreadyInstalled)}}}), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case []tools.Schema:
		items := make([]string, 0, len(result))
		for _, schema := range result {
			label := schema.Name
			if strings.TrimSpace(schema.Title) != "" {
				label += " — " + schema.Title
			}
			items = append(items, label)
		}
		start, end, page, pages := PageBounds(len(items), state.Page, richMaxRows)
		keyboard, err := ui.paginationKeyboard(owner, state, len(items), richMaxRows)
		if err != nil {
			return Screen{}, true, err
		}
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Tool inventory", Text: fmt.Sprintf("%d tool(s) · %s", len(result), PaginationLabel(page, pages))},
			RichBlock{Kind: RichList, Items: items[start:end]},
		), Keyboard: keyboard}, true, nil
	case application.InstructionSettings:
		screen, err := ui.instructionsScreen(ctx, owner)
		return screen, true, err
	case projectcontext.Result:
		rules := make([]string, 0, len(result.InstructionContext.Rules))
		for _, rule := range result.InstructionContext.Rules {
			rules = append(rules, filepath.Base(rule.Path)+" — "+rule.Source)
		}
		skills := make([]string, 0, len(result.InstructionContext.Skills))
		for _, skill := range result.InstructionContext.Skills {
			skills = append(skills, skill.Name+" — "+skill.Source)
		}
		diagnostics := make([]string, 0, len(result.InstructionContext.IntegrationDiagnostics))
		for _, diagnostic := range result.InstructionContext.IntegrationDiagnostics {
			diagnostics = append(diagnostics, diagnostic.ID+" — "+diagnostic.State+" · "+compactPresentationValue(diagnostic.Message))
		}
		blocks := []RichBlock{{Kind: RichHeading, Title: "Project context", Text: result.WorkspaceID}, {Kind: RichTable, Rows: [][]string{{"Root", result.Root}, {"Instruction bytes", fmt.Sprint(result.Summary.InstructionBytes)}, {"Memory bytes", fmt.Sprint(result.Summary.MemoryBytes)}, {"Rules", fmt.Sprint(result.Summary.Rules)}, {"Skills", fmt.Sprint(result.Summary.Skills)}}}}
		if len(rules) > 0 {
			blocks = append(blocks, RichBlock{Kind: RichList, Title: "Rules", Items: rules})
		}
		if len(skills) > 0 {
			blocks = append(blocks, RichBlock{Kind: RichList, Title: "Skills", Items: skills})
		}
		if len(diagnostics) > 0 {
			blocks = append(blocks, RichBlock{Kind: RichList, Title: "Integration diagnostics", Items: diagnostics})
		}
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Source policy", Text: "Provider-native rule and skill sources are provenance only. Telegram exposes no writer for those trees."})
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case []instructioncontext.ScopedPrompt:
		workspaceID := ""
		if input, ok := state.Input.(application.PromptListInput); ok {
			workspaceID = input.WorkspaceID
		}
		screen, err := ui.promptListScreen(owner, result, workspaceID)
		return screen, true, err
	case instructioncontext.ScopedPrompt:
		return Screen{Rich: BuildRichPresentation(RichBlock{Kind: RichHeading, Title: result.Definition.Name, Text: "Prompt mutation completed"}, RichBlock{Kind: RichTable, Rows: [][]string{{"Scope", string(result.Scope)}, {"Messages", fmt.Sprint(len(result.Definition.Messages))}}}), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	default:
		return Screen{}, false, nil
	}
}
