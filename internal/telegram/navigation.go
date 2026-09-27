package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

type Route string

const (
	RouteHome       Route = "home"
	RouteStatus     Route = "status"
	RouteCommands   Route = "commands"
	RouteSettings   Route = "settings"
	RouteAuth       Route = "auth"
	RouteWorkspaces Route = "workspaces"
	RouteWorkspace  Route = "workspace"
	RouteAccess     Route = "workspace.access"
	RouteContainers Route = "containers"
	RouteContainer  Route = "container"
	RouteRequests   Route = "requests"
	RouteRequest    Route = "request"
	RouteOperation  Route = "operation"
)

type ActionState struct {
	Route           Route
	Back            Route
	Operation       capability.ID
	Input           any
	ResourceID      string
	ExpectedVersion string
	Page            int
	Detail          bool
	Confirmed       bool
	InputKind       string
}

type VersionResolver func(context.Context, capability.ID, string) (string, error)

type InterfaceOptions struct {
	Runtime         *Runtime
	Dispatcher      application.OperationDispatcher
	VersionResolver VersionResolver
	StateTTLSeconds int
}

type Interface struct {
	runtime    *Runtime
	dispatcher application.OperationDispatcher
	versions   VersionResolver
	states     *ViewStateStore
	inputs     *InputStore
	callbacks  *CallbackCodec
	router     *Router
}

func NewInterface(options InterfaceOptions) (*Interface, error) {
	if options.Runtime == nil {
		return nil, errors.New("telegram runtime is required")
	}
	codec, err := NewCallbackCodec(nil)
	if err != nil {
		return nil, err
	}
	stateTTL := defaultViewStateTTL
	if options.StateTTLSeconds > 0 {
		stateTTL = time.Duration(options.StateTTLSeconds) * time.Second
	}
	ui := &Interface{
		runtime: options.Runtime, dispatcher: options.Dispatcher, versions: options.VersionResolver,
		states: NewViewStateStore(stateTTL, defaultViewStateMax), inputs: NewInputStore(stateTTL), callbacks: codec, router: NewRouter(),
	}
	handlers := map[Route]RouteHandler{
		RouteHome: ui.handleHome, RouteStatus: ui.handleStatus, RouteCommands: ui.handleCommands,
		RouteWorkspaces: ui.handleWorkspaces, RouteRequests: ui.handleRequests,
	}
	for _, command := range Commands() {
		if handler := handlers[command.Route]; handler != nil {
			ui.router.RegisterCommand(command.Name, handler)
		}
	}
	ui.router.RegisterCallback(ui.handleCallback)
	return ui, nil
}

func (ui *Interface) Handle(ctx context.Context, update Update) {
	if ui == nil || ui.runtime == nil || !ui.runtime.Authorizes(update) {
		return
	}
	if update.Message != nil && messageCommand(update.Message.Text) == "" {
		if ui.handleActionInput(ctx, update) {
			return
		}
		ui.acceptPendingInput(update)
		return
	}
	ui.router.Dispatch(ctx, update)
}

func (ui *Interface) PromptInput(ctx context.Context, owner ViewOwner, screen Screen, placeholder string, secret bool) (int64, error) {
	if ui == nil || ui.runtime == nil || ui.inputs == nil || owner.Generation != ui.runtime.Generation() {
		return 0, errors.New("telegram input workflow is unavailable")
	}
	promptID, err := ui.runtime.SendRichMessage(ctx, owner.ChatID, screen, RichMessageOptions{
		ForceReplyPlaceholder: forceReplyPlaceholder(placeholder),
		ProtectContent:        secret,
	})
	if err != nil {
		return 0, err
	}
	if err := ui.inputs.Put(owner, promptID, secret); err != nil {
		_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, promptID)
		return 0, err
	}
	return promptID, nil
}

func (ui *Interface) acceptPendingInput(update Update) (PendingInput, bool) {
	if ui == nil || ui.runtime == nil || ui.inputs == nil || update.Message == nil {
		return PendingInput{}, false
	}
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return PendingInput{}, false
	}
	return ui.inputs.Match(owner, *update.Message)
}

