package telegram

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/notification"
)

type fakePollResult struct {
	updates []Update
	err     error
}

type fakeAPI struct {
	mu         sync.Mutex
	me         User
	getMeErr   error
	getMeCalls int
	results    []fakePollResult
	offsets    []int64
	limits     []int
	sent       []int64
	sendErr    error
	successes  chan struct{}
}

type navigationFakeAPI struct {
	*fakeAPI
	commands []Command
	menus    map[int64]MenuButton
}

func (api *navigationFakeAPI) SetCommands(_ context.Context, commands []Command) error {
	api.commands = append([]Command(nil), commands...)
	return nil
}

func (api *navigationFakeAPI) SetChatMenuButton(_ context.Context, chatID int64, button MenuButton) error {
	if api.menus == nil {
		api.menus = map[int64]MenuButton{}
	}
	api.menus[chatID] = button
	return nil
}

func (api *navigationFakeAPI) GetChatMenuButton(_ context.Context, chatID int64) (MenuButton, error) {
	button, ok := api.menus[chatID]
	if !ok {
		return MenuButton{}, errors.New("missing menu button")
	}
	return button, nil
}

func (api *fakeAPI) GetMe(context.Context) (User, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.getMeCalls++
	if api.getMeErr != nil {
		return User{}, api.getMeErr
	}
	if api.me.ID > 0 {
		return api.me, nil
	}
	return User{ID: 1000, Username: "codemcp_test_bot", FirstName: "CodeMCP"}, nil
}

func (api *fakeAPI) GetUpdates(ctx context.Context, offset int64, limit int, _ time.Duration) ([]Update, error) {
	api.mu.Lock()
	api.offsets = append(api.offsets, offset)
	api.limits = append(api.limits, limit)
	if len(api.results) > 0 {
		result := api.results[0]
		api.results = api.results[1:]
		api.mu.Unlock()
		if result.err == nil && api.successes != nil {
			select {
			case api.successes <- struct{}{}:
			default:
			}
		}
		return result.updates, result.err
	}
	api.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (api *fakeAPI) SendMessage(_ context.Context, chatID int64, _ string) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.sent = append(api.sent, chatID)
	return api.sendErr
}

func (api *fakeAPI) snapshot() ([]int64, []int, []int64) {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]int64(nil), api.offsets...), append([]int(nil), api.limits...), append([]int64(nil), api.sent...)
}

func testRuntime(t *testing.T, api *fakeAPI, cfg config.TelegramConfig) *Runtime {
	t.Helper()
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{
		Root:           root,
		Factory:        func(string) API { return api },
		PollTimeout:    time.Millisecond,
		ReconnectDelay: func(int) time.Duration { return time.Millisecond },
		StopTimeout:    100 * time.Millisecond,
	})
	runtime.Reconcile(t.Context(), cfg)
	t.Cleanup(runtime.Stop)
	return runtime
}

func TestRuntimeAuthorizesEveryMessageAndCallbackBeforeDispatch(t *testing.T) {
	const allowed = int64(42)
	api := &fakeAPI{results: []fakePollResult{{updates: []Update{
		{UpdateID: 1, Message: &Message{From: &User{ID: allowed}, Chat: Chat{ID: allowed, Type: "private"}, Text: "/status"}},
		{UpdateID: 2, Message: &Message{From: &User{ID: 99}, Chat: Chat{ID: 99, Type: "private"}, Text: "/status"}},
		{UpdateID: 3, Message: &Message{From: &User{ID: allowed}, Chat: Chat{ID: -100, Type: "group"}, Text: "/status"}},
		{UpdateID: 4, CallbackQuery: &CallbackQuery{From: User{ID: allowed}, Message: &Message{Chat: Chat{ID: allowed, Type: "private"}}, Data: "admin"}},
		{UpdateID: 5, CallbackQuery: &CallbackQuery{From: User{ID: 99}, Message: &Message{Chat: Chat{ID: 99, Type: "private"}}, Data: "admin"}},
		{UpdateID: 6, CallbackQuery: &CallbackQuery{From: User{ID: allowed}, Message: &Message{Chat: Chat{ID: -100, Type: "group"}}, Data: "admin"}},
	}}}}
	var (
		mu      sync.Mutex
		handled []int64
	)
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{
		Root: root, Factory: func(string) API { return api },
		PollTimeout: time.Millisecond, ReconnectDelay: func(int) time.Duration { return time.Millisecond },
	})
	runtime.SetHandler(func(_ context.Context, update Update) {
		mu.Lock()
		handled = append(handled, update.UpdateID)
		mu.Unlock()
	})
	runtime.Reconcile(t.Context(), config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{allowed}})
	defer runtime.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(handled)
		mu.Unlock()
		if count == 2 && runtime.Health().NextOffset == 7 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	got := append([]int64(nil), handled...)
	mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Fatalf("authorized dispatches=%v want=[1 4]", got)
	}
	if health := runtime.Health(); health.NextOffset != 7 {
		t.Fatalf("next offset=%d want=7", health.NextOffset)
	}
}

