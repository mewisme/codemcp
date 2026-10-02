package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	telegrambot "github.com/go-telegram/bot"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
)

func TestBotAPIAdapterStreamsOnlyConsumedUpdatesInOrder(t *testing.T) {
	type pollRequest struct {
		Offset  string
		Allowed []string
	}
	requestSeen := make(chan pollRequest, 2)
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(strings.ToLower(r.URL.Path), "/getupdates") {
			http.NotFound(w, r)
			return
		}
		poll := polls.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse getUpdates form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var allowed []string
		if err := json.Unmarshal([]byte(r.FormValue("allowed_updates")), &allowed); err != nil {
			t.Errorf("decode allowed_updates: %v", err)
		}
		if poll <= 2 {
			requestSeen <- pollRequest{Offset: r.FormValue("offset"), Allowed: allowed}
		}
		if poll > 1 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": []any{
				map[string]any{"update_id": 10, "message": map[string]any{"message_id": "future-polymorphic-shape"}},
				map[string]any{"update_id": 11, "message": map[string]any{
					"message_id": 5,
					"from":       map[string]any{"id": 42, "first_name": "Mew"},
					"chat":       map[string]any{"id": 42, "type": "private"},
					"text":       "/status",
				}},
			},
		})
	}))
	defer server.Close()

	client := newAPIClientWithOptions("123456:test-token", 2*time.Second, server.URL, server.Client())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	updates := make(chan Update, 1)
	done := make(chan error, 1)
	go func() {
		done <- client.StartUpdates(ctx, 7, func(_ context.Context, update Update) {
			updates <- update
		}, nil)
	}()

	var got Update
	select {
	case got = <-updates:
	case <-time.After(time.Second):
		t.Fatal("valid update after malformed update was not delivered")
	}
	if got.UpdateID != 11 || got.Message == nil || got.Message.Text != "/status" {
		t.Fatalf("message update=%#v", got)
	}
	first := <-requestSeen
	var second pollRequest
	select {
	case second = <-requestSeen:
	case <-time.After(time.Second):
		t.Fatal("transport did not issue the next poll")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
	if first.Offset != "7" || second.Offset != "12" {
		t.Fatalf("poll offsets first=%q second=%q want=7,12", first.Offset, second.Offset)
	}
	wantAllowed := []string{"message", "callback_query"}
	if !reflect.DeepEqual(first.Allowed, wantAllowed) || !reflect.DeepEqual(second.Allowed, wantAllowed) {
		t.Fatalf("allowed updates first=%v second=%v want=%v", first.Allowed, second.Allowed, wantAllowed)
	}
}

func TestDefaultRuntimeFactoryUsesStreamingBotAPIAdapter(t *testing.T) {
	runtime := NewRuntime(Options{})
	api := runtime.factory("123456:test-token")
	if _, ok := api.(*apiClient); !ok {
		t.Fatalf("default telegram transport=%T want *apiClient", api)
	}
	if _, ok := api.(streamingAPI); !ok {
		t.Fatalf("default telegram transport=%T does not implement streamingAPI", api)
	}
	if _, ok := api.(injectedUpdateAPI); !ok {
		t.Fatalf("default telegram transport=%T does not implement injectedUpdateAPI", api)
	}
	if _, ok := api.(pollingAPI); ok {
		t.Fatalf("default telegram transport=%T retained production batch polling", api)
	}
}

