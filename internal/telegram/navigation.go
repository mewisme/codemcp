package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/productadapter"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

type Route string

const (
	RouteHome            Route = "home"
	RouteStatus          Route = "status"
	RouteCommands        Route = "commands"
	RouteSettings        Route = "settings"
	RouteAuth            Route = "auth"
	RouteWorkspaces      Route = "workspaces"
	RouteWorkspace       Route = "workspace"
	RouteAccess          Route = "workspace.access"
	RouteContainers      Route = "containers"
	RouteContainer       Route = "container"
	RouteRequests        Route = "requests"
	RouteRequest         Route = "request"
	RouteGrants          Route = "grants"
	RouteGrant           Route = "grant"
	RouteCompletions     Route = "completions"
	RouteCompletion      Route = "completion"
	RouteProcesses       Route = "processes"
	RouteProcess         Route = "process"
	RouteCodeGraphWS     Route = "codegraph-workspace"
	RouteOperation       Route = "operation"
	RouteNetwork         Route = "network"
	RouteTunnel          Route = "tunnel"
	RouteManagedTunnels  Route = "managed-tunnels"
	RouteManagedTunnel   Route = "managed-tunnel"
	RouteUpstreams       Route = "upstreams"
	RouteUpstream        Route = "upstream"
	RouteIntegrations    Route = "integrations"
	RouteIntegration     Route = "integration"
	RouteLLM             Route = "llm"
	RouteLLMProvider     Route = "llm-provider"
	RouteLLMModels       Route = "llm-models"
	RouteSetting         Route = "setting"
	RouteAuthorizedUsers Route = "authorized-users"
	RouteSystem          Route = "system"
	RouteDoctor          Route = "doctor"
	RouteInstructions    Route = "instructions"
	RoutePrompts         Route = "prompts"
	RoutePrompt          Route = "prompt"
	RouteLogs            Route = "logs"
)

type ActionState struct {
	Route           Route
	Back            Route
	Operation       capability.ID
	Input           any
	InputFlow       *inputFlowState
	ResourceID      string
	ParentID        string
	ExpectedVersion string
	Page            int
	Detail          bool
	Confirmed       bool
	ForceConfirm    bool
	SecretInput     bool
	InputKind       string
}

type VersionResolver func(context.Context, capability.ID, string) (string, error)
type RuntimeStatusResolver func(context.Context) (runtimecontrol.RuntimeStatus, bool, error)

type InterfaceOptions struct {
	Runtime         *Runtime
	Dispatcher      application.OperationDispatcher
	VersionResolver VersionResolver
	RuntimeStatus   RuntimeStatusResolver
	CompletionList  CompletionListResolver
	CompletionView  CompletionViewResolver
	StateTTLSeconds int
}

