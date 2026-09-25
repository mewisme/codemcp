package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

const completionReadTimeout = 5 * time.Second

func agentCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Inspect agent lifecycle state"}
	completion := &cobra.Command{Use: "completion", Short: "Inspect durable agent completion history"}
	completion.AddCommand(agentCompletionCurrentCommand(), agentCompletionListCommand(), agentCompletionViewCommand(), agentCompletionDoctorCommand())
	cmd.AddCommand(completion)
	return cmd
}

func agentCompletionCurrentCommand() *cobra.Command {
	var workspaceID string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show the latest accepted agent completion for one workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(workspaceID) == "" {
				return fmt.Errorf("workspace is required; provide --workspace <id>")
			}
			return runCompletionRead(cmd, asJSON, "completion.current.loading", "Loading current agent completion", "Loaded current agent completion", func(ctx context.Context) (any, error) {
				return application.CurrentCompletion(ctx, workspaceID)
			}, func(value any) {
				renderCompletion(commandPresenter(cmd), value.(agentcompletion.Record), "Current agent completion")
			})
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID whose current completion should be shown")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func agentCompletionListCommand() *cobra.Command {
	var workspaceID string
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List recent accepted agent completions",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCompletionRead(cmd, asJSON, "completion.list.loading", "Loading agent completion history", "Loaded agent completion history", func(ctx context.Context) (any, error) {
				return application.ListCompletions(ctx, workspaceID, limit)
			}, func(value any) {
				renderCompletionList(commandPresenter(cmd), value.([]agentcompletion.Record))
			})
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "limit history to one workspace ID")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of completion records to return")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func agentCompletionViewCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "view <completion_id>",
		Aliases: []string{"show", "info"},
		Short:   "Show one accepted agent completion",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCompletionRead(cmd, asJSON, "completion.view.loading", "Loading agent completion", "Loaded agent completion", func(ctx context.Context) (any, error) {
				return application.ViewCompletion(ctx, args[0])
			}, func(value any) {
				renderCompletion(commandPresenter(cmd), value.(agentcompletion.Record), "Agent completion")
			})
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func agentCompletionDoctorCommand() *cobra.Command {
	var workspaceID string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose completion store and hook health for one workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(workspaceID) == "" {
				return fmt.Errorf("workspace is required; provide --workspace <id>")
			}
			return runCompletionRead(cmd, asJSON, "completion.doctor.loading", "Diagnosing agent completion lifecycle", "Diagnosed agent completion lifecycle", func(ctx context.Context) (any, error) {
				return application.CompletionHealth(ctx, workspaceID)
			}, func(value any) {
				renderCompletionHealth(commandPresenter(cmd), value.(agentcompletion.Health))
			})
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID whose completion lifecycle should be diagnosed")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func runCompletionRead(cmd *cobra.Command, asJSON bool, phaseID, running, done string, load func(context.Context) (any, error), render func(any)) error {
	var progress *commandProgress
	if !asJSON {
		progress = newCommandProgress(cmd, "COMPLETION")
		progress.Start(phaseID, running, done)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), completionReadTimeout)
	defer cancel()
	value, err := load(ctx)
	if err != nil {
		if progress != nil {
			progress.Stop()
		}
		return err
	}
	if asJSON {
		return writeResultJSON(cmd, value)
	}
	if progress != nil {
		progress.Complete()
	}
	render(value)
	return nil
}

func renderCompletionList(presenter *presentation.Presenter, records []agentcompletion.Record) {
	presenter.Frame("Agent completion history")
	if len(records) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No accepted agent completions")
		presenter.Complete("Done")
		return
	}
	rows := make([]presentation.Row, 0, len(records))
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		rows = append(rows, presentation.Row{
			record.ID,
			string(record.Status),
			record.WorkspaceID,
			record.Title,
			formatCompletionTime(record.CreatedAt),
		})
	}
	presenter.Section(fmt.Sprintf("Completions · %d", len(records)))
	presenter.Rows([]string{"ID", "Status", "Workspace", "Title", "Created"}, rows...)
	presenter.Complete("Done")
}

