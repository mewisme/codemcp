package cli

import (
	"errors"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/app"
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/telegram"
)

func telegramSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "telegram", Short: "Manage the Telegram interface"}
	token := &cobra.Command{Use: "token", Short: "Manage the Telegram bot token"}

	set := &cobra.Command{
		Use: "set <bot-token>", Short: "Set the Telegram bot token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := scopedSettingSet(cmd, "telegram.token", args[0]); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Telegram bot token configured", presentation.Field{Label: "setting", Value: "telegram.token"})
			return nil
		},
	}
	remove := &cobra.Command{
		Use: "remove", Aliases: []string{"clear"}, Short: "Remove the Telegram bot token", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := scopedSettingUnset(cmd, "telegram.token"); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Telegram bot token removed", presentation.Field{Label: "setting", Value: "telegram.token"})
			return nil
		},
	}
	status := &cobra.Command{
		Use: "status", Short: "Show whether a Telegram bot token is configured", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := settingService().Present(cmd.Context(), "telegram.token")
			if err != nil {
				return err
			}
			configured := result.Configured != nil && *result.Configured
			presenter := commandPresenter(cmd)
			presenter.Frame("Telegram bot token")
			state := presentation.StatusInfo
			message := "Telegram bot token is not configured"
			if configured {
				state = presentation.StatusSuccess
				message = "Telegram bot token is configured"
			}
			presenter.StateSection(state, message)
			presenter.NestedFields(presentation.Field{Label: "setting", Value: "telegram.token"})
			presenter.Complete("Status complete")
			return nil
		},
	}

	token.AddCommand(
		markScopedSettings(set, "telegram.token"),
		markScopedSettings(remove, "telegram.token"),
		markScopedSettings(status, "telegram.token"),
	)
	cmd.AddCommand(token, telegramSetupCommand(), telegramLogoutCommand())
	return cmd
}

func telegramSetupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Pair an authorized Telegram user",
		Args:  cobra.NoArgs,
		RunE:  runTelegramSetup,
	}
}

func telegramLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout <user-id>",
		Short: "Remove an authorized Telegram user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			userID, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || userID <= 0 {
				return errors.New("telegram user ID must be a positive integer")
			}
			if _, err := application.SetTelegramAuthorizedUser(cmd.Context(), userID, false, application.TelegramAuthorizationOptions{ReloadRuntime: true}); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Telegram user logged out", presentation.Field{Label: "user", Value: strconv.FormatInt(userID, 10)})
			return nil
		},
	}
}

func runTelegramSetup(cmd *cobra.Command, _ []string) error {
	configured, err := settingService().Present(cmd.Context(), "telegram.token")
	if err != nil {
		return err
	}
	if configured.Configured == nil || !*configured.Configured {
		return errors.New("telegram bot token is not configured; run `cm telegram token set <bot-token>` first")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Server.Enabled = false
	cfg.Admin.Enabled = false
	cfg.Tunnel.Enabled = false
	runtime, err := app.NewWithLoggerContext(cmd.Context(), cfg, commandLogger(cmd))
	if err != nil {
		return err
	}
	defer runtime.Telegram.Stop()
	if runtime.Notifications != nil {
		defer runtime.Notifications.Stop()
	}

	challenge, err := runtime.PrepareTelegramPairing(cmd.Context())
	if err != nil {
		return err
	}

	session := commandProgressSession(cmd)
	presenter := session.Presenter()
	presenter.Frame("Telegram setup")
	presenter.Section("Pairing ready")
	fields := []presentation.Field{
		{Label: "code", Value: challenge.Code},
		{Label: "expires", Value: challenge.ExpiresAt.Local().Format(time.RFC3339)},
	}
	if challenge.Username != "" {
		fields = append([]presentation.Field{{Label: "bot", Value: "@" + challenge.Username}}, fields...)
	}
	if challenge.DeepLink != "" {
		fields = append(fields, presentation.Field{Label: "link", Value: challenge.DeepLink})
	}
	presenter.NestedFields(fields...)
	presenter.Spacer()
	const pairingProgressID = "telegram.setup.pairing"
	session.Update(presentation.ProgressPhase{
		ID:    pairingProgressID,
		Label: "Waiting for Telegram pairing",
		State: presentation.ProgressRunning,
	})

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			_ = runtime.TelegramPairingCancel(challenge.Generation)
			session.Fail(pairingProgressID, "Waiting for Telegram pairing", "Telegram pairing cancelled")
			return cmd.Context().Err()
		case <-ticker.C:
			state, err := runtime.TelegramPairingStatus()
			if err != nil {
				session.Fail(pairingProgressID, "Waiting for Telegram pairing", "Telegram pairing failed")
				return err
			}
			switch state.Status {
			case telegram.PairingStatusPaired:
				session.Success(pairingProgressID, "Waiting for Telegram pairing", "Telegram paired")
				if _, running, statusErr := application.RuntimeStatus(cmd.Context()); statusErr != nil {
					presenter.StateSection(presentation.StatusWarning, "Running runtime status could not be checked")
					presenter.NestedFields(presentation.Field{Label: "fix", Value: "cm restart"})
				} else if running {
					if _, reloadErr := requestRuntimeReload(cmd.Context()); reloadErr != nil {
						presenter.StateSection(presentation.StatusWarning, "Running runtime could not reload Telegram configuration")
						presenter.NestedFields(presentation.Field{Label: "fix", Value: "cm restart"})
					} else {
						presenter.StateSection(presentation.StatusSuccess, "Running runtime reloaded")
					}
				}
				presenter.Complete("Setup complete")
				return nil
			case telegram.PairingStatusExpired:
				session.Fail(pairingProgressID, "Waiting for Telegram pairing", "Telegram pairing expired")
				return errors.New("telegram pairing code expired; run `cm telegram setup` again")
			case telegram.PairingStatusCancelled:
				session.Fail(pairingProgressID, "Waiting for Telegram pairing", "Telegram pairing cancelled")
				return errors.New("telegram pairing was cancelled")
			}
		}
	}
}