type Interface struct {
	runtime        *Runtime
	dispatcher     application.OperationDispatcher
	versions       VersionResolver
	states         *ViewStateStore
	inputs         *InputStore
	callbacks      *CallbackCodec
	router         *Router
	operations     *operationMessageStore
	runtimeStatus  RuntimeStatusResolver
	completionList CompletionListResolver
	completionView CompletionViewResolver
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
		operations: newOperationMessageStore(options.Runtime.root), runtimeStatus: options.RuntimeStatus,
		completionList: options.CompletionList, completionView: options.CompletionView,
	}
	if ui.runtimeStatus == nil {
		ui.runtimeStatus = application.RuntimeStatus
	}
	if ui.completionList == nil {
		ui.completionList = application.ListCompletions
	}
	if ui.completionView == nil {
		ui.completionView = application.ViewCompletion
	}
	handlers := map[Route]RouteHandler{
		RouteHome: ui.handleHome, RouteStatus: ui.handleStatus, RouteCommands: ui.handleCommands,
		RouteWorkspaces: ui.handleWorkspaces, RouteRequests: ui.handleRequests, RouteCompletions: ui.handleCompletions, RouteNetwork: ui.handleNetwork,
		RouteSettings: ui.handleSettings, RouteIntegrations: ui.handleIntegrations, RouteLLM: ui.handleLLM,
		RouteSystem: ui.handleSystem, RouteInstructions: ui.handleInstructions, RouteLogs: ui.handleLogs,
	}
	for _, command := range Commands() {
		if handler := handlers[command.Route]; handler != nil {
			ui.router.RegisterCommand(command.Name, handler)
		}
	}
	ui.router.RegisterCallback(ui.handleCallback)
	options.Runtime.SetNotificationRenderer(ui.RenderNotification)
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
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteHome})
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleStatus(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteStatus, Back: RouteHome, Operation: capability.StatusOverview})
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleCommands(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteCommands, Back: RouteHome})
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleSettings(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteSettings, Back: RouteHome})
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) handleIntegrations(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteIntegrations, Back: RouteHome})
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
		_ = ui.runtime.EditScreen(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID, withRouteBreadcrumb(screen, state))
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
	if state.InputFlow != nil {
		ui.answerCallback(ctx, update.CallbackQuery.ID, "", false)
		messageID := update.CallbackQuery.Message.MessageID
		if state.InputFlow.Awaiting {
			if err := ui.beginInputFlowReply(ctx, owner, messageID, state); err != nil {
				_ = ui.runtime.EditScreen(ctx, owner.ChatID, messageID, withRouteBreadcrumb(ErrorScreen(err), state))
			}
			return
		}
		screen, screenErr := ui.renderState(ctx, owner, state)
		if screenErr != nil {
			screen = withRouteBreadcrumb(ErrorScreen(screenErr), state)
		}
		_ = ui.runtime.EditScreen(ctx, owner.ChatID, messageID, screen)
		return
	}
	if state.InputKind != "" {
		ui.answerCallback(ctx, update.CallbackQuery.ID, "", false)
		messageID := update.CallbackQuery.Message.MessageID
		if started, startErr := ui.beginInputFlow(ctx, owner, messageID, state); startErr != nil {
			_ = ui.runtime.EditScreen(ctx, owner.ChatID, messageID, withRouteBreadcrumb(ErrorScreen(startErr), state))
			return
		} else if started {
			return
		}
		if err := ui.beginActionInput(ctx, owner, state); err != nil {
			_ = ui.runtime.EditScreen(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID, withRouteBreadcrumb(ErrorScreen(err), state))
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
	messageID := update.CallbackQuery.Message.MessageID
	approvalDecision := state.Operation == capability.RequestApprove || state.Operation == capability.RequestDeny
	if hasSpec && shouldShowWorking(spec, state) {
		if err := ui.prepareDurableOperation(ctx, owner, messageID, state); err != nil {
			screen, _ := ui.operationErrorScreen(owner, state, err)
			_ = ui.runtime.EditScreen(ctx, owner.ChatID, messageID, withRouteBreadcrumb(screen, state))
			return
		}
	}
	if hasSpec && shouldShowWorking(spec, state) && !approvalDecision {
		_ = ui.runtime.EditScreen(ctx, owner.ChatID, messageID, workingScreen(state))
	}
	screen, err := ui.renderState(ctx, owner, state)
	if err != nil {
		if preserveDurableOperationOnError(state, err) {
			return
		}
		ui.clearDurableOperation(owner.ChatID, messageID, state)
		screen, _ = ui.operationErrorScreen(owner, state, err)
		_ = ui.runtime.EditScreen(ctx, owner.ChatID, messageID, withRouteBreadcrumb(screen, state))
		return
	}
	if approvalDecision {
		_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, messageID)
		ui.clearDurableOperation(owner.ChatID, messageID, state)
		return
	}
	if editErr := ui.runtime.EditScreen(ctx, owner.ChatID, messageID, screen); editErr == nil {
		ui.clearDurableOperation(owner.ChatID, messageID, state)
	}
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
	back, err := ui.operationBackButton(owner, state)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	screen := StaleScreen()
	screen.Keyboard = [][]Button{{back, home}}
	return withRouteBreadcrumb(screen, state), nil
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

func (ui *Interface) routeScreen(ctx context.Context, owner ViewOwner, state ActionState) Screen {
	screen, err := ui.renderState(ctx, owner, state)
	if err != nil {
		return withRouteBreadcrumb(ErrorScreen(err), state)
	}
	return screen
}

func (ui *Interface) renderState(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	screen, err := ui.renderStateContent(ctx, owner, state)
	if err != nil {
		return Screen{}, err
	}
	return withRouteBreadcrumb(screen, state), nil
}

func (ui *Interface) renderStateContent(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	switch state.Route {
	case RouteHome:
		return ui.homeScreen(owner)
	case RouteCommands:
		return ui.commandsScreen(owner)
	case RouteSettings:
		return ui.settingsScreen(ctx, owner, state)
	case RouteAuth:
		return ui.authScreen(ctx, owner)
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
	case RouteGrants:
		return ui.grantListScreen(ctx, owner, state)
	case RouteGrant:
		return ui.grantDetailScreen(ctx, owner, state)
	case RouteCompletions:
		return ui.completionListScreen(ctx, owner, state)
	case RouteCompletion:
		return ui.completionDetailScreen(ctx, owner, state)
	case RouteProcesses:
		return ui.processListScreen(ctx, owner, state)
	case RouteProcess:
		return ui.processDetailScreen(ctx, owner, state)
	case RouteCodeGraphWS:
		return ui.codeGraphWorkspaceScreen(ctx, owner, state)
	case RouteNetwork:
		return ui.networkScreen(owner)
	case RouteTunnel:
		return ui.tunnelScreen(ctx, owner)
	case RouteManagedTunnels:
		return ui.managedTunnelListScreen(ctx, owner, state)
	case RouteManagedTunnel:
		return ui.managedTunnelDetailScreen(ctx, owner, state)
	case RouteUpstreams:
		return ui.upstreamListScreen(ctx, owner, state)
	case RouteUpstream:
		return ui.upstreamDetailScreen(ctx, owner, state)
	case RouteIntegrations:
		return ui.integrationsScreen(ctx, owner)
	case RouteIntegration:
		return ui.integrationScreen(ctx, owner, state)
	case RouteLLM:
		return ui.llmScreen(ctx, owner, state)
	case RouteLLMProvider:
		return ui.llmProviderScreen(ctx, owner, state)
	case RouteLLMModels:
		return ui.llmModelsScreen(ctx, owner, state)
	case RouteSetting:
		return ui.settingDetailScreen(ctx, owner, state)
	case RouteAuthorizedUsers:
		return ui.authorizedUsersScreen(ctx, owner)
	case RouteSystem:
		return ui.systemScreen(ctx, owner)
	case RouteDoctor:
		return ui.doctorScreen(ctx, owner, state)
	case RouteInstructions:
		return ui.instructionsScreen(ctx, owner)
	case RoutePrompts:
		return ui.promptsScreen(ctx, owner, state)
	case RoutePrompt:
		return ui.promptScreen(ctx, owner, state)
	case RouteLogs:
		return ui.logsMiniAppScreen(owner)
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
	completions, err := ui.stateButton(owner, "Completions", CallbackOpen, ActionState{Route: RouteCompletions, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	network, err := ui.stateButton(owner, "Network", CallbackOpen, ActionState{Route: RouteNetwork, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	upstreams, err := ui.stateButton(owner, "Upstreams", CallbackOpen, ActionState{Route: RouteUpstreams, Back: RouteHome, Operation: capability.UpstreamServerList})
	if err != nil {
		return Screen{}, err
	}
	integrations, err := ui.stateButton(owner, "Integrations", CallbackOpen, ActionState{Route: RouteIntegrations, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	llmButton, err := ui.stateButton(owner, "LLM", CallbackOpen, ActionState{Route: RouteLLM, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	system, err := ui.stateButton(owner, "System", CallbackOpen, ActionState{Route: RouteSystem, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	instructions, err := ui.stateButton(owner, "Instructions", CallbackOpen, ActionState{Route: RouteInstructions, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	logs, err := ui.stateButton(owner, "Logs", CallbackOpen, ActionState{Route: RouteLogs, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Home", Text: "Private administration interface"},
		RichBlock{Kind: RichSection, Title: "Administration", Text: "Manage workspaces, requests, connectivity, integrations, settings, and runtime state."},
		RichBlock{Kind: RichSection, Title: "Activity", Text: "Review agent completions and retained runtime activity from their dedicated views."},
	)
	return Screen{Rich: rich, Keyboard: [][]Button{
		{status, system},
		{requests, completions},
		{workspaces, network},
		{upstreams, integrations},
		{llmButton, instructions},
		{settings, auth},
		{logs},
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
	items := make([]string, 0, len(Commands()))
	for _, command := range Commands() {
		items = append(items, "/"+command.Name+" — "+command.Description)
	}
	rich := BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Help", Text: "Use slash commands or inline controls. Ordinary text is ignored outside an authenticated input flow."},
		RichBlock{Kind: RichList, Title: "Navigation", Items: []string{
			"Summary — Lists and notifications stay compact so you can choose a resource.",
			"Details — Open the selected resource with its current state.",
			"Back / Close — Back returns to the previous screen; Close dismisses eligible terminal screens.",
			"Copy — Available only for safe bounded values such as stable IDs.",
			"Refresh — Re-renders the current route without bypassing operation guards.",
		}},
		RichBlock{Kind: RichList, Title: "Commands", Items: items},
	)
	return Screen{Rich: rich, Keyboard: [][]Button{{back, home, refresh}}}, nil
}

func (ui *Interface) operationScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	if state.InputFlow != nil {
		return ui.inputFlowScreen(ctx, owner, state)
	}
	if state.Operation == "" {
		return Screen{}, errors.New("operation is required")
	}
	spec, ok := capability.Lookup(state.Operation)
	if !ok {
		return Screen{}, fmt.Errorf("unknown operation: %s", state.Operation)
	}
	operationPresentation, _ := productadapter.PresentationFor(state.Operation, "")
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
	if (requiresExplicitConfirmation(spec) || state.ForceConfirm) && !state.Confirmed {
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
		lifecycle, _ := productadapter.Lifecycle(productadapter.LifecycleConfirming)
		detail := "Review this action before continuing."
		if operationPresentation.Danger == productadapter.DangerDestructive {
			detail = "This action is destructive and may remove persistent state."
		}
		tone := ToneWarning
		if spec.Effects.Destructive {
			tone = ToneDestructive
		}
		return Screen{Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: lifecycle.Label, Text: operationPresentation.Title},
			NoticeBlock(tone, operationPresentation.Title, strings.TrimSpace(operationPresentation.Subject+" "+detail)),
		), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{button}, Navigation: []Button{cancel}})}, nil
	}
	if ui.dispatcher == nil {
		return Screen{}, errors.New("operation dispatcher is unavailable")
	}
	result, err := ui.dispatcher.Dispatch(application.WithOperationInterface(ctx, application.OperationInterfaceTelegram), application.DispatchRequest{Operation: state.Operation, Input: state.Input})
	if err != nil {
		return Screen{}, err
	}
	if screen, handled, err := ui.domainOperationResultScreen(ctx, owner, state, spec, result.Value); handled {
		return screen, err
	}
	if screen, handled, err := ui.networkOperationResultScreen(owner, state, spec, result.Value); handled {
		return screen, err
	}
	if screen, handled, err := ui.settingsOperationResultScreen(ctx, owner, state, spec, result.Value); handled {
		return screen, err
	}
	if screen, handled, err := ui.systemOperationResultScreen(ctx, owner, state, result.Value); handled {
		return screen, err
	}
	if status, ok := result.Value.(application.StatusOverview); ok {
		keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, result.Value)
		if err != nil {
			return Screen{}, err
		}
		return Screen{Rich: ui.statusOverviewPresentation(status), Keyboard: keyboard}, nil
	}
	keyboard, err := ui.terminalOperationKeyboard(owner, state, spec, result.Value)
	if err != nil {
		return Screen{}, err
	}
	blocks := []RichBlock{{Kind: RichHeading, Title: operationPresentation.Title, Text: operationPresentation.Subject}}
	resultText := compactAny(result.Value)
	if spec.Kind == capability.KindMutation || spec.Kind == capability.KindRuntime {
		blocks = append(blocks, StateBlock(ToneSuccess, "Success", ""))
	}
	if resultText != "" && resultText != "ok" {
		blocks = append(blocks, FieldsBlock("", []string{"Result", resultText}))
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: keyboard}, nil
}

func workingScreen(state ActionState) Screen {
	label := routeLabel(state.Route)
	title := label
	if state.Operation != "" {
		if metadata, ok := productadapter.PresentationFor(state.Operation, ""); ok {
			title = metadata.Title
		}
	}
	return withRouteBreadcrumb(Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: title},
		StateBlock(TonePending, "Working", "Operation in progress"),
	), Keyboard: [][]Button{{{Text: lifecycleLabel(productadapter.LifecycleWorking) + "…", Disabled: true, Role: ButtonRoleNeutral}}}}, state)
}

func (ui *Interface) operationErrorScreen(owner ViewOwner, state ActionState, operationErr error) (Screen, error) {
	if state.Operation == capability.RequestApprove || state.Operation == capability.RequestDeny {
		if screen, err := ui.requestDetailScreen(context.Background(), owner, ActionState{Route: RouteRequest, Back: RouteRequests, ResourceID: state.ResourceID, Operation: capability.RequestView}); err == nil {
			return screen, nil
		}
	}
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
	back, err := ui.operationBackButton(owner, state)
	if err != nil {
		return ErrorScreen(operationErr), err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return ErrorScreen(operationErr), err
	}
	detail := lifecycleLabel(productadapter.LifecycleRetryableFailure)
	if operationErr != nil && strings.TrimSpace(operationErr.Error()) != "" {
		detail = compactPresentationValue(operationErr.Error())
	}
	title := "Operation"
	if metadata, ok := productadapter.PresentationFor(state.Operation, ""); ok {
		title = metadata.Title
	}
	return withRouteBreadcrumb(Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: title},
		NoticeBlock(ToneFailure, lifecycleLabel(productadapter.LifecycleRetryableFailure), detail),
		RichBlock{Kind: RichDetails, Title: "Retry", Text: "Retry uses the same guarded operation path."},
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{retryButton}, Navigation: []Button{back, home}})}, state), nil
}

func (ui *Interface) inputPromptScreen(owner ViewOwner, state ActionState, title, prompt string) (Screen, error) {
	target := operationBackState(state)
	cancel, err := ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationCancel), CallbackCancel, target)
	if err != nil {
		return Screen{}, err
	}
	return withRouteBreadcrumb(Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: strings.TrimSpace(title), Text: strings.TrimSpace(prompt)},
		NoticeBlock(TonePending, "Input required", "Send the requested value or cancel this flow."),
	), Keyboard: [][]Button{{cancel}}}, state), nil
}