func (ui *Interface) AcceptInput(update Update) (PendingInput, PendingInputValue, bool) {
	if ui == nil || ui.runtime == nil || ui.inputs == nil || update.Message == nil {
		return PendingInput{}, PendingInputValue{}, false
	}
	value, err := InputValue(*update.Message)
	if err != nil {
		return PendingInput{}, PendingInputValue{}, false
	}
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return PendingInput{}, PendingInputValue{}, false
	}
	state, ok := ui.inputs.Match(owner, *update.Message)
	if !ok {
		return PendingInput{}, PendingInputValue{}, false
	}
	return state, value, true
}

func (ui *Interface) CancelInput(promptMessageID int64) {
	if ui == nil || ui.inputs == nil || promptMessageID <= 0 {
		return
	}
	ui.inputs.Delete(promptMessageID)
}

func (ui *Interface) handleHome(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen, err := ui.homeScreen(owner)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleStatus(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen, err := ui.operationScreen(ctx, owner, ActionState{Route: RouteStatus, Back: RouteHome, Operation: capability.StatusOverview})
	if err != nil {
		screen = ErrorScreen(err)
	}
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleCommands(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen, err := ui.commandsScreen(owner)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleCallback(ctx context.Context, update Update) {
	if update.CallbackQuery == nil || update.CallbackQuery.Message == nil {
		return
	}
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	ref, err := ui.callbacks.Decode(update.CallbackQuery.Data)
	if err != nil {
		ui.answerCallback(ctx, update.CallbackQuery.ID, "This control is invalid.", true)
		return
	}
	value, err := ui.states.Get(ref.Token, owner)
	if err != nil {
		ui.answerCallback(ctx, update.CallbackQuery.ID, "This control is stale. Open the screen again.", true)
		return
	}
	state, ok := value.(ActionState)
	if !ok {
		ui.answerCallback(ctx, update.CallbackQuery.ID, "This control is invalid.", true)
		return
	}
	if ui.callbackStateIsStale(ctx, state) {
		ui.states.Delete(ref.Token)
		ui.answerCallback(ctx, update.CallbackQuery.ID, "This control is stale. Reopen the current resource.", true)
		screen, screenErr := ui.staleStateScreen(owner, state)
		if screenErr != nil {
			screen = StaleScreen()
		}
		_ = ui.runtime.EditScreen(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID, screen)
		return
	}
	if ref.Action == CallbackClose {
		ui.states.Delete(ref.Token)
		ui.answerCallback(ctx, update.CallbackQuery.ID, "", false)
		_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID)
		return
	}
	if ref.Action == CallbackConfirm {
		state.Confirmed = true
	}
	if state.InputKind != "" && state.Input == nil {
		ui.answerCallback(ctx, update.CallbackQuery.ID, "", false)
		if err := ui.beginActionInput(ctx, owner, state); err != nil {
			_ = ui.runtime.EditScreen(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID, ErrorScreen(err))
		}
		return
	}
	var spec capability.Spec
	var hasSpec bool
	if state.Operation != "" {
		spec, hasSpec = capability.Lookup(state.Operation)
		if hasSpec && spec.Kind != capability.KindQuery && spec.Kind != capability.KindStream {
			ui.states.Delete(ref.Token)
		}
	}
	ui.answerCallback(ctx, update.CallbackQuery.ID, "", false)
	if hasSpec && shouldShowWorking(spec, state) {
		_ = ui.runtime.EditScreen(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID, workingScreen(state))
	}
	screen, err := ui.renderState(ctx, owner, state)
	if err != nil {
		screen, _ = ui.operationErrorScreen(owner, state, err)
	}
	_ = ui.runtime.EditScreen(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID, screen)
}

func (ui *Interface) callbackStateIsStale(ctx context.Context, state ActionState) bool {
	if ui == nil || ui.versions == nil || state.Operation == "" || strings.TrimSpace(state.ExpectedVersion) == "" {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	current, err := ui.versions(checkCtx, state.Operation, state.ResourceID)
	if err != nil {
		return false
	}
	return strings.TrimSpace(current) != strings.TrimSpace(state.ExpectedVersion)
}

func (ui *Interface) staleStateScreen(owner ViewOwner, state ActionState) (Screen, error) {
	back, err := ui.backButton(owner, state.Back)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	screen := StaleScreen()
	screen.Keyboard = [][]Button{{back, home}}
	return screen, nil
}

func (ui *Interface) answerCallback(ctx context.Context, callbackID, text string, alert bool) {
	if ui == nil || ui.runtime == nil || strings.TrimSpace(callbackID) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ackCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = ui.runtime.AnswerCallback(ackCtx, callbackID, text, alert)
}

func shouldShowWorking(spec capability.Spec, state ActionState) bool {
	if spec.Kind == capability.KindQuery || spec.Kind == capability.KindStream {
		return false
	}
	return !requiresExplicitConfirmation(spec) || state.Confirmed
}

func (ui *Interface) renderState(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	switch state.Route {
	case RouteHome:
		return ui.homeScreen(owner)
	case RouteCommands:
		return ui.commandsScreen(owner)
	case RouteSettings:
		return ui.settingsScreen(owner)
	case RouteAuth:
		return ui.authScreen(owner)
	case RouteStatus:
		return ui.operationScreen(ctx, owner, state)
	case RouteWorkspaces:
		return ui.workspaceListScreen(ctx, owner, state)
	case RouteWorkspace:
		return ui.workspaceDetailScreen(ctx, owner, state)
	case RouteAccess:
		return ui.workspaceAccessScreen(ctx, owner, state)
	case RouteContainers:
		return ui.containerListScreen(ctx, owner, state)
	case RouteContainer:
		return ui.containerDetailScreen(ctx, owner, state)
	case RouteRequests:
		return ui.requestListScreen(ctx, owner, state)
	case RouteRequest:
		return ui.requestDetailScreen(ctx, owner, state)
	default:
		if state.Operation == "" {
			return Screen{}, errors.New("telegram navigation route is unavailable")
		}
		return ui.operationScreen(ctx, owner, state)
	}
}

func (ui *Interface) homeScreen(owner ViewOwner) (Screen, error) {
	status, err := ui.stateButton(owner, "Status", CallbackOpen, ActionState{Route: RouteStatus, Back: RouteHome, Operation: capability.StatusOverview})
	if err != nil {
		return Screen{}, err
	}
	settings, err := ui.stateButton(owner, "Settings", CallbackOpen, ActionState{Route: RouteSettings, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	auth, err := ui.stateButton(owner, "Auth", CallbackOpen, ActionState{Route: RouteAuth, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	help, err := ui.stateButton(owner, "Help", CallbackOpen, ActionState{Route: RouteCommands, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	workspaces, err := ui.stateButton(owner, "Workspaces", CallbackOpen, ActionState{Route: RouteWorkspaces, Back: RouteHome, Operation: capability.WorkspaceList})
	if err != nil {
		return Screen{}, err
	}
	requests, err := ui.stateButton(owner, "Requests", CallbackOpen, ActionState{Route: RouteRequests, Back: RouteHome, Operation: capability.RequestList})
	if err != nil {
		return Screen{}, err
	}
	unavailable := func(label string) Button {
		return Button{Text: CompactActionLabel(label), Disabled: true, Role: ButtonRoleNeutral}
	}
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram"),
		TitleBlock("Home", "Private administration interface"),
		StatusRow(ToneHealthy, "Authorized", "Commands are limited to this private account."),
		StatusRow(ToneHealthy, "Administration", "Workspace and approval operations use canonical application services."),
		StatusRow(ToneWarning, "Unavailable", "Remaining administration sections stay disabled until their canonical Telegram adapters are activated."),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{
		{status, unavailable("System")},
		{requests, unavailable("Completions")},
		{workspaces, unavailable("Secure MCP Tunnel")},
		{unavailable("Upstreams"), unavailable("Integrations")},
		{unavailable("Instructions")},
		{settings, auth},
		{unavailable("Logs")},
		{help, refresh},
	}}, nil
}

func (ui *Interface) commandsScreen(owner ViewOwner) (Screen, error) {
	back, err := ui.backButton(owner, RouteHome)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteCommands, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	items := make([]ListItem, 0, len(Commands()))
	for _, command := range Commands() {
		items = append(items, ListItem{Label: "/" + command.Name, Detail: command.Description})
	}
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram / Help"),
		TitleBlock("Help", "Use slash commands or inline controls. Ordinary text is ignored outside an authenticated input flow."),
		TitleBlock("Navigation", ""),
		CompactList(
			ListItem{Label: "Summary", Detail: "Lists and notifications stay compact so you can choose a resource."},
			ListItem{Label: "Details", Detail: "Details open the same canonical resource with richer current state."},
			ListItem{Label: "Back vs Close", Detail: "Back edits this message to the previous screen; Close dismisses only eligible terminal detail or result screens."},
			ListItem{Label: "Copy", Detail: "Copy controls are only created for explicitly safe bounded values such as stable IDs."},
			ListItem{Label: "Refresh", Detail: "Refresh re-renders the current route; it never bypasses canonical operation guards."},
		),
		TitleBlock("Commands", ""),
		CompactList(items...),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{{back, home, refresh}}}, nil
}

func (ui *Interface) settingsScreen(owner ViewOwner) (Screen, error) {
	back, err := ui.backButton(owner, RouteHome)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteSettings, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	health := ui.runtime.Health()
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram / Settings"),
		TitleBlock("Settings", "Navigation over current canonical configuration domains"),
		MetadataBlock(
			MetadataItem{Label: "Telegram", Value: boolState(health.Enabled)},
			MetadataItem{Label: "Notifications", Value: "canonical adapter pending"},
			MetadataItem{Label: "Admin Web UI", Value: "canonical adapter pending"},
			MetadataItem{Label: "Configuration", Value: "canonical adapter pending"},
		),
		StatusRow(ToneWarning, "Unavailable", "Unavailable settings are visible but inert until their owning Telegram adapter is activated."),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{
		{{Text: "Telegram", Disabled: true, Role: ButtonRoleConfigure}, {Text: "Notifications", Disabled: true, Role: ButtonRoleConfigure}},
		{{Text: "Admin Web UI", Disabled: true, Role: ButtonRoleConfigure}, {Text: "Configuration", Disabled: true, Role: ButtonRoleConfigure}},
		{back, home, refresh},
	}}, nil
}

func (ui *Interface) authScreen(owner ViewOwner) (Screen, error) {
	back, err := ui.backButton(owner, RouteHome)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteAuth, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	health := ui.runtime.Health()
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram / Auth"),
		TitleBlock("Authentication", "Current Telegram authorization boundary"),
		MetadataBlock(
			MetadataItem{Label: "Authorization", Value: boolState(health.AuthorizationConfigured)},
			MetadataItem{Label: "Bot token", Value: boolState(health.TokenConfigured)},
		),
		StatusRow(ToneWarning, "Unavailable", "Credential mutation and broader authentication controls remain inert until their canonical Telegram adapters are activated."),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{
		{{Text: "Authentication", Disabled: true, Role: ButtonRoleConfigure}, {Text: "Credentials", Disabled: true, Role: ButtonRoleConfigure}},
		{back, home, refresh},
	}}, nil
}

func (ui *Interface) operationScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	if state.Operation == "" {
		return Screen{}, errors.New("canonical operation is required")
	}
	spec, ok := capability.Lookup(state.Operation)
	if !ok {
		return Screen{}, fmt.Errorf("unknown canonical operation: %s", state.Operation)
	}
	if state.ExpectedVersion != "" {
		if ui.versions == nil {
			return Screen{}, errors.New("telegram resource version validation is unavailable")
		}
		current, err := ui.versions(ctx, state.Operation, state.ResourceID)
		if err != nil {
			return Screen{}, err
		}
		if strings.TrimSpace(current) != strings.TrimSpace(state.ExpectedVersion) {
			return StaleScreen(), nil
		}
	}
	if requiresExplicitConfirmation(spec) && !state.Confirmed {
		confirm := state
		confirm.Confirmed = true
		button, err := ui.stateButton(owner, "Confirm", CallbackConfirm, confirm)
		if err != nil {
			return Screen{}, err
		}
		if spec.Effects.Destructive {
			button.Role = ButtonRoleDestructive
		} else {
			button.Role = ButtonRolePositive
		}
		cancel, err := ui.cancelButton(owner, state.Back)
		if err != nil {
			return Screen{}, err
		}
		presentation := Present(ProductHeader("CodeMCP", "Telegram / Confirmation"), DestructiveConfirmation("Confirm operation", string(spec.ID), "This action follows the canonical confirmation policy."))
		return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{button}, Navigation: []Button{cancel}})}, nil
	}
	if ui.dispatcher == nil {
		return Screen{}, errors.New("canonical operation dispatcher is unavailable")
	}
	result, err := ui.dispatcher.Dispatch(application.WithOperationInterface(ctx, application.OperationInterfaceTelegram), application.DispatchRequest{Operation: state.Operation, Input: state.Input})
	if err != nil {
		return Screen{}, err
	}
	if screen, handled, err := ui.domainOperationResultScreen(owner, state, spec, result.Value); handled {
		return screen, err
	}
	parts := []PresentationPart{
		ProductHeader("CodeMCP", "Telegram / "+routeLabel(state.Route)),
		TitleBlock(string(result.Operation), "Canonical operation"),
		SuccessState("Operation completed."),
	}
	if status, ok := result.Value.(application.StatusOverview); ok {
		parts = append(parts, MetadataBlock(
			MetadataItem{Label: "runtime", Value: boolState(status.RuntimeRunning)},
			MetadataItem{Label: "MCP HTTP", Value: boolState(status.MCPHTTPEnabled)},
			MetadataItem{Label: "admin", Value: boolState(status.AdminEnabled)},
			MetadataItem{Label: "tunnel", Value: boolState(status.TunnelEnabled)},
			MetadataItem{Label: "Telegram", Value: boolState(status.TelegramEnabled)},
			MetadataItem{Label: "Telegram polling", Value: boolState(status.TelegramHealthy)},
		), StatusRow(ToneWarning, "Unavailable", "System and Logs drill-downs remain inert until their canonical Telegram adapters are activated."))
	} else {
		parts = append(parts, MetadataBlock(
			MetadataItem{Label: "operation", Value: string(result.Operation), Code: true},
			MetadataItem{Label: "result", Value: compactAny(result.Value)},
		))
	}
	presentation := Present(parts...)
	keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, result.Value)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: keyboard}, nil
}

