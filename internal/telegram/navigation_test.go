package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
)

type recordingDispatcher struct {
	calls  []application.DispatchRequest
	ifaces []application.OperationInterface
	result any
	err    error
}

type interactiveTestAPI struct {
	answers []string
	sent    []Screen
	edited  []Screen
}

func (*interactiveTestAPI) GetMe(context.Context) (User, error) { return User{ID: 1}, nil }
func (*interactiveTestAPI) GetUpdates(context.Context, int64, int, time.Duration) ([]Update, error) {
	return nil, nil
}
func (*interactiveTestAPI) SendMessage(context.Context, int64, string) error { return nil }
func (api *interactiveTestAPI) SendScreen(_ context.Context, _ int64, screen Screen) error {
	api.sent = append(api.sent, screen)
	return nil
}
func (api *interactiveTestAPI) EditScreen(_ context.Context, _ int64, _ int64, screen Screen) error {
	api.edited = append(api.edited, screen)
	return nil
}
func (api *interactiveTestAPI) AnswerCallback(_ context.Context, _ string, text string, _ bool) error {
	api.answers = append(api.answers, text)
	return nil
}

func (dispatcher *recordingDispatcher) Dispatch(ctx context.Context, request application.DispatchRequest) (application.DispatchResult, error) {
	dispatcher.calls = append(dispatcher.calls, request)
	dispatcher.ifaces = append(dispatcher.ifaces, application.OperationInterfaceFromContext(ctx))
	if dispatcher.err != nil {
		return application.DispatchResult{}, dispatcher.err
	}
	spec, _ := capability.Lookup(request.Operation)
	return application.DispatchResult{Operation: request.Operation, Metadata: spec, Value: dispatcher.result}, nil
}

func TestRouterDispatchesOnlyCommandsAndCallbacks(t *testing.T) {
	router := NewRouter()
	commands, callbacks := 0, 0
	router.RegisterCommand("status", func(context.Context, Update) { commands++ })
	router.RegisterCallback(func(context.Context, Update) { callbacks++ })

	if !router.Dispatch(t.Context(), Update{Message: &Message{Text: "/status@codemcp_bot now"}}) || commands != 1 {
		t.Fatalf("command calls=%d", commands)
	}
	if router.Dispatch(t.Context(), Update{Message: &Message{Text: "status"}}) {
		t.Fatal("free-form text was routed as a command")
	}
	if !router.Dispatch(t.Context(), Update{CallbackQuery: &CallbackQuery{Data: "opaque"}}) || callbacks != 1 {
		t.Fatalf("callback calls=%d", callbacks)
	}
}

func TestCompletedTelegramNavigationEntryPointsAreRegistered(t *testing.T) {
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 1}, Dispatcher: &recordingDispatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	routerCommands := map[string]bool{}
	for _, item := range ui.router.snapshot(true) {
		routerCommands[item.key] = true
	}
	routerCallbacks := ui.router.snapshot(false)
	commandRoutes := map[Route]bool{}
	for _, command := range Commands() {
		commandRoutes[command.Route] = true
	}

	for _, item := range capability.TelegramRolloutInventory() {
		if item.State != capability.TelegramRolloutLive {
			continue
		}
		for _, entry := range item.EntryPoints {
			switch entry.Kind {
			case capability.TelegramEntryCommand:
				if !routerCommands[entry.Value] {
					t.Fatalf("completed Telegram command %q is not registered in the production router", entry.Value)
				}
			case capability.TelegramEntryRoute:
				if !commandRoutes[Route(entry.Value)] {
					t.Fatalf("completed Telegram route %q is not reachable from the production command registry", entry.Value)
				}
			case capability.TelegramEntryCallback:
				if entry.Value != "*" || len(routerCallbacks) == 0 {
					t.Fatalf("completed Telegram callback route %q is not registered", entry.Value)
				}
			}
		}
	}
}

func TestProductionHandleAuthorizesBeforeRouterDispatch(t *testing.T) {
	dispatcher := &recordingDispatcher{result: map[string]any{"ok": true}}
	api := &interactiveTestAPI{}
	runtime := &Runtime{
		api: api, generation: 1,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
	}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}

	ui.Handle(t.Context(), Update{Message: &Message{From: &User{ID: 99}, Chat: Chat{ID: 99, Type: "private"}, Text: "/status"}})
	ui.Handle(t.Context(), Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: -100, Type: "group"}, Text: "/status"}})
	if len(dispatcher.calls) != 0 || len(api.sent) != 0 {
		t.Fatalf("unauthorized production updates reached router: calls=%d screens=%d", len(dispatcher.calls), len(api.sent))
	}

	ui.Handle(t.Context(), Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/status"}})
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.StatusOverview {
		t.Fatalf("authorized status dispatch=%#v", dispatcher.calls)
	}
	if dispatcher.ifaces[0] != application.OperationInterfaceTelegram || len(api.sent) != 1 {
		t.Fatalf("authorized production route iface=%q screens=%d", dispatcher.ifaces[0], len(api.sent))
	}
}