func TestSetupModeNeverFallsThroughToNormalHandler(t *testing.T) {
	const allowed = int64(42)
	api := &fakeAPI{results: []fakePollResult{{updates: []Update{
		{UpdateID: 1, Message: &Message{From: &User{ID: allowed}, Chat: Chat{ID: allowed, Type: "private"}, Text: "/status"}},
	}}}}
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{
		Root: root, Factory: func(string) API { return api },
		PollTimeout: time.Millisecond, ReconnectDelay: func(int) time.Duration { return time.Millisecond },
	})
	setup := make(chan Update, 1)
	normal := make(chan Update, 1)
	runtime.SetSetupHandler(func(_ context.Context, update Update) bool {
		setup <- update
		return false
	})
	runtime.SetHandler(func(_ context.Context, update Update) { normal <- update })
	if err := runtime.StartSetup(t.Context(), config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{allowed}}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Stop()
	select {
	case update := <-setup:
		if update.UpdateID != 1 {
			t.Fatalf("setup update=%d", update.UpdateID)
		}
	case <-time.After(time.Second):
		t.Fatal("setup handler did not receive update")
	}
	select {
	case update := <-normal:
		t.Fatalf("normal handler received setup-mode update %d", update.UpdateID)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRuntimePollingFailureSelfHealsWithoutRestart(t *testing.T) {
	api := &fakeAPI{
		results: []fakePollResult{
			{err: errors.New("temporary network failure")},
			{updates: []Update{{UpdateID: 9, Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}}}}},
		},
		successes: make(chan struct{}, 1),
	}
	handled := make(chan Update, 1)
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{
		Root: root, Factory: func(string) API { return api },
		PollTimeout: time.Millisecond, ReconnectDelay: func(int) time.Duration { return time.Millisecond },
	})
	runtime.SetHandler(func(_ context.Context, update Update) { handled <- update })
	runtime.Reconcile(t.Context(), config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}})
	defer runtime.Stop()

	select {
	case update := <-handled:
		if update.UpdateID != 9 {
			t.Fatalf("update=%d", update.UpdateID)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not recover from transient polling failure")
	}
	health := runtime.Health()
	if !health.Running || !health.PollingHealthy || health.Reconnecting || health.ReconnectCount != 1 || health.LastError != "" {
		t.Fatalf("health after recovery=%#v", health)
	}
	offsets, limits, _ := api.snapshot()
	if len(offsets) < 2 || offsets[0] != 0 || offsets[1] != 0 {
		t.Fatalf("poll offsets=%v", offsets)
	}
	for _, limit := range limits {
		if limit != maxUpdatesPerPoll {
			t.Fatalf("poll limit=%d want=%d", limit, maxUpdatesPerPoll)
		}
	}
}

func TestRuntimeOffsetReplayAndBatchBound(t *testing.T) {
	updates := make([]Update, 0, 150)
	for id := int64(1); id <= 150; id++ {
		updates = append(updates, Update{UpdateID: id, Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}}})
	}
	api := &fakeAPI{results: []fakePollResult{{updates: updates}, {updates: []Update{
		{UpdateID: 99, Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}}},
		{UpdateID: 101, Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}}},
	}}}}
	var count int
	var mu sync.Mutex
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{Root: root, Factory: func(string) API { return api }, PollTimeout: time.Millisecond})
	runtime.SetHandler(func(context.Context, Update) {
		mu.Lock()
		count++
		mu.Unlock()
	})
	runtime.Reconcile(t.Context(), config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}})
	defer runtime.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && runtime.Health().NextOffset < 102 {
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	got := count
	mu.Unlock()
	if got != 101 {
		t.Fatalf("dispatch count=%d want=101", got)
	}
	if runtime.Health().NextOffset != 102 {
		t.Fatalf("next offset=%d want=102", runtime.Health().NextOffset)
	}
	offsets, _, _ := api.snapshot()
	if len(offsets) < 2 || offsets[1] != 101 {
		t.Fatalf("second poll offset=%v want second=101", offsets)
	}
}

func TestRuntimeNotificationAvailabilityAndAuthorizedRecipients(t *testing.T) {
	api := &fakeAPI{results: []fakePollResult{{updates: nil}}}
	runtime := testRuntime(t, api, config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{7, 8}})
	deadline := time.Now().Add(time.Second)
	for !runtime.Available() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !runtime.Available() {
		t.Fatalf("runtime health=%#v", runtime.Health())
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{Title: "Done", Body: "Finished"}); err != nil {
		t.Fatal(err)
	}
	_, _, recipients := api.snapshot()
	if len(recipients) != 2 || recipients[0] != 7 || recipients[1] != 8 {
		t.Fatalf("recipients=%v", recipients)
	}
}

