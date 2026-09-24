package cli

import (
	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func beginMutationProgress(cmd *cobra.Command, title string) {
	commandProgressSession(cmd).Begin(title)
}

func renderMutationResult(cmd *cobra.Command, title string, kind presentation.StatusKind, message string, fields ...presentation.Field) {
	closeCommandProgress(cmd, nil)
	presenter := commandPresenter(cmd)
	presenter.Frame(title)
	presenter.Status(kind, message)
	if len(fields) > 0 {
		presenter.Fields(fields...)
	}
	presenter.FrameEnd("Done")
}

func renderMutationSuccess(cmd *cobra.Command, title, message string, fields ...presentation.Field) {
	renderMutationResult(cmd, title, presentation.StatusSuccess, message, fields...)
}
