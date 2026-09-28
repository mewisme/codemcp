package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/notification"
)

func TestDeliveryRetriesOnceFromTypedRetryAfterWithoutChangingPollingHealth(t *testing.T) {
	runtime := &Runtime{
		deliveryInterval: 0,
		health:           Health{Running: true, PollingHealthy: true},
	}
	calls := 0
	err := runtime.deliver(t.Context(), func() error {
		calls++
		if calls == 1 {
			return &transportError{Class: transportErrorRateLimited, RetryAfter: time.Millisecond}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("delivery calls=%d want=2", calls)
	}
	health := runtime.Health()
	if health.DeliveryDegraded || health.DeliveryRateLimited || health.DeliveryRetryAfterMS != 0 || health.DeliveryFailures != 1 {
		t.Fatalf("delivery health after retry=%#v", health)
	}
	if !health.PollingHealthy || !health.Running {
		t.Fatalf("delivery retry changed polling health=%#v", health)
	}
}

func TestDeliveryLimiterHonorsContextBeforeOutboundSend(t *testing.T) {
	runtime := &Runtime{
		deliveryInterval: time.Hour,
		deliveryNext:     time.Now().Add(time.Hour),
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	err := runtime.deliver(ctx, func() error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("delivery error=%v", err)
	}
	if calls != 0 {
		t.Fatalf("rate-limited delivery reached transport %d time(s)", calls)
	}
}

type staleEditAPI struct {
	sendCalls int
	editCalls int
}

func (api *staleEditAPI) GetMe(context.Context) (User, error)              { return User{ID: 1}, nil }
func (api *staleEditAPI) SendMessage(context.Context, int64, string) error { return nil }
func (api *staleEditAPI) SendScreen(context.Context, int64, Screen) error {
	api.sendCalls++
	return nil
}
func (api *staleEditAPI) EditScreen(context.Context, int64, int64, Screen) error {
	api.editCalls++
	return &transportError{Class: transportErrorStaleMessage}
}
func (api *staleEditAPI) AnswerCallback(context.Context, string, string, bool) error { return nil }

func TestStaleEditFallsBackToReplacementMessage(t *testing.T) {
	api := &staleEditAPI{}
	runtime := &Runtime{api: api, deliveryInterval: 0, health: Health{Running: true}}
	if err := runtime.EditScreen(t.Context(), 42, 7, Screen{Text: "replacement"}); err != nil {
		t.Fatal(err)
	}
	if api.editCalls != 1 || api.sendCalls != 1 {
		t.Fatalf("edit=%d replacement=%d", api.editCalls, api.sendCalls)
	}
}

type recipientFailureAPI struct {
	mu   sync.Mutex
	sent []int64
	fail int64
}

func (api *recipientFailureAPI) GetMe(context.Context) (User, error) { return User{ID: 1}, nil }
func (api *recipientFailureAPI) SendMessage(_ context.Context, chatID int64, _ string) error {
	api.mu.Lock()
	api.sent = append(api.sent, chatID)
	api.mu.Unlock()
	if chatID == api.fail {
		return &transportError{Class: transportErrorForbidden}
	}
	return nil
}

func TestNotificationDeliveryFailureIsIsolatedPerRecipient(t *testing.T) {
	api := &recipientFailureAPI{fail: 7}
	runtime := &Runtime{
		api:              api,
		deliveryInterval: 0,
		config:           config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{7, 8}},
		health:           Health{Running: true, Enabled: true, AuthorizationConfigured: true, PollingHealthy: true},
	}
	err := runtime.SendNotification(t.Context(), notification.Message{Title: "Done"})
	if transportErrorKind(err) != transportErrorForbidden {
		t.Fatalf("notification error=%v", err)
	}
	api.mu.Lock()
	sent := append([]int64(nil), api.sent...)
	api.mu.Unlock()
	if len(sent) != 2 || sent[0] != 7 || sent[1] != 8 {
		t.Fatalf("recipients=%v want=[7 8]", sent)
	}
	health := runtime.Health()
	if !health.Running || !health.PollingHealthy || !health.DeliveryDegraded || health.DeliveryFailures != 1 {
		t.Fatalf("health=%#v", health)
	}
}

type partialNavigationAPI struct {
	*fakeAPI
	mu          sync.Mutex
	commands    []Command
	menus       map[int64]MenuButton
	failMenuFor int64
	setMenus    []int64
}

func (api *partialNavigationAPI) GetCommands(context.Context) ([]Command, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]Command(nil), api.commands...), nil
}
func (api *partialNavigationAPI) SetCommands(_ context.Context, commands []Command) error {
	api.mu.Lock()
	api.commands = append([]Command(nil), commands...)
	api.mu.Unlock()
	return nil
}
func (api *partialNavigationAPI) GetChatMenuButton(_ context.Context, chatID int64) (MenuButton, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	button, ok := api.menus[chatID]
	if !ok {
		return MenuButton{}, errors.New("missing menu")
	}
	return button, nil
}
func (api *partialNavigationAPI) SetChatMenuButton(_ context.Context, chatID int64, button MenuButton) error {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.setMenus = append(api.setMenus, chatID)
	if chatID == api.failMenuFor {
		return errors.New("menu sync failed")
	}
	if api.menus == nil {
		api.menus = map[int64]MenuButton{}
	}
	api.menus[chatID] = button
	return nil
}