func TestProductionDispatchRejectsTelegramExemptOperation(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "unexpected"}
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 1}, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ui.dispatch(t.Context(), capability.AuthStatus, nil)
	if err == nil || !strings.Contains(err.Error(), "not exposed through Telegram") {
		t.Fatalf("dispatch error=%v", err)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("Telegram-exempt operation reached canonical dispatcher: %#v", dispatcher.calls)
	}
}

func TestProductionCallbackRejectsUnauthorizedAndStaleBeforeOperation(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "ok"}
	api := &interactiveTestAPI{}
	runtime := &Runtime{
		api: api, generation: 4,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
	}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 4}
	button, err := ui.stateButton(owner, "Status", CallbackOpen, ActionState{Route: RouteStatus, Back: RouteHome, Operation: capability.StatusOverview})
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := Update{CallbackQuery: &CallbackQuery{ID: "bad", From: User{ID: 99}, Data: button.CallbackData, Message: &Message{MessageID: 7, Chat: Chat{ID: 99, Type: "private"}}}}
	ui.Handle(t.Context(), unauthorized)
	if len(dispatcher.calls) != 0 || len(api.edited) != 0 {
		t.Fatalf("unauthorized callback reached operation: calls=%d edits=%d", len(dispatcher.calls), len(api.edited))
	}

	valid := Update{CallbackQuery: &CallbackQuery{ID: "ok", From: User{ID: 42}, Data: button.CallbackData, Message: &Message{MessageID: 7, Chat: Chat{ID: 42, Type: "private"}}}}
	ui.Handle(t.Context(), valid)
	if len(dispatcher.calls) != 1 || len(api.edited) != 1 {
		t.Fatalf("authorized callback calls=%d edits=%d", len(dispatcher.calls), len(api.edited))
	}
	ref, err := ui.callbacks.Decode(button.CallbackData)
	if err != nil {
		t.Fatal(err)
	}
	ui.states.Delete(ref.Token)
	ui.Handle(t.Context(), valid)
	if len(dispatcher.calls) != 1 {
		t.Fatalf("stale callback re-dispatched operation: calls=%d", len(dispatcher.calls))
	}
	if len(api.answers) < 2 || !strings.Contains(api.answers[len(api.answers)-1], "stale") {
		t.Fatalf("stale callback answer=%#v", api.answers)
	}
}

func TestCallbackCodecRejectsTamperingAndBoundsPayload(t *testing.T) {
	codec, err := NewCallbackCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := codec.Encode(CallbackOpen, "AbC_123-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(value) > MaxCallbackDataBytes || strings.Contains(value, "workspace") || strings.Contains(value, "secret") {
		t.Fatalf("callback=%q", value)
	}
	ref, err := codec.Decode(value)
	if err != nil || ref.Action != CallbackOpen || ref.Token != "AbC_123-x" {
		t.Fatalf("ref=%#v err=%v", ref, err)
	}
	tampered := value[:len(value)-1] + "A"
	if tampered == value {
		tampered = value[:len(value)-1] + "B"
	}
	if _, err := codec.Decode(tampered); err == nil {
		t.Fatal("tampered callback signature was accepted")
	}
	if _, err := codec.Encode(CallbackOpen, strings.Repeat("x", 60)); err == nil {
		t.Fatal("oversized callback payload was accepted")
	}
}

func TestViewStateRejectsForeignExpiredAndRestartedOwners(t *testing.T) {
	store := NewViewStateStore(time.Minute, 2)
	now := time.Now()
	store.now = func() time.Time { return now }
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	token, err := store.Put(owner, ActionState{Route: RouteHome})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(token, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(token, ViewOwner{ChatID: 43, UserID: 43, Generation: 7}); !errors.Is(err, ErrViewStateForeign) {
		t.Fatalf("foreign owner err=%v", err)
	}
	if _, err := store.Get(token, ViewOwner{ChatID: 42, UserID: 42, Generation: 8}); !errors.Is(err, ErrViewStateForeign) {
		t.Fatalf("restart generation err=%v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.Get(token, owner); !errors.Is(err, ErrViewStateStale) {
		t.Fatalf("expired state err=%v", err)
	}
}

func TestCanonicalRequiredConfirmationBlocksDispatchUntilConfirmed(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "purged"}
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 1}, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 1}
	state := ActionState{Route: "workspace.purge", Back: RouteHome, Operation: capability.WorkspacePurge, Input: "typed-input"}

	screen, err := ui.operationScreen(t.Context(), owner, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 0 || !strings.Contains(screen.Text, "Confirm operation") {
		t.Fatalf("calls=%d screen=%q", len(dispatcher.calls), screen.Text)
	}
	if len(screen.Keyboard) == 0 || len(screen.Keyboard[0]) == 0 {
		t.Fatalf("confirmation keyboard=%#v", screen.Keyboard)
	}
	ref, err := ui.callbacks.Decode(screen.Keyboard[0][0].CallbackData)
	if err != nil || ref.Action != CallbackConfirm {
		t.Fatalf("confirmation callback=%#v err=%v", ref, err)
	}
	value, err := ui.states.Get(ref.Token, owner)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := value.(ActionState)
	if !confirmed.Confirmed {
		t.Fatal("confirmed action state did not carry explicit confirmation")
	}
	if _, err := ui.operationScreen(t.Context(), owner, confirmed); err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.WorkspacePurge {
		t.Fatalf("calls=%#v", dispatcher.calls)
	}
	if dispatcher.calls[0].Input != "typed-input" {
		t.Fatalf("typed input=%#v", dispatcher.calls[0].Input)
	}
	if dispatcher.ifaces[0] != application.OperationInterfaceTelegram {
		t.Fatalf("operation interface=%q", dispatcher.ifaces[0])
	}
}

