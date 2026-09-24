package cli

import (
	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func beginMutationProgress(cmd *cobra.Command, title string) {
	commandProgressSession(cmd).Begin(title)
}

func renderMutationBlock(cmd *cobra.Command, title string, render func(*presentation.Presenter)) {
	presenter := commandPresenter(cmd)
	if session := takeCommandProgress(cmd); session != nil {
		if session.Begun() {
			session.Suspend()
			render(presenter)
			session.CloseWith("Done")
			return
		}
		session.Close()
	}
	presenter.Frame(title)
	render(presenter)
	presenter.FrameEnd("Done")
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
