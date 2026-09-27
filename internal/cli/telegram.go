package cli

import (
	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
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
	cmd.AddCommand(token)
	return cmd
}
