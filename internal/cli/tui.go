package cli

import (
	"github.com/spf13/cobra"

	commandtui "go.mewis.me/codemcp/internal/tui"
)

func tuiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui [path...]",
		Short: "Open the full-screen CodeMCP command center",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "TUI", "tui.route.parsing", "Resolving command center route")
			route, err := commandtui.ParseRoute(args)
			if err != nil {
				return err
			}
			logCommandStep(cmd, "TUI", "tui.starting", "Starting command center")
			return commandtui.Run(cmd.Context(), route, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
