package cli

import (
	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func renderMutationBlock(cmd *cobra.Command, render func(*presentation.Presenter)) {
	session := commandProgressSession(cmd)
	session.Append(render)
	session.SetCompletion("Done")
}

func renderMutationResult(cmd *cobra.Command, kind presentation.StatusKind, message string, fields ...presentation.Field) {
	renderMutationBlock(cmd, func(presenter *presentation.Presenter) {
		presenter.Status(kind, message)
		if len(fields) > 0 {
			presenter.Fields(fields...)
		}
	})
}

func renderMutationSuccess(cmd *cobra.Command, message string, fields ...presentation.Field) {
	renderMutationResult(cmd, presentation.StatusSuccess, message, fields...)
}

func renderEntityMutationResult(cmd *cobra.Command, kind presentation.StatusKind, message, id string, fields ...presentation.Field) {
	renderMutationBlock(cmd, func(presenter *presentation.Presenter) {
		presenter.Status(kind, message)
		if id != "" {
			presenter.Subsection(id)
			presenter.NestedFields(fields...)
			return
		}
		if len(fields) > 0 {
			presenter.Fields(fields...)
		}
	})
}

func renderEntityMutationSuccess(cmd *cobra.Command, message, id string, fields ...presentation.Field) {
	renderEntityMutationResult(cmd, presentation.StatusSuccess, message, id, fields...)
}