func TestBotAPIAdapterHonorsTelegramRetryAfterWithoutHotRetry(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(strings.ToLower(r.URL.Path), "/getupdates") {
			http.NotFound(w, r)
			return
		}
		polls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"error_code":  429,
			"description": "Too Many Requests",
			"parameters":  map[string]any{"retry_after": 1},
		})
	}))
	defer server.Close()

	client := newAPIClientWithOptions("123456:test-token", 2*time.Second, server.URL, server.Client())
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	events := make(chan pollEvent, 2)
	err := client.StartUpdates(ctx, 0, func(context.Context, Update) {}, func(event pollEvent) { events <- event })
	if err == nil || ctx.Err() == nil {
		t.Fatalf("stream err=%v ctx=%v", err, ctx.Err())
	}
	if got := polls.Load(); got != 1 {
		t.Fatalf("getUpdates calls=%d want=1 during retry_after window", got)
	}
	select {
	case event := <-events:
		var typed *transportError
		if !errors.As(event.Err, &typed) || typed.Class != transportErrorRateLimited || typed.RetryAfter != time.Second {
			t.Fatalf("rate-limit event=%#v err=%v", event, event.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("rate-limit event was not reported")
	}
}

func TestClassifyEditErrorDistinguishesNoopAndStaleMessages(t *testing.T) {
	if err := classifyEditError(errors.New("Bad Request: message is not modified")); err != nil {
		t.Fatalf("no-op edit error=%v", err)
	}
	stale := classifyEditError(errors.New("Bad Request: message to edit not found"))
	if transportErrorKind(stale) != transportErrorStaleMessage {
		t.Fatalf("stale edit classification=%v kind=%q", stale, transportErrorKind(stale))
	}
}

func TestBotAPIInjectedUpdateCannotBypassAuthorizationOrSignedCallbackState(t *testing.T) {
	dispatcher := &recordingDispatcher{result: "ok"}
	transport := &interactiveTestAPI{}
	runtime := &Runtime{
		api: transport, generation: 4,
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
	client := newAPIClientWithOptions("123456:test-token", time.Second, "", nil)
	client.ProcessUpdate(t.Context(), Update{UpdateID: 1, Message: &Message{
		From: &User{ID: 99}, Chat: Chat{ID: 99, Type: "private"}, Text: "/status",
	}}, ui.Handle)
	if len(dispatcher.calls) != 0 {
		t.Fatalf("foreign injected command dispatched=%#v", dispatcher.calls)
	}
	client.ProcessUpdate(t.Context(), Update{UpdateID: 2, Message: &Message{
		From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/status",
	}}, ui.Handle)
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.StatusOverview {
		t.Fatalf("valid injected command=%#v", dispatcher.calls)
	}

	unauthorized := Update{UpdateID: 3, CallbackQuery: &CallbackQuery{
		ID: "foreign", From: User{ID: 99}, Data: button.CallbackData,
		Message: &Message{MessageID: 7, Chat: Chat{ID: 99, Type: "private"}},
	}}
	client.ProcessUpdate(t.Context(), unauthorized, ui.Handle)
	if len(dispatcher.calls) != 1 {
		t.Fatalf("foreign injected callback dispatched=%#v", dispatcher.calls)
	}

	tamperedData := button.CallbackData[:len(button.CallbackData)-1] + "x"
	tampered := Update{UpdateID: 4, CallbackQuery: &CallbackQuery{
		ID: "tampered", From: User{ID: 42}, Data: tamperedData,
		Message: &Message{MessageID: 7, Chat: Chat{ID: 42, Type: "private"}},
	}}
	client.ProcessUpdate(t.Context(), tampered, ui.Handle)
	if len(dispatcher.calls) != 1 {
		t.Fatalf("tampered injected callback dispatched=%#v", dispatcher.calls)
	}

	valid := Update{UpdateID: 5, CallbackQuery: &CallbackQuery{
		ID: "valid", From: User{ID: 42}, Data: button.CallbackData,
		Message: &Message{MessageID: 7, Chat: Chat{ID: 42, Type: "private"}},
	}}
	client.ProcessUpdate(t.Context(), valid, ui.Handle)
	if len(dispatcher.calls) != 2 || dispatcher.calls[1].Operation != capability.StatusOverview || dispatcher.ifaces[1] != application.OperationInterfaceTelegram {
		t.Fatalf("valid injected callback=%#v interfaces=%#v", dispatcher.calls, dispatcher.ifaces)
	}
}

func TestBotAPIInjectedUpdatePreservesSetupModeHandoff(t *testing.T) {
	client := newAPIClientWithOptions("123456:test-token", time.Second, "", nil)
	setup := []int64{}
	normal := []int64{}
	runtime := &Runtime{
		setupMode: true,
		config:    config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}},
		setupHandler: func(_ context.Context, update Update) bool {
			setup = append(setup, update.UpdateID)
			return true
		},
		handler: func(_ context.Context, update Update) { normal = append(normal, update.UpdateID) },
	}
	dispatch := func(ctx context.Context, update Update) { runtime.dispatchBatch(ctx, []Update{update}) }
	client.ProcessUpdate(t.Context(), Update{UpdateID: 9, Message: &Message{
		From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/start pair",
	}}, dispatch)
	if !reflect.DeepEqual(setup, []int64{9}) || len(normal) != 0 || runtime.Health().NextOffset != 10 {
		t.Fatalf("setup=%v normal=%v health=%#v", setup, normal, runtime.Health())
	}
}