func workingScreen(state ActionState) Screen {
	label := routeLabel(state.Route)
	if label == "Operation" && state.Operation != "" {
		label = string(state.Operation)
	}
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram / "+label),
		TitleBlock("Working", "The canonical operation is running."),
		LoadingState("Working…"),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{{{Text: "Working…", Disabled: true, Role: ButtonRoleNeutral}}}}
}

func (ui *Interface) operationErrorScreen(owner ViewOwner, state ActionState, operationErr error) (Screen, error) {
	if state.Operation == capability.WorkspaceRelocate {
		if screen, ok := ui.workspaceRelocationConflictScreen(owner, state, operationErr); ok {
			return screen, nil
		}
	}
	retry := state
	retry.Confirmed = false
	retryButton, err := ui.retryButton(owner, retry)
	if err != nil {
		return ErrorScreen(operationErr), err
	}
	back, err := ui.backButton(owner, state.Back)
	if err != nil {
		return ErrorScreen(operationErr), err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return ErrorScreen(operationErr), err
	}
	detail := "Operation failed"
	if operationErr != nil && strings.TrimSpace(operationErr.Error()) != "" {
		detail = compactPresentationValue(operationErr.Error())
	}
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram / Error"),
		TitleBlock("Operation failed", "Retry uses the same guarded operation path."),
		ErrorState(detail),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{retryButton}, Navigation: []Button{back, home}})}, nil
}

