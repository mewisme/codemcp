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
	RouteHome     Route = "home"
	RouteStatus   Route = "status"
	RouteCommands Route = "commands"
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
		states: NewViewStateStore(stateTTL, defaultViewStateMax), callbacks: codec, router: NewRouter(),
	}
	ui.router.RegisterCommand("start", ui.handleHome)
	ui.router.RegisterCommand("home", ui.handleHome)
	ui.router.RegisterCommand("status", ui.handleStatus)
	ui.router.RegisterCommand("commands", ui.handleCommands)
	ui.router.RegisterCommand("help", ui.handleCommands)
	ui.router.RegisterCallback(ui.handleCallback)
	return ui, nil
}

func (ui *Interface) Handle(ctx context.Context, update Update) {
	if ui == nil {
		return
	}
	ui.router.Dispatch(ctx, update)
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
		_ = ui.runtime.AnswerCallback(ctx, update.CallbackQuery.ID, "This control is invalid.", true)
		return
	}
	value, err := ui.states.Get(ref.Token, owner)
	if err != nil {
		_ = ui.runtime.AnswerCallback(ctx, update.CallbackQuery.ID, "This control is stale. Open the screen again.", true)
		return
	}
	state, ok := value.(ActionState)
	if !ok {
		_ = ui.runtime.AnswerCallback(ctx, update.CallbackQuery.ID, "This control is invalid.", true)
		return
	}
	if ref.Action == CallbackConfirm {
		state.Confirmed = true
	}
	if state.Operation != "" {
		if spec, exists := capability.Lookup(state.Operation); exists && spec.Kind != capability.KindQuery && spec.Kind != capability.KindStream {
			ui.states.Delete(ref.Token)
		}
	}
	screen, err := ui.renderState(ctx, owner, state)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_ = ui.runtime.EditScreen(ctx, owner.ChatID, update.CallbackQuery.Message.MessageID, screen)
	_ = ui.runtime.AnswerCallback(ctx, update.CallbackQuery.ID, "", false)
}

func (ui *Interface) renderState(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	switch state.Route {
	case RouteHome:
		return ui.homeScreen(owner)
	case RouteCommands:
		return ui.commandsScreen(owner)
	case RouteStatus:
		return ui.operationScreen(ctx, owner, state)
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
	commands, err := ui.stateButton(owner, "Commands", CallbackOpen, ActionState{Route: RouteCommands, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram"),
		TitleBlock("Home", "Private administration interface"),
		StatusRow(ToneHealthy, "Authorized", "Commands are limited to this private account."),
	)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{{status, commands}}}, nil
}

func (ui *Interface) commandsScreen(owner ViewOwner) (Screen, error) {
	home, err := ui.stateButton(owner, "Home", CallbackBack, ActionState{Route: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	items := make([]ListItem, 0, len(commandRegistry))
	for _, command := range commandRegistry {
		items = append(items, ListItem{Label: "/" + command.Name, Detail: command.Description})
	}
	presentation := Present(ProductHeader("CodeMCP", "Telegram / Commands"), TitleBlock("Commands", "Available navigation commands"), CompactList(items...))
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{{home}}}, nil
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
		back, err := ui.backButton(owner, state.Back)
		if err != nil {
			return Screen{}, err
		}
		presentation := Present(ProductHeader("CodeMCP", "Telegram / Confirmation"), DestructiveConfirmation("Confirm operation", string(spec.ID), "This action follows the canonical confirmation policy."))
		return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{{button, back}}}, nil
	}
	if ui.dispatcher == nil {
		return Screen{}, errors.New("canonical operation dispatcher is unavailable")
	}
	result, err := ui.dispatcher.Dispatch(application.WithOperationInterface(ctx, application.OperationInterfaceTelegram), application.DispatchRequest{Operation: state.Operation, Input: state.Input})
	if err != nil {
		return Screen{}, err
	}
	back, err := ui.backButton(owner, state.Back)
	if err != nil {
		return Screen{}, err
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
		))
	} else {
		parts = append(parts, MetadataBlock(
			MetadataItem{Label: "operation", Value: string(result.Operation), Code: true},
			MetadataItem{Label: "result", Value: compactAny(result.Value)},
		))
	}
	presentation := Present(parts...)
	return Screen{Text: presentation.Text, HTML: presentation.HTML, Keyboard: [][]Button{{back}}}, nil
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
	return Button{Text: label, CallbackData: data}, nil
}

func (ui *Interface) backButton(owner ViewOwner, route Route) (Button, error) {
	if route == "" {
		route = RouteHome
	}
	return ui.stateButton(owner, "Back", CallbackBack, ActionState{Route: route})
}

func (ui *Interface) paginationKeyboard(owner ViewOwner, state ActionState, total, size int) ([][]Button, error) {
	_, _, page, pages := PageBounds(total, state.Page, size)
	row := []Button{}
	if page > 0 {
		previous := state
		previous.Page = page - 1
		button, err := ui.stateButton(owner, "Newer", CallbackOpen, previous)
		if err != nil {
			return nil, err
		}
		row = append(row, button)
	}
	row = append(row, Button{Text: PaginationLabel(page, pages), Disabled: true})
	if page+1 < pages {
		next := state
		next.Page = page + 1
		button, err := ui.stateButton(owner, "Older", CallbackOpen, next)
		if err != nil {
			return nil, err
		}
		row = append(row, button)
	}
	back, err := ui.backButton(owner, state.Back)
	if err != nil {
		return nil, err
	}
	return [][]Button{row, []Button{back}}, nil
}

func requiresExplicitConfirmation(spec capability.Spec) bool {
	return spec.Confirmation.Mode == capability.ConfirmationRequired
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
	default:
		value := strings.TrimSpace(string(route))
		if value == "" {
			return "Operation"
		}
		return value
	}
}