func (ui *Interface) inputFailureScreen(owner ViewOwner, state ActionState, inputErr error) (Screen, error) {
	retry, err := ui.retryButton(owner, state)
	if err != nil {
		return ErrorScreen(inputErr), err
	}
	back, err := ui.operationBackButton(owner, state)
	if err != nil {
		return ErrorScreen(inputErr), err
	}
	detail := "Input was rejected"
	if inputErr != nil && strings.TrimSpace(inputErr.Error()) != "" {
		detail = compactPresentationValue(inputErr.Error())
	}
	return withRouteBreadcrumb(Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Input failed"},
		NoticeBlock(ToneFailure, "Input rejected", detail),
	), Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{retry}, Navigation: []Button{back}})}, state), nil
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
	back, err := ui.operationBackButton(owner, state)
	if err != nil {
		return nil, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return nil, err
	}
	secondary := []Button{}
	if _, ok := value.(application.StatusOverview); ok {
		system, systemErr := ui.stateButton(owner, "System", CallbackOpen, ActionState{Route: RouteSystem, Back: RouteStatus})
		if systemErr != nil {
			return nil, systemErr
		}
		logs, logsErr := ui.stateButton(owner, "Logs", CallbackOpen, ActionState{Route: RouteLogs, Back: RouteStatus})
		if logsErr != nil {
			return nil, logsErr
		}
		secondary = append(secondary, system, logs)
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

func (ui *Interface) operationBackButton(owner ViewOwner, state ActionState) (Button, error) {
	return ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationBack), CallbackBack, operationBackState(state))
}