func (ui *Interface) inputPromptScreen(owner ViewOwner, state ActionState, title, prompt string) (Screen, error) {
	cancel, err := ui.cancelButton(owner, state.Back)
	if err != nil {
		return Screen{}, err
	}
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram / Input"),
		TitleBlock(strings.TrimSpace(title), strings.TrimSpace(prompt)),
		StatusRow(TonePending, "Input required", "Send the requested value or cancel this flow."),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{{cancel}}}, nil
}

func (ui *Interface) inputFailureScreen(owner ViewOwner, state ActionState, inputErr error) (Screen, error) {
	retry, err := ui.retryButton(owner, state)
	if err != nil {
		return ErrorScreen(inputErr), err
	}
	back, err := ui.backButton(owner, state.Back)
	if err != nil {
		return ErrorScreen(inputErr), err
	}
	detail := "Input was rejected"
	if inputErr != nil && strings.TrimSpace(inputErr.Error()) != "" {
		detail = compactPresentationValue(inputErr.Error())
	}
	presentation := Present(ProductHeader("CodeMCP", "Telegram / Input"), TitleBlock("Input failed", ""), ErrorState(detail))
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{retry}, Navigation: []Button{back}})}, nil
}

func (ui *Interface) editInputPrompt(ctx context.Context, owner ViewOwner, messageID int64, state ActionState, title, prompt string) error {
	if ui == nil || ui.runtime == nil || messageID <= 0 {
		return errors.New("telegram input workflow is unavailable")
	}
	screen, err := ui.inputPromptScreen(owner, state, title, prompt)
	if err != nil {
		return err
	}
	return ui.runtime.EditScreen(ctx, owner.ChatID, messageID, screen)
}

