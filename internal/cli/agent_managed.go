package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/commandalias"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

const managedAgentReadTimeout = 15 * time.Second

func managedAgentCommands() []*cobra.Command {
	return []*cobra.Command{
		managedAgentSpawnCommand(),
		managedAgentListCommand(),
		managedAgentGetCommand(),
		managedAgentSendCommand(),
		managedAgentWaitCommand(),
		managedAgentCancelCommand(),
	}
}

func managedAgentSpawnCommand() *cobra.Command {
	var workspaceID, backend, model, effort string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "spawn <prompt>",
		Short: "Spawn a managed child agent in the running CodeMCP runtime",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(workspaceID) == "" {
				return fmt.Errorf("workspace is required; provide --workspace <id>")
			}
			input := application.ManagedAgentSpawnInput{
				WorkspaceID: workspaceID, Prompt: args[0], Backend: backend,
				Model: model, ReasoningEffort: effort,
			}
			var snapshot managedagent.Snapshot
			if err := managedAgentRuntimeRequest(cmd.Context(), http.MethodPost, "/agents/spawn", input, &snapshot); err != nil {
				return err
			}
			return outputManagedAgentSnapshot(cmd, asJSON, snapshot, "Managed agent spawned")
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID assigned to the child")
	cmd.Flags().StringVar(&backend, "backend", "", "managed-agent backend ID")
	cmd.Flags().StringVar(&model, "model", "", "backend model name")
	cmd.Flags().StringVar(&effort, "effort", "", "backend reasoning effort when supported by the account")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func managedAgentListCommand() *cobra.Command {
	var workspaceID, backend, state string
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: commandalias.Aliases("agent", "list"),
		Short:   "List managed agents in the running CodeMCP runtime",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query := url.Values{}
			if strings.TrimSpace(workspaceID) != "" {
				query.Set("workspace", workspaceID)
			}
			if strings.TrimSpace(backend) != "" {
				query.Set("backend", backend)
			}
			if strings.TrimSpace(state) != "" {
				query.Set("state", state)
			}
			path := "/agents"
			if encoded := query.Encode(); encoded != "" {
				path += "?" + encoded
			}
			var snapshots []managedagent.Snapshot
			if err := managedAgentRuntimeRequest(cmd.Context(), http.MethodGet, path, nil, &snapshots); err != nil {
				return err
			}
			if asJSON {
				return writeResultJSON(cmd, snapshots)
			}
			renderManagedAgentList(commandPresenter(cmd), snapshots)
			return nil
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "filter by workspace ID")
	cmd.Flags().StringVar(&backend, "backend", "", "filter by backend ID")
	cmd.Flags().StringVar(&state, "state", "", "filter by managed-agent state")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func managedAgentGetCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "get <agent_id>",
		Short: "Show one managed agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var snapshot managedagent.Snapshot
			if err := managedAgentRuntimeRequest(cmd.Context(), http.MethodGet, "/agents/get?id="+url.QueryEscape(args[0]), nil, &snapshot); err != nil {
				return err
			}
			return outputManagedAgentSnapshot(cmd, asJSON, snapshot, "Managed agent")
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func managedAgentSendCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "send <agent_id> <message>",
		Short: "Send a follow-up to an idle managed agent",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var snapshot managedagent.Snapshot
			if err := managedAgentRuntimeRequest(cmd.Context(), http.MethodPost, "/agents/send", application.ManagedAgentSendInput{AgentID: args[0], Message: args[1]}, &snapshot); err != nil {
				return err
			}
			return outputManagedAgentSnapshot(cmd, asJSON, snapshot, "Managed agent follow-up sent")
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func managedAgentWaitCommand() *cobra.Command {
	var timeout time.Duration
	var afterRevision uint64
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "wait <agent_id>",
		Short: "Wait up to 10 seconds for a managed agent state change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 || timeout > managedagent.MaxWaitDuration {
				return fmt.Errorf("timeout must be between 0 and %s", managedagent.MaxWaitDuration)
			}
			var snapshot managedagent.Snapshot
			if err := managedAgentRuntimeRequest(cmd.Context(), http.MethodPost, "/agents/wait", application.ManagedAgentWaitInput{
				AgentID: args[0], AfterRevision: afterRevision, TimeoutMS: int(timeout / time.Millisecond),
			}, &snapshot); err != nil {
				return err
			}
			return outputManagedAgentSnapshot(cmd, asJSON, snapshot, "Managed agent")
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", managedagent.MaxWaitDuration, "maximum bounded wait duration")
	cmd.Flags().Uint64Var(&afterRevision, "after-revision", 0, "return when the agent revision advances beyond this value")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func managedAgentCancelCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "cancel <agent_id>",
		Short: "Cancel a managed agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var snapshot managedagent.Snapshot
			if err := managedAgentRuntimeRequest(cmd.Context(), http.MethodPost, "/agents/cancel", application.ManagedAgentIDInput{AgentID: args[0]}, &snapshot); err != nil {
				return err
			}
			return outputManagedAgentSnapshot(cmd, asJSON, snapshot, "Managed agent cancelled")
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func managedAgentRuntimeRequest(ctx context.Context, method, path string, input, output any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, managedAgentReadTimeout)
	defer cancel()
	_, err := runtimecontrol.Request(callCtx, method, path, input, output)
	if err != nil && runtimecontrol.IsUnavailable(err) {
		return fmt.Errorf("managed-agent runtime is unavailable; start CodeMCP with 'cm up' or 'cm serve' first: %w", err)
	}
	return err
}

