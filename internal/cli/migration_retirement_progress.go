package cli

import (
	"strings"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/migration/released024"
)

func migrationRetirementProgressObserver(session *presentation.ProgressSession) func(released024.RetirementEvent) {
	if session != nil {
		session.SetTitle("Retire released ChatGPT-MCP identities")
	}
	return func(event released024.RetirementEvent) {
		if session == nil {
			return
		}
		if event.Child {
			kind := presentation.StatusSuccess
			switch strings.TrimSpace(event.State) {
			case "warning":
				kind = presentation.StatusWarning
			case "failed":
				kind = presentation.StatusError
			}
			session.Append(func(presenter *presentation.Presenter) {
				presenter.ChildStatus(kind, event.Message)
			})
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
		}
		session.Update(presentation.ProgressPhase{
			ID:      "migration.retirement." + event.Stage,
			Label:   migrationRetirementStageLabel(event.Stage),
			State:   state,
			Message: event.Message,
		})
	}
}

func migrationRetirementStageLabel(stage string) string {
	switch strings.TrimSpace(stage) {
	case "canonical":
		return "Verify CodeMCP"
	case "services":
		return "Retire services"
	case "launchers":
		return "Retire commands"
	case "runtime-metadata":
		return "Runtime metadata"
	case "retention":
		return "Rollback retention"
	case "readiness":
		return "Verify readiness"
	case "commit":
		return "Complete retirement"
	default:
		return stage
	}
}