func (ui *Interface) editInputFailure(ctx context.Context, owner ViewOwner, messageID int64, state ActionState, inputErr error) error {
	if ui == nil || ui.runtime == nil || messageID <= 0 {
		return errors.New("telegram input workflow is unavailable")
	}
	screen, err := ui.inputFailureScreen(owner, state, inputErr)
	if err != nil {
		return err
	}
	return ui.runtime.EditScreen(ctx, owner.ChatID, messageID, screen)
}

func (ui *Interface) completeInput(ctx context.Context, owner ViewOwner, promptMessageID, inputMessageID int64, secretInput bool, result Screen) error {
	if ui == nil || ui.runtime == nil || promptMessageID <= 0 {
		return errors.New("telegram input workflow is unavailable")
	}
	if secretInput && inputMessageID > 0 {
		_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, inputMessageID)
	}
	if ui.inputs != nil {
		ui.inputs.Delete(promptMessageID)
	}
	return ui.runtime.EditScreen(ctx, owner.ChatID, promptMessageID, result)
}

func (ui *Interface) terminalOperationKeyboard(owner ViewOwner, state ActionState, spec capability.Spec, value any) ([][]Button, error) {
	back, err := ui.backButton(owner, state.Back)
	if err != nil {
		return nil, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return nil, err
	}
	secondary := []Button{}
	if _, ok := value.(application.StatusOverview); ok {
		secondary = append(secondary,
			Button{Text: "System", Disabled: true, Role: ButtonRoleView},
			Button{Text: "Logs", Disabled: true, Role: ButtonRoleView},
		)
	}
	if copyID, ok := CopyValueButton("Copy ID", state.ResourceID); ok {
		secondary = append(secondary, copyID)
	}
	navigation := []Button{back, home}
	if spec.Kind == capability.KindQuery || spec.Kind == capability.KindStream {
		refresh, err := ui.refreshButton(owner, state)
		if err != nil {
			return nil, err
		}
		navigation = append(navigation, refresh)
	} else {
		closeButton, err := ui.closeButton(owner, state)
		if err != nil {
			return nil, err
		}
		navigation = append(navigation, closeButton)
	}
	return BoundedActionGroups(ActionGroups{Secondary: secondary, Navigation: navigation}), nil
}

