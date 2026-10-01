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
	"go.mewis.me/codemcp/internal/instructionpolicy"
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
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteSystem, Back: RouteHome})
	_, _ = ui.runtime.SendRichMessageToTopic(ctx, owner.ChatID, TopicRuntime, screen, RichMessageOptions{})
}

func (ui *Interface) handleInstructions(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteInstructions, Back: RouteHome})
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
	rows := [][]string{{"Version", about.Version}, {"Commit", compactPresentationValue(about.Commit)}, {"Install method", string(about.InstallMethod)}, {"Server uptime", about.ServerUptime.String()}}
	if about.MachineUptimeOK {
		rows = append(rows, []string{"Machine uptime", about.MachineUptime.String()})
	}
	destructive := []Button{}
	if about.RuntimeRunning {
		destructive = append(destructive, down)
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "System", Text: "Runtime, updates, diagnostics, and tools"},
		StateBlock(stateTone(about.RuntimeRunning), stateLabel(about.RuntimeRunning, "Runtime running", "Runtime stopped"), ""),
		RichBlock{Kind: RichFields, Title: "Build and uptime", Rows: rows},
		RichBlock{Kind: RichDetails, Title: "Local-only actions", Text: "Actions that require local elevation or another owner return a command to run locally instead of executing it through Telegram."},
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
		if err := ui.runtime.SendDocumentToTopic(ctx, owner.ChatID, TopicRuntime, DocumentUpload{FileName: "codemcp-doctor.json", ContentType: "application/json", Data: data, Caption: "CodeMCP doctor report", ProtectContent: true}); err != nil {
			return Screen{}, err
		}
	}
	items := make([]string, 0, len(report.Components))
	for _, component := range report.Components {
		items = append(items, fmt.Sprintf("%s\n%s · %s", component.ID, displayState(string(component.State)), compactPresentationValue(component.Summary)))
	}
	export, err := ui.stateButton(owner, "Export JSON", CallbackOpen, ActionState{Route: RouteDoctor, Back: RouteSystem, Detail: true})
	if err != nil {
		return Screen{}, err
	}
	health, err := ui.stateButton(owner, "Health", CallbackOpen, ActionState{Route: RouteOperation, Back: RouteDoctor, Operation: capability.HealthRead})
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteDoctor, Back: RouteSystem})
	if err != nil {
		return Screen{}, err
	}
	back, _ := ui.backButton(owner, RouteSystem)
	home, _ := ui.homeButton(owner)
	doctorState := "Needs attention"
	tone := ToneWarning
	if report.Healthy {
		doctorState, tone = "Healthy", ToneHealthy
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: "Doctor"},
		StateBlock(tone, doctorState, ""),
		FieldsBlock("Summary",
			[]string{"Warnings", fmt.Sprint(report.Warnings)},
			[]string{"Errors", fmt.Sprint(report.Errors)},
			[]string{"Provider failures", fmt.Sprint(report.ProviderFailures)},
		),
		{Kind: RichList, Title: "Components", Items: items},
	}
	if state.Detail {
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Export", Text: "The doctor report was sent as a protected JSON document."})
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Secondary: []Button{export, health}, Navigation: []Button{back, home, refresh}})}, nil
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
		sources = append(sources, fmt.Sprintf("%s/%s\n%s · %d item(s) · %s · %s",
			source.Provider, source.Kind, displayState(source.Scope), source.Count,
			stateLabel(source.Enabled, "Enabled", "Disabled"),
			stateLabel(source.Loaded, "Loaded", "Not loaded"),
		))
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
		RichBlock{Kind: RichHeading, Title: "Instructions", Text: "Instruction settings, project context, and prompts"},
		FieldsBlock("Overview", []string{"Version", fmt.Sprint(settings.Version)}, []string{"Global context bytes", fmt.Sprint(len([]byte(settings.Context)))}, []string{"Global rules", fmt.Sprint(len(settings.Rules))}, []string{"Source policies", fmt.Sprint(len(settings.SourcePolicy))}),
		RichBlock{Kind: RichList, Items: sources},
		RichBlock{Kind: RichDetails, Title: "Native authoring", Text: "Provider-native sources are read-only here. Telegram does not edit provider rule or skill files."},
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
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: prompt.Definition.Name, Text: prompt.Definition.Description},
		FieldsBlock("Prompt",
			[]string{"Scope", string(prompt.Scope)},
			[]string{"Version", fmt.Sprint(prompt.Definition.Version)},
			[]string{"Arguments", fmt.Sprint(len(prompt.Definition.Arguments))},
			[]string{"Messages", fmt.Sprint(len(prompt.Definition.Messages))},
		),
	), Keyboard: BoundedActionGroups(ActionGroups{Secondary: []Button{update}, Destructive: []Button{remove}, Navigation: []Button{back, home}})}, nil
}

