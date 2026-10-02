package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/telegram"
)

type pairingTestAPI struct {
	mu      sync.Mutex
	updates chan []telegram.Update
	sent    []int64
}

func (api *pairingTestAPI) GetMe(context.Context) (telegram.User, error) {
	return telegram.User{ID: 1000, Username: "codemcp_test_bot", FirstName: "CodeMCP"}, nil
}

func (api *pairingTestAPI) GetUpdates(ctx context.Context, _ int64, _ int, _ time.Duration) ([]telegram.Update, error) {
	select {
	case updates := <-api.updates:
		return updates, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (api *pairingTestAPI) SendMessage(_ context.Context, chatID int64, _ string) error {
	api.mu.Lock()
	api.sent = append(api.sent, chatID)
	api.mu.Unlock()
	return nil
}

func TestTelegramPairingPromotesSetupToAuthorizedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "test-mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "test-admin-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	api := &pairingTestAPI{updates: make(chan []telegram.Update, 2)}
	runtime := telegram.NewRuntime(telegram.Options{
		Root:           root,
		Factory:        func(string) telegram.API { return api },
		PollTimeout:    time.Millisecond,
		ReconnectDelay: func(int) time.Duration { return time.Millisecond },
		StopTimeout:    100 * time.Millisecond,
	})
	application := &App{
		Config:          config.NewRuntimeStore(cfg),
		Telegram:        runtime,
		TelegramPairing: telegram.NewPairingStore(root),
	}
	runtime.SetSetupHandler(application.handleTelegramPairingUpdate)
	t.Cleanup(runtime.Stop)

	if _, err := application.TelegramSetToken(t.Context(), "validated-token"); err != nil {
		t.Fatal(err)
	}
	challenge, err := application.PrepareTelegramPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Code == "" || challenge.DeepLink == "" {
		t.Fatalf("challenge=%#v", challenge)
	}
	health := runtime.Health()
	if !health.Running || !health.SetupMode || health.AuthorizationConfigured || runtime.Available() {
		t.Fatalf("setup health=%#v available=%t", health, runtime.Available())
	}

	api.updates <- []telegram.Update{{
		UpdateID: 1,
		Message: &telegram.Message{
			From: &telegram.User{ID: 42},
			Chat: telegram.Chat{ID: 42, Type: "private"},
			Text: "/start " + challenge.Code,
		},
	}}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current := application.Config.Snapshot()
		if current.Telegram.Enabled && len(current.Telegram.AllowedUserIDs) == 1 && current.Telegram.AllowedUserIDs[0] == 42 && runtime.Available() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	current := application.Config.Snapshot()
	if !current.Telegram.Enabled || len(current.Telegram.AllowedUserIDs) != 1 || current.Telegram.AllowedUserIDs[0] != 42 {
		t.Fatalf("runtime config=%#v", current.Telegram)
	}
	if health := runtime.Health(); health.SetupMode || !health.AuthorizationConfigured || !health.Running {
		t.Fatalf("promoted health=%#v", health)
	}
	persisted, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Telegram.Enabled || len(persisted.Telegram.AllowedUserIDs) != 1 || persisted.Telegram.AllowedUserIDs[0] != 42 {
		t.Fatalf("persisted telegram config=%#v", persisted.Telegram)
	}
	state, err := application.TelegramPairingStatus()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != telegram.PairingStatusPaired || state.ChallengeHash != "" {
		t.Fatalf("pairing state=%#v", state)
	}

	if err := application.TelegramLogout(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	current = application.Config.Snapshot()
	if current.Telegram.Enabled || len(current.Telegram.AllowedUserIDs) != 0 {
		t.Fatalf("logout config=%#v", current.Telegram)
	}
	if runtime.Health().Running || runtime.Available() {
		t.Fatalf("runtime remained active after last-user logout: %#v", runtime.Health())
	}
	persisted, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Telegram.Enabled || len(persisted.Telegram.AllowedUserIDs) != 0 {
		t.Fatalf("persisted logout config=%#v", persisted.Telegram)
	}
}

func TestTelegramPairingRejectsWrongCodeWithoutAuthorization(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "test-mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "test-admin-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	api := &pairingTestAPI{updates: make(chan []telegram.Update, 1)}
	runtime := telegram.NewRuntime(telegram.Options{
		Root:           root,
		Factory:        func(string) telegram.API { return api },
		PollTimeout:    time.Millisecond,
		ReconnectDelay: func(int) time.Duration { return time.Millisecond },
	})
	application := &App{Config: config.NewRuntimeStore(cfg), Telegram: runtime, TelegramPairing: telegram.NewPairingStore(root)}
	runtime.SetSetupHandler(application.handleTelegramPairingUpdate)
	t.Cleanup(runtime.Stop)

	if _, err := application.TelegramSetToken(t.Context(), "validated-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := application.PrepareTelegramPairing(t.Context()); err != nil {
		t.Fatal(err)
	}
	api.updates <- []telegram.Update{{
		UpdateID: 1,
		Message: &telegram.Message{
			From: &telegram.User{ID: 99},
			Chat: telegram.Chat{ID: 99, Type: "private"},
			Text: "/start ABCD-EFGH",
		},
	}}

	time.Sleep(20 * time.Millisecond)
	current := application.Config.Snapshot()
	if !current.Telegram.Enabled || len(current.Telegram.AllowedUserIDs) != 0 {
		t.Fatalf("wrong code changed setup authorization: %#v", current.Telegram)
	}
	if runtime.Available() {
		t.Fatal("setup runtime became normally available after invalid pairing")
	}
}