func operationBackState(state ActionState) ActionState {
	route := state.Back
	if route == "" {
		route = RouteHome
	}
	if parentID := strings.TrimSpace(state.ParentID); parentID != "" {
		return ActionState{Route: route, ResourceID: parentID}
	}
	switch route {
	case RouteUpstream, RouteWorkspace, RouteCodeGraphWS, RouteManagedTunnel, RouteLLMProvider, RouteLLMModels:
		if resourceID := strings.TrimSpace(state.ResourceID); resourceID != "" {
			return ActionState{Route: route, ResourceID: resourceID}
		}
	}
	return ActionState{Route: route}
}

func (ui *Interface) stateButton(owner ViewOwner, label string, action CallbackAction, state ActionState) (Button, error) {
	switch action {
	case CallbackBack:
		label = previousNavigationLabel(label)
	case CallbackHome:
		label = homeNavigationLabel(label)
	case CallbackRefresh:
		label = refreshNavigationLabel(label)
	}
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

func previousNavigationLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" || strings.HasPrefix(label, "«") {
		return label
	}
	return "« " + label
}

func nextNavigationLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" || strings.HasSuffix(label, "»") {
		return label
	}
	return label + " »"
}

func homeNavigationLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" || strings.HasPrefix(label, "⌂") {
		return label
	}
	return "⌂ " + label
}

func refreshNavigationLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" || strings.HasPrefix(label, "↻") {
		return label
	}
	return "↻ " + label
}

func (ui *Interface) backButton(owner ViewOwner, route Route) (Button, error) {
	if route == "" {
		route = RouteHome
	}
	return ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationBack), CallbackBack, ActionState{Route: route})
}

func (ui *Interface) homeButton(owner ViewOwner) (Button, error) {
	return ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationHome), CallbackHome, ActionState{Route: RouteHome})
}

func (ui *Interface) refreshButton(owner ViewOwner, state ActionState) (Button, error) {
	return ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationRefresh), CallbackRefresh, state)
}

func (ui *Interface) retryButton(owner ViewOwner, state ActionState) (Button, error) {
	return ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationRetry), CallbackRetry, state)
}

func (ui *Interface) cancelButton(owner ViewOwner, route Route) (Button, error) {
	if route == "" {
		route = RouteHome
	}
	return ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationCancel), CallbackCancel, ActionState{Route: route})
}

func (ui *Interface) closeButton(owner ViewOwner, state ActionState) (Button, error) {
	return ui.stateButton(owner, productadapter.NavigationLabel(productadapter.NavigationClose), CallbackClose, ActionState{Route: state.Route, Back: state.Back})
}