func systemInputPrompt(state ActionState) (title, prompt, placeholder string) {
	switch state.InputKind {
	case inputProjectContext:
		return "Project context", "Reply with the registered workspace ID to inspect its project context.", "ws_..."
	case inputPromptWorkspace:
		return "Workspace prompts", "Reply with the registered workspace ID whose prompt inventory you want to inspect.", "ws_..."
	default:
		return "", "", ""
	}
}

func (ui *Interface) systemInputFlow(ctx context.Context, state ActionState) (inputFlowDescriptor, bool, error) {
	switch state.InputKind {
	case inputInstructionPatch:
		value, err := ui.dispatch(ctx, capability.InstructionSettingsRead, nil)
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		settings, ok := value.(application.InstructionSettings)
		if !ok {
			return inputFlowDescriptor{}, true, errors.New("instruction settings returned an unexpected result")
		}
		rulesJSON, _ := json.MarshalIndent(settings.Rules, "", "  ")
		policyJSON, _ := json.MarshalIndent(settings.SourcePolicy, "", "  ")
		fields := []inputFlowField{
			{
				Key: "context", Label: "Global context", Description: "Global instruction context included with project-specific instructions.",
				Kind: inputFlowMultiline, HasDefault: true, Default: settings.Context, CanClear: true,
			},
			{
				Key: "rules", Label: "Global rules", Description: "JSON array of global rule objects. Each rule contains id, optional name, enabled, and content.",
				Kind: inputFlowJSON, HasDefault: true, Default: string(rulesJSON), CanClear: true,
				Example:  `[{"id":"safe","enabled":true,"content":"Keep changes scoped."}]`,
				Validate: inputFlowJSONValidator(func() any { return &[]instructionpolicy.GlobalRule{} }),
			},
			{
				Key: "source_policy", Label: "Source policy", Description: "JSON object keyed by provider/source name. Each entry may control enabled, context, rules, and skills.",
				Kind: inputFlowJSON, HasDefault: true, Default: string(policyJSON), CanClear: true,
				Example:  `{"claude":{"enabled":true,"rules":true}}`,
				Validate: inputFlowJSONValidator(func() any { return &map[string]instructionpolicy.SourcePolicy{} }),
			},
		}
		return inputFlowDescriptor{
			Title: "Edit instruction settings", Description: "Update global instruction context, rules, and source policy.", SubmitLabel: "Save instructions",
			Fields: fields,
			Build: func(data inputFlowData) (any, error) {
				patch := application.InstructionSettingsPatch{}
				changed := false
				if raw, explicit := data.Explicit("context"); explicit {
					value := raw
					patch.Context, changed = &value, true
				}
				if raw, explicit := data.Explicit("rules"); explicit {
					var rules []instructionpolicy.GlobalRule
					if strings.TrimSpace(raw) != "" {
						if err := json.Unmarshal([]byte(raw), &rules); err != nil {
							return nil, fmt.Errorf("invalid rules JSON: %w", err)
						}
					}
					patch.Rules, changed = &rules, true
				}
				if raw, explicit := data.Explicit("source_policy"); explicit {
					policy := map[string]instructionpolicy.SourcePolicy{}
					if strings.TrimSpace(raw) != "" {
						if err := json.Unmarshal([]byte(raw), &policy); err != nil {
							return nil, fmt.Errorf("invalid source policy JSON: %w", err)
						}
					}
					patch.SourcePolicy, changed = policy, true
				}
				if !changed {
					return nil, errors.New("no instruction changes selected")
				}
				return patch, nil
			},
		}, true, nil
	case inputPromptCreate:
		return promptCreateInputFlow(), true, nil
	case inputPromptUpdate:
		value, err := ui.dispatch(ctx, capability.PromptGet, application.PromptGetInput{WorkspaceID: state.ExpectedVersion, Name: state.ResourceID})
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		prompt, ok := value.(instructioncontext.ScopedPrompt)
		if !ok {
			return inputFlowDescriptor{}, true, errors.New("prompt view returned an unexpected result")
		}
		return promptUpdateInputFlow(state, prompt), true, nil
	default:
		return inputFlowDescriptor{}, false, nil
	}
}

