package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
)

func installCutoverObserver(cmd *cobra.Command) func(application.InstallCutoverEvent) {
	session := commandProgressSession(cmd)
	if session != nil {
		session.SetTitle("Install CodeMCP")
	}
	return func(event application.InstallCutoverEvent) {
		if session == nil {
			return
		}
		if event.Child {
			kind := presentation.StatusSuccess
			switch strings.TrimSpace(event.State) {
			case "failed", "warning", "unavailable":
				kind = presentation.StatusWarning
			case "skipped":
				kind = presentation.StatusInfo
			}
			session.Append(func(p *presentation.Presenter) { p.ChildStatus(kind, event.Message) })
			return
		}
		state := presentation.ProgressSuccess
		switch strings.TrimSpace(event.State) {
		case "running":
			state = presentation.ProgressRunning
		case "warning":
			state = presentation.ProgressWarning
		case "failed":
			state = presentation.ProgressFailed
		case "skipped":
			state = presentation.ProgressSkipped
		}
		session.Update(presentation.ProgressPhase{
			ID: "install." + event.Stage, Label: installCutoverStageLabel(event.Stage), State: state, Message: event.Message,
		})
	}
}

func installCutoverStageLabel(stage string) string {
	switch strings.TrimSpace(stage) {
	case "detect":
		return "Detect predecessor state"
	case "stage":
		return "Stage migration"
	case "validate":
		return "Validate staged state"
	case "activate":
		return "Activate CodeMCP"
	case "cleanup":
		return "Finalize installation"
	default:
		return stage
	}
}