func lifecycleLabel(state productadapter.LifecycleState) string {
	if value, ok := productadapter.Lifecycle(state); ok {
		return value.Label
	}
	return string(state)
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
	rows := make([][]Button, 0, 2)
	if pages > 1 {
		row := make([]Button, 0, paginatorMaxPageButtons)
		for _, target := range PaginatorPages(page, pages) {
			label := fmt.Sprintf("%d", target+1)
			if pages > paginatorMaxPageButtons {
				switch target {
				case 0:
					label = "« " + label
				case pages - 1:
					label += " »"
				}
			}
			if target == page {
				row = append(row, Button{Text: "( " + fmt.Sprintf("%d", target+1) + " )", Disabled: true, Role: ButtonRoleNeutral})
				continue
			}
			targetState := state
			targetState.Page = target
			button, err := ui.stateButton(owner, label, CallbackOpen, targetState)
			if err != nil {
				return nil, err
			}
			button.Role = ButtonRoleNavigation
			row = append(row, button)
		}
		rows = append(rows, row)
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
	rows = append(rows, []Button{back, home, refresh})
	return rows, nil
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
	return stateLabel(value, "Enabled", "Disabled")
}

func (ui *Interface) statusOverviewPresentation(status application.StatusOverview) *RichPresentation {
	runtimeTone, runtimeLabel := ToneStopped, "Stopped"
	if status.RuntimeRunning {
		runtimeTone, runtimeLabel = ToneHealthy, "Running"
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: "Status", Text: "Runtime and interface overview"},
		StateBlock(runtimeTone, "Runtime "+runtimeLabel, ""),
		FieldsBlock("Services",
			[]string{"MCP HTTP", boolState(status.MCPHTTPEnabled)},
			[]string{"Admin UI", boolState(status.AdminEnabled)},
			[]string{"Secure MCP Tunnel", effectiveState(status.TunnelEnabled, status.TunnelConfigured, status.TunnelReady || status.TunnelRunning)},
		),
	}
	polling := "Disabled"
	if status.TelegramEnabled {
		polling = effectiveState(true, status.TelegramConfigured, status.TelegramHealthy)
	}
	topicsState := effectiveState(status.TelegramTopicsEnabled, status.TelegramTopicsSupported, status.TelegramTopicsEffective)
	if status.TelegramTopicsRepairing {
		topicsState = "Repairing"
	} else if strings.TrimSpace(status.TelegramTopicsError) != "" {
		topicsState = "Degraded"
	}
	telegramRows := [][]string{
		{"Bot", boolState(status.TelegramEnabled)},
		{"Polling", polling},
		{"Topics", topicsState},
		{"Logs App", effectiveState(status.LogsMiniAppEnabled, status.LogsMiniAppAvailable, status.LogsMiniAppEffective)},
	}
	var logsNotice *RichBlock
	if ui != nil && ui.runtime != nil {
		health := ui.runtime.Health().LogsMiniApp
		if health.Enabled && health.State != MiniAppReady && health.State != MiniAppStarting {
			detail := strings.TrimSpace(health.LastError)
			if detail == "" {
				detail = "Logs App is not ready."
			}
			notice := NoticeBlock(ToneWarning, "Logs App unavailable", detail)
			logsNotice = &notice
		}
	}
	blocks = append(blocks, FieldsBlock("Telegram", telegramRows...))
	if logsNotice != nil {
		blocks = append(blocks, *logsNotice)
	}
	return BuildRichPresentation(blocks...)
}