func TestBotAPIInjectedUnknownUpdateAdvancesRuntimeOffset(t *testing.T) {
	client := newAPIClientWithOptions("123456:test-token", time.Second, "", nil)
	var handled []int64
	runtime := &Runtime{
		config:  config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}},
		handler: func(_ context.Context, update Update) { handled = append(handled, update.UpdateID) },
	}
	dispatch := func(ctx context.Context, update Update) { runtime.dispatchBatch(ctx, []Update{update}) }

	client.ProcessUpdate(t.Context(), Update{UpdateID: 10}, dispatch)
	client.ProcessUpdate(t.Context(), Update{UpdateID: 11, Message: &Message{
		From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/status",
	}}, dispatch)
	if runtime.Health().NextOffset != 12 {
		t.Fatalf("next offset=%d want=12", runtime.Health().NextOffset)
	}
	if !reflect.DeepEqual(handled, []int64{11}) {
		t.Fatalf("authorized dispatches=%v want=[11]", handled)
	}
}

func TestBotAPITransportErrorsAreTypedAndSecretSafe(t *testing.T) {
	for _, test := range []struct {
		input error
		class transportErrorClass
	}{
		{fmt.Errorf("%w, bot was blocked by the user", telegrambot.ErrorForbidden), transportErrorForbidden},
		{fmt.Errorf("%w, malformed callback payload", telegrambot.ErrorBadRequest), transportErrorBadRequest},
		{fmt.Errorf("%w, token=SECRET", telegrambot.ErrorUnauthorized), transportErrorUnauthorized},
		{fmt.Errorf("%w, another poller", telegrambot.ErrorConflict), transportErrorConflict},
	} {
		err := classifyTransportError(test.input)
		if transportErrorKind(err) != test.class {
			t.Fatalf("classify %v = %v", test.input, err)
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "callback payload") || strings.Contains(err.Error(), "blocked by") {
			t.Fatalf("typed error leaked upstream detail: %q", err.Error())
		}
	}
	rate := classifyTransportError(&telegrambot.TooManyRequestsError{Message: "secret upstream body", RetryAfter: 3})
	var typed *transportError
	if !errors.As(rate, &typed) || typed.Class != transportErrorRateLimited || typed.RetryAfter != 3*time.Second {
		t.Fatalf("rate limit=%#v", rate)
	}
}

