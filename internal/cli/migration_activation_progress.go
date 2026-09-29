package cli

import (
	"strings"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/migration/released024"
)

func migrationActivationProgressObserver(session *presentation.ProgressSession) func(released024.ActivationEvent) {
	if session != nil {
		session.SetTitle("Activate migrated CodeMCP state")
	}
	return func(event released024.ActivationEvent) {
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
			ID: "migration.activation." + event.Stage, Label: migrationActivationStageLabel(event.Stage),
			State: state, Message: event.Message,
		})
	}
}

func migrationActivationStageLabel(stage string) string {
	switch strings.TrimSpace(stage) {
	case "workspaces":
		return "Prepare workspaces"
	case "secrets-bind":
		return "Bind secrets"
	case "publish":
		return "Publish state"
	case "workspaces-activate":
		return "Activate workspaces"
	case "install":
		return "Install cm"
	case "health":
		return "Health checks"
	case "service-environment":
		return "Service environment"
	case "service":
		return "Managed service"
	case "readiness":
		return "Runtime readiness"
	case "commit":
		return "Commit migration"
	case "rollback":
		return "Rollback migration"
	default:
		return stage
	}
}