func (ui *Interface) stateButton(owner ViewOwner, label string, action CallbackAction, state ActionState) (Button, error) {
	token, err := ui.states.Put(owner, state)
	if err != nil {
		return Button{}, err
	}
	data, err := ui.callbacks.Encode(action, token)
	if err != nil {
		ui.states.Delete(token)
		return Button{}, err
	}
	return Button{Text: CompactActionLabel(label), CallbackData: data, Role: buttonRoleForCallback(action)}, nil
}

func (ui *Interface) backButton(owner ViewOwner, route Route) (Button, error) {
	if route == "" {
		route = RouteHome
	}
	return ui.stateButton(owner, "Back", CallbackBack, ActionState{Route: route})
}

func (ui *Interface) homeButton(owner ViewOwner) (Button, error) {
	return ui.stateButton(owner, "Home", CallbackHome, ActionState{Route: RouteHome})
}

func (ui *Interface) refreshButton(owner ViewOwner, state ActionState) (Button, error) {
	return ui.stateButton(owner, "Refresh", CallbackRefresh, state)
}

func (ui *Interface) retryButton(owner ViewOwner, state ActionState) (Button, error) {
	return ui.stateButton(owner, "Retry", CallbackRetry, state)
}

func (ui *Interface) cancelButton(owner ViewOwner, route Route) (Button, error) {
	if route == "" {
		route = RouteHome
	}
	return ui.stateButton(owner, "Cancel", CallbackCancel, ActionState{Route: route})
}