func renderCompletion(presenter *presentation.Presenter, record agentcompletion.Record, title string) {
	presenter.Frame(title)
	presenter.StateSection(completionPresentationKind(record.Status), completionStatusLabel(record.Status))
	presenter.Subsection(record.ID)
	fields := []presentation.Field{
		{Label: "workspace", Value: record.WorkspaceID},
		{Label: "agent", Value: record.AgentID},
		{Label: "sequence", Value: record.Sequence},
	}
	if record.Source != "" {
		fields = append(fields, presentation.Field{Label: "source", Value: record.Source})
	}
	if record.SupersedesID != "" {
		fields = append(fields, presentation.Field{Label: "supersedes", Value: record.SupersedesID})
	}
	fields = append(fields, presentation.Field{Label: "created", Value: formatCompletionTime(record.CreatedAt)})
	presenter.NestedFields(fields...)
	presenter.Spacer()
	presenter.Section(record.Title)
	if strings.TrimSpace(record.Summary) != "" {
		presenter.List(record.Summary)
	}
	presenter.Complete("Done")
}

func renderCompletionHealth(presenter *presentation.Presenter, health agentcompletion.Health) {
	presenter.Frame("Agent completion health")
	presenter.StateSection(completionHealthPresentationKind(health.Status), completionHealthStatusLabel(health.Status))
	presenter.Subsection(health.WorkspaceID)
	presenter.NestedFields(
		presentation.Field{Label: "hot records", Value: health.HotRecords},
		presentation.Field{Label: "archived records", Value: health.ArchivedRecords},
		presentation.Field{Label: "latest sequence", Value: health.LatestSequence},
		presentation.Field{Label: "archive tail issue", Value: health.ArchiveTailIssue},
	)
	presenter.Spacer()
	presenter.Section("Completion hooks")
	presenter.Fields(
		presentation.Field{Label: "registered", Value: health.Hooks.Registered},
		presentation.Field{Label: "stopped", Value: health.Hooks.Stopped},
		presentation.Field{Label: "recent diagnostics", Value: health.Hooks.RecentDiagnostics},
		presentation.Field{Label: "failures", Value: health.Hooks.Failures},
		presentation.Field{Label: "timeouts", Value: health.Hooks.Timeouts},
		presentation.Field{Label: "cancelled", Value: health.Hooks.Cancelled},
		presentation.Field{Label: "duplicates", Value: health.Hooks.Duplicates},
	)
	if strings.TrimSpace(health.Error) != "" {
		presenter.Spacer()
		presenter.Section("Diagnostic error")
		presenter.List(health.Error)
	}
	presenter.Complete("Done")
}

func completionHealthPresentationKind(status agentcompletion.HealthStatus) presentation.StatusKind {
	switch status {
	case agentcompletion.HealthHealthy:
		return presentation.StatusSuccess
	case agentcompletion.HealthDegraded:
		return presentation.StatusWarning
	case agentcompletion.HealthCorrupt, agentcompletion.HealthClosed:
		return presentation.StatusError
	default:
		return presentation.StatusInfo
	}
}

func completionHealthStatusLabel(status agentcompletion.HealthStatus) string {
	switch status {
	case agentcompletion.HealthHealthy:
		return "Healthy"
	case agentcompletion.HealthDegraded:
		return "Degraded"
	case agentcompletion.HealthCorrupt:
		return "Corrupt"
	case agentcompletion.HealthClosed:
		return "Closed"
	default:
		return string(status)
	}
}

func completionPresentationKind(status agentcompletion.Status) presentation.StatusKind {
	switch status {
	case agentcompletion.StatusCompleted:
		return presentation.StatusSuccess
	case agentcompletion.StatusBlocked, agentcompletion.StatusCancelled:
		return presentation.StatusError
	case agentcompletion.StatusPartial:
		return presentation.StatusWarning
	default:
		return presentation.StatusInfo
	}
}

func completionStatusLabel(status agentcompletion.Status) string {
	switch status {
	case agentcompletion.StatusCompleted:
		return "Completed"
	case agentcompletion.StatusPartial:
		return "Partially completed"
	case agentcompletion.StatusBlocked:
		return "Blocked"
	case agentcompletion.StatusCancelled:
		return "Cancelled"
	default:
		return string(status)
	}
}

func formatCompletionTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format(time.RFC3339Nano)
}