func promptCreateInputFlow() inputFlowDescriptor {
	scope := inputFlowField{
		Key: "scope", Label: "Scope", Description: "Choose where this prompt is stored.", Kind: inputFlowEnum, Required: true,
		HasDefault: true, Default: string(instructioncontext.PromptScopeGlobal),
		Options: []inputFlowOption{{Label: "Global", Value: string(instructioncontext.PromptScopeGlobal)}, {Label: "Workspace", Value: string(instructioncontext.PromptScopeWorkspace)}},
	}
	workspace := inputFlowField{
		Key: "workspace_id", Label: "Workspace ID", Description: "Registered workspace that owns this prompt.", Kind: inputFlowText, Required: true,
		Placeholder: "ws_...", Example: "ws_...", When: func(data inputFlowData) bool {
			return data.Value("scope") == string(instructioncontext.PromptScopeWorkspace)
		},
	}
	name := inputFlowField{Key: "name", Label: "Prompt name", Description: "Stable prompt name used when invoking the prompt.", Kind: inputFlowText, Required: true, Placeholder: "review-code", Example: "review-code"}
	description := inputFlowField{Key: "description", Label: "Description", Description: "Optional human-readable description.", Kind: inputFlowText, Placeholder: "Review code changes"}
	arguments := inputFlowField{
		Key: "arguments", Label: "Arguments", Description: "Optional JSON array of prompt arguments. Each argument accepts name, optional description, and required.",
		Kind: inputFlowJSON, CanClear: true, Example: `[{"name":"path","description":"Path to review","required":true}]`,
		Validate: inputFlowJSONValidator(func() any { return &[]instructioncontext.PromptArgument{} }),
	}
	messages := inputFlowField{
		Key: "messages", Label: "Messages", Description: "JSON array of prompt messages. Each message has role and text content.", Kind: inputFlowJSON, Required: true,
		Example:  `[{"role":"user","content":{"type":"text","text":"Review {{path}}"}}]`,
		Validate: inputFlowJSONValidator(func() any { return &[]instructioncontext.PromptMessage{} }),
	}
	return inputFlowDescriptor{
		Title: "Create prompt", Description: "Create a reusable prompt definition.", SubmitLabel: "Create prompt",
		Fields: []inputFlowField{scope, workspace, name, description, arguments, messages},
		Build: func(data inputFlowData) (any, error) {
			var argumentsValue []instructioncontext.PromptArgument
			if raw := strings.TrimSpace(data.Value("arguments")); raw != "" {
				if err := json.Unmarshal([]byte(raw), &argumentsValue); err != nil {
					return nil, err
				}
			}
			var messagesValue []instructioncontext.PromptMessage
			if err := json.Unmarshal([]byte(data.Value("messages")), &messagesValue); err != nil {
				return nil, err
			}
			definition := instructioncontext.PromptDefinition{
				Version: instructioncontext.PromptDefinitionVersion, Name: data.Value("name"), Description: data.Value("description"),
				Arguments: argumentsValue, Messages: messagesValue,
			}
			if err := instructioncontext.ValidatePromptDefinition(definition); err != nil {
				return nil, err
			}
			scopeValue := instructioncontext.PromptScope(data.Value("scope"))
			return application.PromptWriteRequest{Scope: scopeValue, WorkspaceID: data.Value("workspace_id"), Mode: "create", Definition: definition}, nil
		},
	}
}

func promptUpdateInputFlow(state ActionState, prompt instructioncontext.ScopedPrompt) inputFlowDescriptor {
	argumentsJSON, _ := json.MarshalIndent(prompt.Definition.Arguments, "", "  ")
	messagesJSON, _ := json.MarshalIndent(prompt.Definition.Messages, "", "  ")
	description := inputFlowField{Key: "description", Label: "Description", Description: "Human-readable prompt description.", Kind: inputFlowText, HasDefault: true, Default: prompt.Definition.Description, CanClear: true}
	arguments := inputFlowField{
		Key: "arguments", Label: "Arguments", Description: "JSON array of prompt arguments. Each argument accepts name, optional description, and required.",
		Kind: inputFlowJSON, HasDefault: true, Default: string(argumentsJSON), CanClear: true,
		Validate: inputFlowJSONValidator(func() any { return &[]instructioncontext.PromptArgument{} }),
	}
	messages := inputFlowField{
		Key: "messages", Label: "Messages", Description: "JSON array of prompt messages. Each message has role and text content.", Kind: inputFlowJSON, Required: true,
		HasDefault: true, Default: string(messagesJSON),
		Validate: inputFlowJSONValidator(func() any { return &[]instructioncontext.PromptMessage{} }),
	}
	return inputFlowDescriptor{
		Title: "Update prompt", Description: prompt.Definition.Name + " · " + string(prompt.Scope), SubmitLabel: "Save prompt",
		Fields: []inputFlowField{description, arguments, messages},
		Build: func(data inputFlowData) (any, error) {
			var argumentsValue []instructioncontext.PromptArgument
			if raw := strings.TrimSpace(data.Value("arguments")); raw != "" {
				if err := json.Unmarshal([]byte(raw), &argumentsValue); err != nil {
					return nil, err
				}
			}
			var messagesValue []instructioncontext.PromptMessage
			if err := json.Unmarshal([]byte(data.Value("messages")), &messagesValue); err != nil {
				return nil, err
			}
			definition := prompt.Definition
			definition.Description, definition.Arguments, definition.Messages = data.Value("description"), argumentsValue, messagesValue
			if err := instructioncontext.ValidatePromptDefinition(definition); err != nil {
				return nil, err
			}
			return application.PromptWriteRequest{Scope: prompt.Scope, WorkspaceID: state.ExpectedVersion, Mode: "update", Definition: definition}, nil
		},
	}
}

