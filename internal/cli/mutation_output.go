package cli

import (
	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func beginMutationProgress(cmd *cobra.Command, title string) {
	commandProgressSession(cmd).Begin(title)
}

func renderMutationBlock(cmd *cobra.Command, title string, render func(*presentation.Presenter)) {
	session := commandProgressSession(cmd)
	session.Begin(title)
	session.Append(render)
	session.CloseWith("Done")
}

func renderMutationResult(cmd *cobra.Command, title string, kind presentation.StatusKind, message string, fields ...presentation.Field) {
	renderMutationBlock(cmd, title, func(presenter *presentation.Presenter) {
		presenter.Status(kind, message)
		if len(fields) > 0 {
			presenter.Fields(fields...)
		}
	})
}

func renderMutationSuccess(cmd *cobra.Command, title, message string, fields ...presentation.Field) {
	renderMutationResult(cmd, title, presentation.StatusSuccess, message, fields...)
}