func TestRuntimeBlockedRecipientDoesNotCrashPolling(t *testing.T) {
	api := &fakeAPI{results: []fakePollResult{{updates: nil}}, sendErr: &transportError{Class: transportErrorForbidden}}
	runtime := testRuntime(t, api, config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{7}})
	deadline := time.Now().Add(time.Second)
	for !runtime.Available() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !runtime.Available() {
		t.Fatalf("runtime health=%#v", runtime.Health())
	}
	if err := runtime.SendNotification(t.Context(), notification.Message{Title: "Done"}); transportErrorKind(err) != transportErrorForbidden {
		t.Fatalf("notification err=%v", err)
	}
	health := runtime.Health()
	if !health.Running || !health.PollingHealthy || health.Reconnecting {
		t.Fatalf("blocked delivery changed polling health=%#v", health)
	}
}

func TestRuntimeReconcilesCommandRegistryAndPrivateMenuButtons(t *testing.T) {
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	api := &navigationFakeAPI{fakeAPI: &fakeAPI{results: []fakePollResult{{updates: nil}}}, menus: map[int64]MenuButton{}}
	runtime := NewRuntime(Options{
		Root: root, Factory: func(string) API { return api }, PollTimeout: time.Millisecond,
		ReconnectDelay: func(int) time.Duration { return time.Millisecond }, StopTimeout: 100 * time.Millisecond,
	})
	defer runtime.Stop()
	if err := runtime.Reconcile(t.Context(), config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42, 43}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(api.commands, Commands()) {
		t.Fatalf("published commands=%#v want=%#v", api.commands, Commands())
	}
	for _, userID := range []int64{42, 43} {
		if button := api.menus[userID]; button.Type != MenuButtonCommands {
			t.Fatalf("menu for %d=%#v", userID, button)
		}
	}
	if err := runtime.SetChatMenuButton(t.Context(), 99, MenuButton{Type: MenuButtonDefault}); err == nil {
		t.Fatal("unauthorized user changed Telegram chat menu")
	}
	if err := runtime.SetChatMenuButton(t.Context(), 42, MenuButton{Type: MenuButtonDefault}); err != nil {
		t.Fatal(err)
	}
	button, err := runtime.GetChatMenuButton(t.Context(), 42)
	if err != nil || button.Type != MenuButtonDefault {
		t.Fatalf("authorized menu=%#v err=%v", button, err)
	}
}

func TestTokenIsSecretStoreStateNotRuntimeConfig(t *testing.T) {
	root := t.TempDir()
	if configured, err := TokenConfigured(root); err != nil || configured {
		t.Fatalf("initial configured=%t err=%v", configured, err)
	}
	if err := SetToken(root, "SECRET_BOT_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if configured, err := TokenConfigured(root); err != nil || !configured {
		t.Fatalf("configured=%t err=%v", configured, err)
	}
	if err := ClearToken(root); err != nil {
		t.Fatal(err)
	}
	if configured, err := TokenConfigured(root); err != nil || configured {
		t.Fatalf("configured after clear=%t err=%v", configured, err)
	}
}

func TestRuntimeValidatesTokenBeforePolling(t *testing.T) {
	api := &fakeAPI{getMeErr: errors.New("unauthorized")}
	root := t.TempDir()
	if err := SetToken(root, "invalid-token"); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{Root: root, Factory: func(string) API { return api }})
	err := runtime.Reconcile(t.Context(), config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}})
	if err == nil {
		t.Fatal("expected token validation failure")
	}
	health := runtime.Health()
	if health.Running || health.PollingHealthy || health.LastError != "telegram bot token validation failed" {
		t.Fatalf("health=%#v", health)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.getMeCalls != 1 || len(api.offsets) != 0 {
		t.Fatalf("getMe calls=%d poll offsets=%v", api.getMeCalls, api.offsets)
	}
}

func TestSetValidatedTokenRejectsBeforePersisting(t *testing.T) {
	root := t.TempDir()
	api := &fakeAPI{getMeErr: errors.New("unauthorized")}
	runtime := NewRuntime(Options{Root: root, Factory: func(string) API { return api }})
	if _, err := runtime.SetValidatedToken(t.Context(), "bad-token"); err == nil {
		t.Fatal("expected token validation failure")
	}
	configured, err := TokenConfigured(root)
	if err != nil || configured {
		t.Fatalf("configured=%t err=%v", configured, err)
	}
}