func TestNavigationReconciliationReportsDriftAndContinuesAfterPartialFailure(t *testing.T) {
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	api := &partialNavigationAPI{
		fakeAPI: &fakeAPI{results: []fakePollResult{{updates: nil}}},
		menus: map[int64]MenuButton{
			42: {Type: MenuButtonDefault},
			43: {Type: MenuButtonDefault},
		},
		failMenuFor: 42,
	}
	runtime := NewRuntime(Options{
		Root: root, Factory: func(string) API { return api }, PollTimeout: time.Millisecond,
		ReconnectDelay: func(int) time.Duration { return time.Millisecond }, StopTimeout: 100 * time.Millisecond,
		DeliveryInterval: -1,
	})
	defer runtime.Stop()
	if err := runtime.Reconcile(t.Context(), config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42, 43}}); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	setMenus := append([]int64(nil), api.setMenus...)
	menu43 := api.menus[43]
	api.mu.Unlock()
	if len(setMenus) != 2 || setMenus[0] != 42 || setMenus[1] != 43 || menu43.Type != MenuButtonCommands {
		t.Fatalf("partial navigation reconciliation menus=%v menu43=%#v", setMenus, menu43)
	}
	health := runtime.Health()
	if !health.CommandsPublished || health.CommandDrift || health.MenuReconciled || health.MenuDriftCount == 0 || health.NavigationLastError == "" {
		t.Fatalf("navigation health=%#v", health)
	}
}

func TestDiagnosticsExposeCompatibilityWithoutEphemeralMiniAppURL(t *testing.T) {
	runtime := &Runtime{
		api: &apiClient{},
		health: Health{
			Running:        true,
			AllowedUpdates: []string{"message", "callback_query"},
			LogsMiniApp: LogsMiniAppHealth{
				Enabled: true, State: MiniAppReady, DependencyAvailable: true,
				ListenerReady: true, TunnelRunning: true, PublicIngressReady: true,
				PublicURL: "https://ephemeral.trycloudflare.com/",
			},
		},
	}
	runtime.logsMiniApp = &LogsMiniAppRuntime{health: runtime.health.LogsMiniApp}
	diagnostics := runtime.Diagnostics()
	if diagnostics.LogsMiniApp.PublicURL != "" {
		t.Fatalf("diagnostics leaked ephemeral public URL: %#v", diagnostics.LogsMiniApp)
	}
	if diagnostics.TransportLibrary != "github.com/go-telegram/bot" || len(diagnostics.AllowedUpdates) != 2 || !diagnostics.RichMessageSupported || diagnostics.MaxFileTransferBytes != MaxFileTransferBytes {
		t.Fatalf("transport diagnostics=%#v", diagnostics)
	}
	if !diagnostics.LogsMiniApp.ListenerReady || !diagnostics.LogsMiniApp.TunnelRunning || !diagnostics.LogsMiniApp.PublicIngressReady {
		t.Fatalf("mini app diagnostics=%#v", diagnostics.LogsMiniApp)
	}
}