func effectiveState(enabled, configuredOrAvailable, effective bool) string {
	if !enabled {
		return "Disabled"
	}
	if effective {
		return "Running"
	}
	if !configuredOrAvailable {
		return "Unavailable"
	}
	return "Available"
}

func withRouteBreadcrumb(screen Screen, state ActionState) Screen {
	screen.Breadcrumb = routeBreadcrumb(state)
	return screen
}

func routeBreadcrumb(state ActionState) []string {
	current := state.Route
	if current == "" {
		current = RouteOperation
	}
	chain := []Route{current}
	parent := state.Back
	if parent == "" || parent == current {
		parent = defaultBreadcrumbParent(current)
	}
	seen := map[Route]bool{current: true}
	for parent != "" && parent != RouteHome && !seen[parent] {
		seen[parent] = true
		chain = append([]Route{parent}, chain...)
		parent = defaultBreadcrumbParent(parent)
	}
	items := make([]string, 0, len(chain)+2)
	items = append(items, "CodeMCP")
	for _, route := range chain {
		items = append(items, routeLabel(route))
	}
	if id := strings.TrimSpace(state.ResourceID); id != "" {
		items = append(items, id)
	}
	return dedupeBreadcrumb(items)
}

func defaultBreadcrumbParent(route Route) Route {
	switch route {
	case RouteWorkspace:
		return RouteWorkspaces
	case RouteAccess, RouteContainers, RouteCodeGraphWS:
		return RouteWorkspace
	case RouteContainer:
		return RouteContainers
	case RouteRequest, RouteGrants:
		return RouteRequests
	case RouteGrant:
		return RouteGrants
	case RouteCompletion:
		return RouteCompletions
	case RouteProcess:
		return RouteProcesses
	case RouteTunnel, RouteManagedTunnels, RouteUpstreams:
		return RouteNetwork
	case RouteManagedTunnel:
		return RouteManagedTunnels
	case RouteUpstream:
		return RouteUpstreams
	case RouteIntegration:
		return RouteIntegrations
	case RouteLLMProvider, RouteLLMModels:
		return RouteLLM
	case RouteSetting, RouteAuthorizedUsers:
		return RouteSettings
	case RouteDoctor:
		return RouteSystem
	case RoutePrompts:
		return RouteInstructions
	case RoutePrompt:
		return RoutePrompts
	default:
		return RouteHome
	}
}

func dedupeBreadcrumb(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || (len(result) > 0 && result[len(result)-1] == item) {
			continue
		}
		result = append(result, item)
	}
	return result
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
	case RouteGrants:
		return "Grants"
	case RouteGrant:
		return "Grant"
	case RouteCompletions:
		return "Completions"
	case RouteCompletion:
		return "Completion"
	case RouteProcesses:
		return "Processes"
	case RouteProcess:
		return "Process"
	case RouteCodeGraphWS:
		return "CodeGraph"
	case RouteNetwork:
		return "Network"
	case RouteTunnel:
		return "Secure MCP Tunnel"
	case RouteManagedTunnels:
		return "Managed tunnels"
	case RouteManagedTunnel:
		return "Managed tunnel"
	case RouteUpstreams:
		return "Upstreams"
	case RouteUpstream:
		return "Upstream"
	case RouteIntegrations:
		return "Integrations"
	case RouteIntegration:
		return "Integration"
	case RouteLLM:
		return "LLM"
	case RouteLLMProvider:
		return "LLM provider"
	case RouteLLMModels:
		return "LLM models"
	case RouteSetting:
		return "Setting"
	case RouteAuthorizedUsers:
		return "Authorized users"
	case RouteSystem:
		return "System"
	case RouteDoctor:
		return "Doctor"
	case RouteInstructions:
		return "Instructions"
	case RoutePrompts:
		return "Prompts"
	case RoutePrompt:
		return "Prompt"
	case RouteLogs:
		return "Logs"
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