func systemActionInput(state ActionState, text string) (any, bool, error) {
	switch state.InputKind {
	case inputProjectContext:
		options := projectcontext.DefaultOptions()
		options.IncludeGit = true
		options.IncludeSkills = true
		return application.ProjectContextInput{WorkspaceID: strings.TrimSpace(text), Options: options}, true, nil
	case inputPromptWorkspace:
		return application.PromptListInput{WorkspaceID: strings.TrimSpace(text)}, true, nil
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
		blocks := []RichBlock{
			{Kind: RichHeading, Title: displayState(result.Action)},
			StateBlock(stateTone(result.Service.Running), stateLabel(result.Service.Running, "Runtime running", "Runtime stopped"), ""),
			FieldsBlock("Service",
				[]string{"Scope", string(result.Scope)},
				[]string{"Result", stateLabel(result.Changed, "Changed", "No change")},
				[]string{"Service", result.Service.ID},
				[]string{"Backend", result.Service.Backend},
			),
		}
		if result.External != nil {
			blocks = append(blocks, NoticeBlock(ToneWarning, "Local action required", result.External.Reason), RichBlock{Kind: RichCode, Title: "Command", Text: result.External.Command})
		}
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case updatepkg.CheckResult:
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Update check"},
			StateBlock(statusTone(string(result.Status)), displayState(string(result.Status)), ""),
			FieldsBlock("Versions", []string{"Current", result.Current}, []string{"Latest", result.Latest}),
		), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case application.UpdateApplyResult:
		blocks := []RichBlock{
			{Kind: RichHeading, Title: "Update", Text: result.Notice},
			StateBlock(ToneSuccess, stateLabel(result.Result.Changed, "Updated", "Already up to date"), ""),
			FieldsBlock("Versions", []string{"Current", result.Result.Current}, []string{"Target", result.Result.Target}),
		}
		if result.External != nil {
			blocks = append(blocks, NoticeBlock(ToneWarning, "Local action required", result.External.Reason), RichBlock{Kind: RichCode, Text: result.External.Command})
		}
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case install.Result:
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Install completed"},
			StateBlock(ToneSuccess, stateLabel(result.AlreadyInstalled, "Already installed", "Installed"), ""),
			FieldsBlock("Result", []string{"Version", result.Version}),
		), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case []tools.Schema:
		items := make([]string, 0, len(result))
		for _, schema := range result {
			label := schema.Name
			if strings.TrimSpace(schema.Title) != "" {
				label += " — " + schema.Title
			}
			items = append(items, label)
		}
		start, end, page, pages := PageBounds(len(items), state.Page, richPageSize)
		keyboard, err := ui.paginationKeyboard(owner, state, len(items), richPageSize)
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
		blocks := []RichBlock{{Kind: RichHeading, Title: "Project context", Text: result.WorkspaceID}, FieldsBlock("Overview", []string{"Root", result.Root}, []string{"Instruction bytes", fmt.Sprint(result.Summary.InstructionBytes)}, []string{"Memory bytes", fmt.Sprint(result.Summary.MemoryBytes)}, []string{"Rules", fmt.Sprint(result.Summary.Rules)}, []string{"Skills", fmt.Sprint(result.Summary.Skills)})}
		if len(rules) > 0 {
			blocks = append(blocks, RichBlock{Kind: RichList, Title: "Rules", Items: rules})
		}
		if len(skills) > 0 {
			blocks = append(blocks, RichBlock{Kind: RichList, Title: "Skills", Items: skills})
		}
		if len(diagnostics) > 0 {
			blocks = append(blocks, RichBlock{Kind: RichList, Title: "Integration diagnostics", Items: diagnostics})
		}
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Source policy", Text: "Provider-native rule and skill sources are read-only here. Telegram does not edit those files."})
		return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	case []instructioncontext.ScopedPrompt:
		workspaceID := ""
		if input, ok := state.Input.(application.PromptListInput); ok {
			workspaceID = input.WorkspaceID
		}
		screen, err := ui.promptListScreen(owner, result, workspaceID)
		return screen, true, err
	case instructioncontext.ScopedPrompt:
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: result.Definition.Name},
			StateBlock(ToneSuccess, "Prompt updated", ""),
			FieldsBlock("Prompt", []string{"Scope", string(result.Scope)}, []string{"Messages", fmt.Sprint(len(result.Definition.Messages))}),
		), Keyboard: BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})}, true, nil
	default:
		return Screen{}, false, nil
	}
}