func TestExpectedVersionFailsClosedWithoutResolver(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "mutated"}
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 1}, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ui.operationScreen(t.Context(), ViewOwner{ChatID: 42, UserID: 42, Generation: 1}, ActionState{
		Route: "resource.update", Operation: capability.WorkspacePurge,
		ResourceID: "ws_a", ExpectedVersion: "v1", Confirmed: true,
	})
	if err == nil || len(dispatcher.calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(dispatcher.calls))
	}
}

func TestMutationCallbackStateIsOneShot(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "purged"}
	api := &interactiveTestAPI{}
	runtime := &Runtime{
		api: api, generation: 1,
		health: Health{Running: true, AuthorizationConfigured: true},
	}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 1}
	button, err := ui.stateButton(owner, "Confirm", CallbackConfirm, ActionState{
		Route: "workspace.purge", Operation: capability.WorkspacePurge, Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	update := Update{CallbackQuery: &CallbackQuery{
		ID: "cb_1", From: User{ID: 42}, Data: button.CallbackData,
		Message: &Message{MessageID: 7, Chat: Chat{ID: 42, Type: "private"}},
	}}
	ui.handleCallback(t.Context(), update)
	ui.handleCallback(t.Context(), update)
	if len(dispatcher.calls) != 1 {
		t.Fatalf("mutation dispatch count=%d", len(dispatcher.calls))
	}
	if len(api.answers) != 2 || !strings.Contains(api.answers[1], "stale") {
		t.Fatalf("callback answers=%#v", api.answers)
	}
}

func TestStaleExpectedVersionBlocksCanonicalDispatch(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "mutated"}
	ui, err := NewInterface(InterfaceOptions{
		Runtime:         &Runtime{generation: 1},
		Dispatcher:      dispatcher,
		VersionResolver: func(context.Context, capability.ID, string) (string, error) { return "v2", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	screen, err := ui.operationScreen(t.Context(), ViewOwner{ChatID: 42, UserID: 42, Generation: 1}, ActionState{
		Route: "resource.update", Back: RouteHome, Operation: capability.WorkspacePurge,
		ResourceID: "ws_a", ExpectedVersion: "v1", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 0 || !strings.Contains(screen.Text, "Stale control") {
		t.Fatalf("calls=%d screen=%q", len(dispatcher.calls), screen.Text)
	}
}

func TestNavigationPrimitivesCentralizeBackPaginationAndDetail(t *testing.T) {
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 3}, Dispatcher: &recordingDispatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 3}
	keyboard, err := ui.paginationKeyboard(owner, ActionState{Route: RouteStatus, Back: RouteHome, Page: 1}, 25, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyboard) != 2 || len(keyboard[0]) != 4 || keyboard[0][0].Text != "1" || keyboard[0][1].Text != "( 2 )" || !keyboard[0][1].Disabled || keyboard[0][2].Text != "3" || keyboard[0][3].Text != "4" || keyboard[1][0].Text != "Back" {
		t.Fatalf("pagination keyboard=%#v", keyboard)
	}
	part := DetailBlock("Detail <unsafe>", strings.Repeat("value & ", 100))
	if strings.Contains(string(part.HTML), "<unsafe>") || len(part.Text) == 0 {
		t.Fatalf("detail=%#v", part)
	}
	start, end, page, pages := PageBounds(25, 99, 8)
	if start != 24 || end != 25 || page != 3 || pages != 4 {
		t.Fatalf("bounds=%d,%d page=%d pages=%d", start, end, page, pages)
	}
}

func TestPresentationStatesAreEscapedAndBounded(t *testing.T) {
	presentation := Present(
		ProductHeader("CodeMCP", "Telegram"),
		LoadingState("<working>"),
		SuccessState("<success>"),
		ErrorState("<error>"),
		DestructiveConfirmation("Delete", "<target>", "required"),
		DetailBlock("Detail", strings.Repeat("<secret>&", 200)),
	)
	if len(presentation.HTML) > presentationMaxBytes {
		t.Fatalf("presentation bytes=%d", len(presentation.HTML))
	}
	for _, hostile := range []string{"<working>", "<success>", "<error>", "<target>", "<secret>"} {
		if strings.Contains(string(presentation.HTML), hostile) {
			t.Fatalf("unescaped value %q in %q", hostile, presentation.HTML)
		}
	}
}