func (ui *Interface) closeButton(owner ViewOwner, state ActionState) (Button, error) {
	return ui.stateButton(owner, "Close", CallbackClose, ActionState{Route: state.Route, Back: state.Back})
}

func buttonRoleForCallback(action CallbackAction) ButtonRole {
	switch action {
	case CallbackBack, CallbackHome, CallbackRefresh, CallbackCancel, CallbackClose:
		return ButtonRoleNavigation
	case CallbackRetry:
		return ButtonRolePrimary
	case CallbackConfirm:
		return ButtonRolePositive
	default:
		return ButtonRoleView
	}
}

func (ui *Interface) paginationKeyboard(owner ViewOwner, state ActionState, total, size int) ([][]Button, error) {
	_, _, page, pages := PageBounds(total, state.Page, size)
	row := make([]Button, 0, 3)
	if page > 0 {
		previous := state
		previous.Page = page - 1
		button, err := ui.stateButton(owner, "Newer", CallbackOpen, previous)
		if err != nil {
			return nil, err
		}
		button.Role = ButtonRoleNavigation
		row = append(row, button)
	} else {
		row = append(row, Button{Text: "Newer", Disabled: true, Role: ButtonRoleNavigation})
	}
	row = append(row, Button{Text: PaginationLabel(page, pages), Disabled: true, Role: ButtonRoleNeutral})
	if page+1 < pages {
		next := state
		next.Page = page + 1
		button, err := ui.stateButton(owner, "Older", CallbackOpen, next)
		if err != nil {
			return nil, err
		}
		button.Role = ButtonRoleNavigation
		row = append(row, button)
	} else {
		row = append(row, Button{Text: "Older", Disabled: true, Role: ButtonRoleNavigation})
	}
	back, err := ui.backButton(owner, state.Back)
	if err != nil {
		return nil, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return nil, err
	}
	refresh, err := ui.refreshButton(owner, state)
	if err != nil {
		return nil, err
	}
	return [][]Button{row, {back, home, refresh}}, nil
}

func requiresExplicitConfirmation(spec capability.Spec) bool {
	return spec.Confirmation.Mode == capability.ConfirmationRequired || spec.Effects.Destructive
}

func ownerFromUpdate(runtime *Runtime, update Update) (ViewOwner, bool) {
	if runtime == nil {
		return ViewOwner{}, false
	}
	if update.Message != nil && update.Message.From != nil {
		return ViewOwner{ChatID: update.Message.Chat.ID, UserID: update.Message.From.ID, Generation: runtime.Generation()}, true
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
		return ViewOwner{ChatID: update.CallbackQuery.Message.Chat.ID, UserID: update.CallbackQuery.From.ID, Generation: runtime.Generation()}, true
	}
	return ViewOwner{}, false
}

func compactAny(value any) string {
	if value == nil {
		return "ok"
	}
	return compactPresentationValue(fmt.Sprint(value))
}

func boolState(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func routeLabel(route Route) string {
	switch route {
	case RouteHome:
		return "Home"
	case RouteStatus:
		return "Status"
	case RouteCommands:
		return "Commands"
	case RouteSettings:
		return "Settings"
	case RouteAuth:
		return "Auth"
	case RouteWorkspaces:
		return "Workspaces"
	case RouteWorkspace:
		return "Workspace"
	case RouteAccess:
		return "Workspace access"
	case RouteContainers:
		return "Containers"
	case RouteContainer:
		return "Container"
	case RouteRequests:
		return "Requests"
	case RouteRequest:
		return "Request"
	case RouteOperation:
		return "Operation"
	default:
		value := strings.TrimSpace(string(route))
		if value == "" {
			return "Operation"
		}
		return value
	}
}
