package application

import (
	"context"
	"errors"
	"slices"

	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type TelegramAuthorizationOptions struct {
	ReloadRuntime bool
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
