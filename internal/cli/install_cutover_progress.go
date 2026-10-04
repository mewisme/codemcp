package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
)

func installCutoverObserver(cmd *cobra.Command) func(application.InstallCutoverEvent) {
	return installCutoverProgressObserver(commandProgressSession(cmd))
}

func installCutoverProgressObserver(session *presentation.ProgressSession) func(application.InstallCutoverEvent) {
	return func(event application.InstallCutoverEvent) {
		if session == nil {
			return
		}
		if !event.Child {
			state := strings.TrimSpace(event.State)
			if state != "warning" && state != "skipped" && state != "unavailable" {
				return
			}
			name := "install.cutover." + strings.TrimSpace(event.Stage)
			spec, ok := traceProgress[name]
			if !ok {
				return
			}
			progressState := presentation.ProgressWarning
			if state == "skipped" || state == "unavailable" {
				progressState = presentation.ProgressSkipped
			}
			session.Update(presentation.ProgressPhase{ID: name, Label: spec.start, State: progressState, Message: event.Message})
			return
		}
		kind := presentation.StatusSuccess
		switch strings.TrimSpace(event.State) {
		case "failed", "warning", "unavailable":
			kind = presentation.StatusWarning
		case "skipped":
			kind = presentation.StatusInfo
		}
		session.Append(func(p *presentation.Presenter) { p.ChildStatus(kind, event.Message) })
	}
}