func TestBotAPIAdapterUsesTypedMessageEditAndCallbackMethods(t *testing.T) {
	type call struct {
		method string
		form   map[string]string
	}
	calls := make(chan call, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.ToLower(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		form := map[string]string{}
		if method != "getme" {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse %s form: %v", method, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for _, key := range []string{"chat_id", "message_id", "text", "parse_mode", "rich_message", "reply_markup", "callback_query_id", "show_alert"} {
				if value := r.FormValue(key); value != "" {
					form[key] = value
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getme":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": 1000, "is_bot": true, "first_name": "CodeMCP", "username": "codemcp_bot"}})
		case "sendrichmessage", "editmessagetext":
			calls <- call{method: method, form: form}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 9, "date": 0, "chat": map[string]any{"id": 42, "type": "private"}}})
		case "answercallbackquery":
			calls <- call{method: method, form: form}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAPIClientWithOptions("123456:test-token", time.Second, server.URL, server.Client())
	identity, err := client.GetMe(t.Context())
	if err != nil || identity.ID != 1000 || identity.Username != "codemcp_bot" {
		t.Fatalf("getMe identity=%#v err=%v", identity, err)
	}
	screen := Screen{Text: "Hello", HTML: "<b>Hello</b>", Keyboard: [][]Button{{{Text: "Status", CallbackData: "signed"}}}}
	if err := client.SendScreen(t.Context(), 42, screen); err != nil {
		t.Fatal(err)
	}
	if err := client.EditScreen(t.Context(), 42, 9, screen); err != nil {
		t.Fatal(err)
	}
	if err := client.AnswerCallback(t.Context(), "callback-1", "Done", true); err != nil {
		t.Fatal(err)
	}

	seen := map[string]map[string]string{}
	for range 3 {
		item := <-calls
		seen[item.method] = item.form
	}
	for _, method := range []string{"sendrichmessage", "editmessagetext", "answercallbackquery"} {
		if seen[method] == nil {
			t.Fatalf("typed method %s was not called: %#v", method, seen)
		}
	}
	if seen["sendrichmessage"]["chat_id"] != "42" || seen["sendrichmessage"]["rich_message"] == "" || !strings.Contains(seen["sendrichmessage"]["reply_markup"], "signed") {
		t.Fatalf("sendRichMessage form=%#v", seen["sendrichmessage"])
	}
	if seen["editmessagetext"]["message_id"] != "9" || seen["editmessagetext"]["text"] != "<b>Hello</b>" || seen["editmessagetext"]["parse_mode"] != "HTML" || seen["editmessagetext"]["rich_message"] == "" {
		t.Fatalf("editMessageText form=%#v", seen["editmessagetext"])
	}
	if seen["answercallbackquery"]["callback_query_id"] != "callback-1" || seen["answercallbackquery"]["show_alert"] != "true" {
		t.Fatalf("answerCallbackQuery form=%#v", seen["answercallbackquery"])
	}
}

func TestBotAPICanonicalizesLegacyScreenAndPlainTextAsRichHTML(t *testing.T) {
	type call struct {
		method string
		form   map[string]string
	}
	calls := make(chan call, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.ToLower(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		form := map[string]string{}
		for _, key := range []string{"chat_id", "text", "parse_mode", "rich_message"} {
			if value := r.FormValue(key); value != "" {
				form[key] = value
			}
		}
		calls <- call{method: method, form: form}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "sendrichmessage":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 11, "date": 1, "chat": map[string]any{"id": 42, "type": "private"}}})
		case "sendmessage":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 12, "date": 1, "chat": map[string]any{"id": 42, "type": "private"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAPIClientWithOptions("123456:test-token", time.Second, server.URL, server.Client())
	legacy := Present(ProductHeader("CodeMCP", "Telegram"), TitleBlock("Status", "Ready"))
	if err := client.SendScreen(t.Context(), 42, Screen{Text: legacy.Text, HTML: legacy.HTML}); err != nil {
		t.Fatal(err)
	}
	if err := client.SendMessage(t.Context(), 42, "<unsafe>&"); err != nil {
		t.Fatal(err)
	}

	seen := make([]call, 0, 2)
	for range 2 {
		seen = append(seen, <-calls)
	}
	if seen[0].method != "sendrichmessage" || seen[0].form["rich_message"] == "" {
		t.Fatalf("legacy screen did not use canonical rich HTML: %#v", seen[0])
	}
	var rich struct {
		HTML string `json:"html"`
	}
	if seen[1].method != "sendrichmessage" || json.Unmarshal([]byte(seen[1].form["rich_message"]), &rich) != nil || rich.HTML != "&lt;unsafe&gt;&amp;" {
		t.Fatalf("plain text was not normalized to canonical rich HTML: %#v", seen[1])
	}
}

