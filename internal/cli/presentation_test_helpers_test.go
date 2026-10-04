package cli

import "go.mewis.me/codemcp/internal/cli/presentation"

func renderStandalonePresentation(presenter *presentation.Presenter, title string, render func()) {
	presenter.Frame(title)
	render()
	presenter.Complete("Done")
}
