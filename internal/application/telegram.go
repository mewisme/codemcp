package application

import (
	"context"
	"errors"
	"slices"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type TelegramAuthorizationOptions struct {
	ReloadRuntime bool
}

type TelegramSetupInput struct {
	Token  string `json:"token"`
	UserID int64  `json:"user_id"`
}

type TelegramSetupResult struct {
	Token           SettingResult `json:"token"`
	Enabled         bool          `json:"enabled"`
	AuthorizedUsers []int64       `json:"authorized_users"`
}

func BindTelegramOperations(dispatcher *Dispatcher) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	return dispatcher.Register(capability.TelegramSetup, typedOperation[TelegramSetupInput](capability.TelegramSetup, func(ctx context.Context, input TelegramSetupInput) (any, error) {
		return SetupTelegram(ctx, input)
	}))
}

func SetupTelegram(ctx context.Context, input TelegramSetupInput) (TelegramSetupResult, error) {
	if input.UserID <= 0 {
		return TelegramSetupResult{}, errors.New("telegram authorized user ID must be positive")
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		return TelegramSetupResult{}, errors.New("telegram bot token is required")
	}
	presented, err := NewSettingService().SetWithOptions(ctx, "telegram.token", token, SettingSetOptions{SecretSource: "browser-protected-input"})
	if err != nil {
		return TelegramSetupResult{}, err
	}
	cfg, err := SetTelegramAuthorizedUser(ctx, input.UserID, true, TelegramAuthorizationOptions{ReloadRuntime: true})
	if err != nil {
		return TelegramSetupResult{}, err
	}
	return TelegramSetupResult{
		Token: presented, Enabled: cfg.Telegram.Enabled,
		AuthorizedUsers: append([]int64(nil), cfg.Telegram.AllowedUserIDs...),
	}, nil
}

func SetTelegramAuthorizedUser(ctx context.Context, userID int64, authorized bool, options TelegramAuthorizationOptions) (result config.Config, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TELEGRAM", "telegram.authorization.set", "Updating Telegram authorization", tracepkg.Int64("user_id", userID), tracepkg.Bool("authorized", authorized), tracepkg.Bool("reload_runtime", options.ReloadRuntime))
	defer func() {
		if resultErr != nil {
			span.FailMessage("Telegram authorization update failed", resultErr)
			return
		}
		span.EndMessage("Telegram authorization updated", tracepkg.Bool("enabled", result.Telegram.Enabled), tracepkg.Int("authorized_users", len(result.Telegram.AllowedUserIDs)))
	}()
	if userID <= 0 {
		return config.Config{}, errors.New("telegram authorized user ID must be positive")
	}
	previous, err := LoadConfig(ctx)
	if err != nil {
		return config.Config{}, err
	}
	next := previous
	ids := append([]int64(nil), previous.Telegram.AllowedUserIDs...)
	if authorized {
		if !slices.Contains(ids, userID) {
			ids = append(ids, userID)
			slices.Sort(ids)
		}
		next.Telegram.Enabled = true
	} else {
		ids = slices.DeleteFunc(ids, func(id int64) bool { return id == userID })
		if len(ids) == 0 {
			next.Telegram.Enabled = false
		}
	}
	next.Telegram.AllowedUserIDs = ids
	if err := config.Validate(next); err != nil {
		return config.Config{}, err
	}
	if options.ReloadRuntime {
		if _, _, err := saveConfigMutation(ctx, previous, next); err != nil {
			return config.Config{}, err
		}
		return next, nil
	}
	if err := config.Save(next); err != nil {
		return config.Config{}, err
	}
	return next, nil
}