func TestBotAPIAdapterUsesTypedCommandMenuAndDeleteMethods(t *testing.T) {
	type call struct {
		method string
		form   map[string]string
	}
	calls := make(chan call, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.ToLower(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse %s form: %v", method, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		form := map[string]string{}
		for _, key := range []string{"commands", "chat_id", "menu_button", "message_id"} {
			if value := r.FormValue(key); value != "" {
				form[key] = value
			}
		}
		calls <- call{method: method, form: form}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getchatmenubutton":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"type": "commands"}})
		case "setmycommands", "setchatmenubutton", "deletemessage":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAPIClientWithOptions("123456:test-token", time.Second, server.URL, server.Client())
	if err := client.SetCommands(t.Context(), Commands()); err != nil {
		t.Fatal(err)
	}
	if err := client.SetChatMenuButton(t.Context(), 42, MenuButton{Type: MenuButtonCommands}); err != nil {
		t.Fatal(err)
	}
	button, err := client.GetChatMenuButton(t.Context(), 42)
	if err != nil || button.Type != MenuButtonCommands {
		t.Fatalf("menu button=%#v err=%v", button, err)
	}
	if err := client.DeleteMessage(t.Context(), 42, 9); err != nil {
		t.Fatal(err)
	}

	seen := map[string]map[string]string{}
	for range 4 {
		item := <-calls
		seen[item.method] = item.form
	}
	if !strings.Contains(seen["setmycommands"]["commands"], `"command":"status"`) || !strings.Contains(seen["setmycommands"]["commands"], `"command":"help"`) {
		t.Fatalf("setMyCommands form=%#v", seen["setmycommands"])
	}
	if seen["setchatmenubutton"]["chat_id"] != "42" || !strings.Contains(seen["setchatmenubutton"]["menu_button"], `"type":"commands"`) || strings.Contains(seen["setchatmenubutton"]["menu_button"], "web_app") {
		t.Fatalf("setChatMenuButton form=%#v", seen["setchatmenubutton"])
	}
	if seen["getchatmenubutton"]["chat_id"] != "42" {
		t.Fatalf("getChatMenuButton form=%#v", seen["getchatmenubutton"])
	}
	if seen["deletemessage"]["chat_id"] != "42" || seen["deletemessage"]["message_id"] != "9" {
		t.Fatalf("deleteMessage form=%#v", seen["deletemessage"])
	}
}

func TestBotAPIClearReplyKeyboardRemovesStaleNativePicker(t *testing.T) {
	type call struct {
		method string
		form   map[string]string
	}
	calls := make(chan call, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.ToLower(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse %s form: %v", method, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		form := map[string]string{}
		for _, key := range []string{"chat_id", "text", "disable_notification", "reply_markup", "message_id"} {
			if value := r.FormValue(key); value != "" {
				form[key] = value
			}
		}
		calls <- call{method: method, form: form}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "sendmessage":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{
				"message_id": 77,
				"date":       1,
				"chat":       map[string]any{"id": 42, "type": "private"},
			}})
		case "deletemessage":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAPIClientWithOptions("123456:test-token", time.Second, server.URL, server.Client())
	if err := client.ClearReplyKeyboard(t.Context(), 42); err != nil {
		t.Fatal(err)
	}

	seen := map[string]map[string]string{}
	for range 2 {
		item := <-calls
		seen[item.method] = item.form
	}
	if seen["sendmessage"]["chat_id"] != "42" ||
		seen["sendmessage"]["disable_notification"] != "true" ||
		!strings.Contains(seen["sendmessage"]["reply_markup"], `"remove_keyboard":true`) {
		t.Fatalf("reply keyboard cleanup form=%#v", seen["sendmessage"])
	}
	if seen["deletemessage"]["chat_id"] != "42" || seen["deletemessage"]["message_id"] != "77" {
		t.Fatalf("reply keyboard cleanup carrier was not deleted: %#v", seen["deletemessage"])
	}
}

func TestTelegramFileTransferBoundaryIsEnforcedBeforeTransport(t *testing.T) {
	for _, size := range []int64{0, 1, MaxFileTransferBytes} {
		if err := validateTelegramFileSize(size); err != nil {
			t.Fatalf("size %d rejected: %v", size, err)
		}
	}
	for _, size := range []int64{-1, MaxFileTransferBytes + 1} {
		if err := validateTelegramFileSize(size); err == nil {
			t.Fatalf("size %d was not rejected", size)
		}
	}
}
