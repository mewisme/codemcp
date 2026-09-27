package app

import (
	"context"
	"errors"
	"slices"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/telegram"
)

func (a *App) TelegramSetToken(ctx context.Context, token string) (telegram.BotIdentity, error) {
	if a == nil || a.Telegram == nil || a.Config == nil {
		return telegram.BotIdentity{}, errors.New("telegram runtime is unavailable")
	}
	identity, err := a.Telegram.SetValidatedToken(ctx, token)
	if err != nil {
		return telegram.BotIdentity{}, err
	}
	if a.running {
		if err := a.Telegram.Reconcile(ctx, a.Config.Snapshot().Telegram); err != nil {
			return telegram.BotIdentity{}, err
		}
	}
	return identity, nil
}

func (a *App) TelegramClearToken() error {
	if a == nil || a.Telegram == nil {
		return errors.New("telegram runtime is unavailable")
	}
	a.Telegram.Stop()
	return telegram.ClearToken(config.RootPath())
}

func (a *App) PrepareTelegramPairing(ctx context.Context) (telegram.PairingChallenge, error) {
	if a == nil || a.Telegram == nil || a.TelegramPairing == nil || a.Config == nil {
		return telegram.PairingChallenge{}, errors.New("telegram pairing is unavailable")
	}
	identity, err := a.Telegram.ValidateStoredToken(ctx)
	if err != nil {
		return telegram.PairingChallenge{}, err
	}
	challenge, err := a.TelegramPairing.Create(identity)
	if err != nil {
		return telegram.PairingChallenge{}, err
	}
	if err := a.Telegram.StartSetup(ctx, a.Config.Snapshot().Telegram); err != nil {
		_ = a.TelegramPairing.Cancel(challenge.Generation)
		return telegram.PairingChallenge{}, err
	}
	return challenge, nil
}

func (a *App) TelegramPairingStatus() (telegram.PairingState, error) {
	if a == nil || a.TelegramPairing == nil {
		return telegram.PairingState{}, errors.New("telegram pairing is unavailable")
	}
	return a.TelegramPairing.Status()
}

func (a *App) TelegramPairingCancel(generation string) error {
	if a == nil || a.TelegramPairing == nil || a.Telegram == nil || a.Config == nil {
		return errors.New("telegram pairing is unavailable")
	}
	if err := a.TelegramPairing.Cancel(generation); err != nil {
		return err
	}
	cfg := a.Config.Snapshot().Telegram
	if cfg.Enabled && len(cfg.AllowedUserIDs) > 0 {
		return a.Telegram.Reconcile(a.runtimeCtx, cfg)
	}
	a.Telegram.Stop()
	return nil
}

func (a *App) TelegramLogout(ctx context.Context, userID int64) error {
	if a == nil || a.Config == nil || a.Telegram == nil || userID <= 0 {
		return errors.New("telegram authorized user is required")
	}
	previous := a.Config.Snapshot()
	next, err := application.SetTelegramAuthorizedUser(ctx, userID, false, application.TelegramAuthorizationOptions{ReloadRuntime: false})
	if err != nil {
		return err
	}
	if previous.Telegram.Enabled == next.Telegram.Enabled && slices.Equal(previous.Telegram.AllowedUserIDs, next.Telegram.AllowedUserIDs) {
		return nil
	}
	if _, err := a.Config.Update(func(config.Config) (config.Config, error) { return next, nil }); err != nil {
		return err
	}
	if err := a.Telegram.Reconcile(ctx, next.Telegram); err != nil {
		_, _ = a.Config.Update(func(config.Config) (config.Config, error) { return previous, nil })
		_, _ = application.SetTelegramAuthorizedUser(ctx, userID, true, application.TelegramAuthorizationOptions{ReloadRuntime: false})
		_ = a.Telegram.Reconcile(ctx, previous.Telegram)
		return err
	}
	return nil
}

func (a *App) handleTelegramPairingUpdate(ctx context.Context, update telegram.Update) bool {
	code, ok := telegram.PairingCodeFromUpdate(update)
	if !ok || a == nil || a.TelegramPairing == nil || a.Telegram == nil || a.Config == nil {
		return false
	}
	message := update.Message
	_, err := a.TelegramPairing.Consume(code, message.Chat.ID, message.From.ID, func(userID int64) error {
		_, applyErr := a.persistTelegramPairingUser(userID)
		return applyErr
	})
	if err != nil {
		_ = a.Telegram.SendSetupMessage(ctx, message.Chat.ID, "Pairing request is invalid or expired.")
		return true
	}
	_ = a.Telegram.SendSetupMessage(ctx, message.Chat.ID, "Telegram pairing completed.")
	next := a.Config.Snapshot().Telegram
	if err := a.Telegram.PromoteSetup(next); err != nil {
		return true
	}
	return true
}

func (a *App) persistTelegramPairingUser(userID int64) (config.Config, error) {
	next, err := application.SetTelegramAuthorizedUser(context.Background(), userID, true, application.TelegramAuthorizationOptions{ReloadRuntime: false})
	if err != nil {
		return config.Config{}, err
	}
	if _, err := a.Config.Update(func(config.Config) (config.Config, error) { return next, nil }); err != nil {
		return config.Config{}, err
	}
	return next, nil
}