func outputManagedAgentSnapshot(cmd *cobra.Command, asJSON bool, snapshot managedagent.Snapshot, title string) error {
	if asJSON {
		return writeResultJSON(cmd, snapshot)
	}
	renderManagedAgentSnapshot(commandPresenter(cmd), snapshot, title)
	return nil
}

func renderManagedAgentList(presenter *presentation.Presenter, snapshots []managedagent.Snapshot) {
	if len(snapshots) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No managed agents")
		return
	}
	rows := make([]presentation.Row, 0, len(snapshots))
	for _, snapshot := range snapshots {
		rows = append(rows, presentation.Row{
			string(snapshot.ID), string(snapshot.State), snapshot.WorkspaceID,
			string(snapshot.Backend), fmt.Sprint(snapshot.Turn), fmt.Sprint(snapshot.Revision),
		})
	}
	presenter.Table([]string{"ID", "State", "Workspace", "Backend", "Turn", "Revision"}, rows, presentation.TableOptions{
		Border: presentation.TableBare,
		Layout: presentation.TableAdaptive,
		Depth:  1,
	})
}

func renderManagedAgentSnapshot(presenter *presentation.Presenter, snapshot managedagent.Snapshot, title string) {
	presenter.StateSection(managedAgentPresentationKind(snapshot.State), strings.ToUpper(string(snapshot.State)))
	presenter.Subsection(string(snapshot.ID))
	fields := []presentation.Field{
		{Label: "workspace", Value: snapshot.WorkspaceID},
		{Label: "backend", Value: snapshot.Backend},
		{Label: "depth", Value: snapshot.Depth},
		{Label: "turn", Value: snapshot.Turn},
		{Label: "revision", Value: snapshot.Revision},
		{Label: "updated", Value: snapshot.UpdatedAt.UTC().Format(time.RFC3339)},
	}
	if snapshot.Model != "" {
		fields = append(fields, presentation.Field{Label: "model", Value: snapshot.Model})
	}
	if snapshot.ReasoningEffort != "" {
		fields = append(fields, presentation.Field{Label: "effort", Value: snapshot.ReasoningEffort})
	}
	presenter.NestedFields(fields...)
	if strings.TrimSpace(snapshot.Result) != "" {
		presenter.Section("Result")
		presenter.List(snapshot.Result)
	}
	if strings.TrimSpace(snapshot.Error) != "" {
		presenter.Section("Error")
		presenter.List(snapshot.Error)
	}
}

func managedAgentPresentationKind(state managedagent.State) presentation.StatusKind {
	switch state {
	case managedagent.StateCompleted:
		return presentation.StatusSuccess
	case managedagent.StateFailed, managedagent.StateExpired:
		return presentation.StatusError
	case managedagent.StateCancelled:
		return presentation.StatusWarning
	case managedagent.StateIdle:
		return presentation.StatusInactive
	default:
		return presentation.StatusInfo
	}
}
