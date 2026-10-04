package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	mcpnetwork "go.mewis.me/codemcp/internal/network"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

func healthCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "health", Short: "Show lightweight CodeMCP health", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		value := application.HealthStatus{OK: true, AdminAuthEnabled: cfg.HTTP.Admin.Auth.Enabled}
		if asJSON {
			return writeResultJSON(cmd, value)
		}
		p := commandPresenter(cmd)
		p.StateSection(presentation.StatusSuccess, "CodeMCP configuration is healthy")
		p.Fields(presentation.Field{Label: "ok", Value: value.OK}, presentation.Field{Label: "admin auth", Value: value.AdminAuthEnabled})
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func networkCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "network", Short: "Inspect host network state"}
	var asJSON bool
	interfaces := &cobra.Command{Use: "interfaces", Short: "List network interfaces available to CodeMCP", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		values, err := mcpnetwork.Discover()
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, values)
		}
		p := commandPresenter(cmd)
		if len(values) == 0 {
			p.StateSection(presentation.StatusInactive, "No eligible interfaces")
		} else {
			p.Section(fmt.Sprintf("Interfaces · %d", len(values)))
			for index, value := range values {
				p.SubsectionItem(value.Name, index == len(values)-1)
				for _, address := range value.Addresses {
					p.NestedFields(presentation.Field{Label: address.Scope, Value: address.Host})
				}
			}
		}
		return nil
	}}
	addJSONResultFlag(interfaces, &asJSON)
	cmd.AddCommand(interfaces)
	return cmd
}

func configSnapshotCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "snapshot", Short: "Read the canonical configuration snapshot", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		values, err := settingService().List(cmd.Context(), "")
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, values)
		}
		return printSettingSelection(cmd, settingService(), "", true, configOutputOptions{noAccepts: true})
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func configPatchCommand() *cobra.Command {
	var sets, unsets []string
	var asJSON bool
	cmd := &cobra.Command{Use: "patch", Short: "Apply multiple canonical setting changes atomically", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		changes := make([]application.SettingChange, 0, len(sets)+len(unsets))
		for _, raw := range sets {
			key, value, ok := strings.Cut(raw, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return fmt.Errorf("--set requires key=value, got %q", raw)
			}
			changes = append(changes, application.SettingChange{Key: strings.TrimSpace(key), Value: value})
		}
		for _, key := range unsets {
			if strings.TrimSpace(key) == "" {
				return errors.New("--unset requires a setting key")
			}
			changes = append(changes, application.SettingChange{Key: strings.TrimSpace(key), Unset: true})
		}
		if len(changes) == 0 {
			return errors.New("config patch requires at least one --set key=value or --unset key")
		}
		result, err := settingService().Apply(cmd.Context(), changes)
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result)
		}
		renderMutationSuccess(cmd, "Configuration patch applied", presentation.Field{Label: "changes", Value: len(result.Results)})
		return nil
	}}
	cmd.Flags().StringSliceVar(&sets, "set", nil, "setting assignment in key=value form; repeatable")
	cmd.Flags().StringSliceVar(&unsets, "unset", nil, "setting key to reset/clear; repeatable")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func workspaceContainerMembershipListCommand() *cobra.Command {
	var workspaceID, containerID string
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Short: "List workspace-container membership from either side", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if (strings.TrimSpace(workspaceID) == "") == (strings.TrimSpace(containerID) == "") {
			return errors.New("provide exactly one of --workspace or --container")
		}
		service := workspaceServiceForCommand(cmd)
		if workspaceID != "" {
			result, err := service.ContainersForWorkspace(cmd.Context(), workspaceID)
			if err != nil {
				return err
			}
			if asJSON {
				return writeResultJSON(cmd, result.Value)
			}
			renderWorkspaceContainers(commandPresenter(cmd), result.Value)
			return nil
		}
		result, err := service.WorkspacesForContainer(cmd.Context(), containerID)
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result.Value)
		}
		p := commandPresenter(cmd)
		p.Section(fmt.Sprintf("Workspaces · %d", len(result.Value)))
		for index, value := range result.Value {
			p.SubsectionItem(value.ID, index == len(result.Value)-1)
			p.NestedFields(presentation.Field{Label: "path", Value: value.Path})
		}
		return nil
	}}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID whose container membership should be listed")
	cmd.Flags().StringVar(&containerID, "container", "", "container ID whose workspaces should be listed")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func executionFeedCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "feed", Short: "Stream the canonical workspace execution feed", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		stream, _, err := runtimecontrol.OpenExecutionFeed(cmd.Context())
		if err != nil {
			return err
		}
		defer stream.Close()
		if err := writeStreamValue(cmd, asJSON, "execution snapshot", stream.Snapshot()); err != nil {
			return err
		}
		for {
			event, err := stream.Next()
			if err != nil {
				return streamEnd(cmd.Context(), err)
			}
			if err := writeStreamValue(cmd, asJSON, "execution", event); err != nil {
				return err
			}
		}
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func executionStreamCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "stream <workspace_id> <execution_id>", Short: "Stream events for one workspace execution", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		workspaceID, executionID := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
		if _, err := application.NewRuntimeInspectionService().ViewExecution(cmd.Context(), workspaceID, executionID); err != nil {
			return err
		}
		stream, _, err := runtimecontrol.OpenExecutionFeed(cmd.Context())
		if err != nil {
			return err
		}
		defer stream.Close()
		for {
			event, err := stream.Next()
			if err != nil {
				return streamEnd(cmd.Context(), err)
			}
			if event.ExecutionID != executionID {
				continue
			}
			if err := writeStreamValue(cmd, asJSON, "execution", event); err != nil {
				return err
			}
		}
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func processClearCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "clear <workspace_id> <process_id>", Short: "Clear one finished managed process", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := application.NewRuntimeInspectionService().ClearProcess(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result.Value)
		}
		renderMutationSuccess(cmd, "Finished process cleared", presentation.Field{Label: "process", Value: args[1]})
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestStreamCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "stream", Short: "Stream approval-request lifecycle events", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		stream, _, err := runtimecontrol.OpenApprovalFeed(cmd.Context())
		if err != nil {
			return err
		}
		defer stream.Close()
		if err := writeStreamValue(cmd, asJSON, "approval snapshot", stream.Snapshot()); err != nil {
			return err
		}
		for {
			event, err := stream.Next()
			if err != nil {
				return streamEnd(cmd.Context(), err)
			}
			if err := writeStreamValue(cmd, asJSON, "approval", event); err != nil {
				return err
			}
		}
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestExplainViewCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "view <request_id>", Short: "Show the current generated explanation for one approval request", Args: cobra.ExactArgs(1), ValidArgsFunction: completeApprovalRequestIDs, RunE: func(cmd *cobra.Command, args []string) error {
		status, err := application.GetApprovalExplainStatus(cmd.Context())
		if err != nil {
			return err
		}
		result, err := application.GetApprovalExplanation(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result)
		}
		p := commandPresenter(cmd)
		renderApprovalExplanation(p, status, result)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func agentCompletionFeedCommand() *cobra.Command {
	var workspaceID string
	var limit int
	var asJSON bool
	cmd := &cobra.Command{Use: "feed", Short: "Stream accepted agent completions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		stream, _, err := runtimecontrol.OpenCompletionFeed(cmd.Context(), workspaceID, limit)
		if err != nil {
			return err
		}
		defer stream.Close()
		if err := writeStreamValue(cmd, asJSON, "completion snapshot", stream.Snapshot()); err != nil {
			return err
		}
		for {
			event, err := stream.Next()
			if err != nil {
				return streamEnd(cmd.Context(), err)
			}
			if err := writeStreamValue(cmd, asJSON, "completion", event); err != nil {
				return err
			}
		}
	}}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "limit events to one workspace ID")
	cmd.Flags().IntVar(&limit, "limit", 50, "snapshot history limit")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func notificationStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "status", Short: "Show canonical notification delivery policy", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		value := map[string]bool{
			"approval_enabled":            cfg.Notifications.Approval.Enabled,
			"approval_pending":            cfg.Notifications.Approval.Pending,
			"approval_resolved":           cfg.Notifications.Approval.Resolved,
			"desktop_enabled":             cfg.Notifications.Approval.DesktopEnabled,
			"telegram_enabled":            cfg.Notifications.Approval.TelegramEnabled,
			"completion_enabled":          cfg.Notifications.Completion.Enabled,
			"completion_desktop_enabled":  cfg.Notifications.Completion.DesktopEnabled,
			"completion_telegram_enabled": cfg.Notifications.Completion.TelegramEnabled,
		}
		if asJSON {
			return writeResultJSON(cmd, value)
		}
		p := commandPresenter(cmd)
		p.Fields(
			presentation.Field{Label: "approval", Value: value["approval_enabled"]},
			presentation.Field{Label: "desktop", Value: value["desktop_enabled"]},
			presentation.Field{Label: "telegram", Value: value["telegram_enabled"]},
			presentation.Field{Label: "completion", Value: value["completion_enabled"]},
		)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func tunnelConfigReadCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "config", Short: "Show canonical OpenAI Secure MCP Tunnel configuration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		status, err := application.TunnelStatus()
		if err != nil {
			return err
		}
		view := struct {
			Enabled              bool   `json:"enabled"`
			ID                   string `json:"id,omitempty"`
			ControlPlaneBaseURL  string `json:"control_plane_base_url,omitempty"`
			OrganizationID       string `json:"organization_id,omitempty"`
			RuntimeKeyConfigured bool   `json:"runtime_key_configured"`
			AdminEnabled         bool   `json:"admin_enabled"`
			AdminKeyConfigured   bool   `json:"admin_key_configured"`
		}{
			Enabled: status.Config.Enabled, ID: status.Config.ID, ControlPlaneBaseURL: status.Config.ControlPlaneBaseURL,
			OrganizationID: status.Config.OrganizationID, RuntimeKeyConfigured: strings.TrimSpace(status.Config.APIKey) != "",
			AdminEnabled: status.Config.Admin.Enabled, AdminKeyConfigured: strings.TrimSpace(status.Config.Admin.Key) != "",
		}
		if asJSON {
			return writeResultJSON(cmd, view)
		}
		p := commandPresenter(cmd)
		p.Fields(
			presentation.Field{Label: "enabled", Value: view.Enabled},
			presentation.Field{Label: "id", Value: view.ID},
			presentation.Field{Label: "control plane", Value: view.ControlPlaneBaseURL},
			presentation.Field{Label: "runtime key configured", Value: view.RuntimeKeyConfigured},
			presentation.Field{Label: "admin key configured", Value: view.AdminKeyConfigured},
		)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func activityCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "activity", Short: "Inspect canonical runtime tool-call activity"}
	cmd.AddCommand(activityStreamCommand(), activityViewCommand())
	return cmd
}

func activityStreamCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "stream", Short: "Stream tool-call activity", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		stream, _, err := runtimecontrol.OpenToolCallFeed(cmd.Context())
		if err != nil {
			return err
		}
		defer stream.Close()
		if err := writeStreamValue(cmd, asJSON, "activity snapshot", stream.Snapshot()); err != nil {
			return err
		}
		for {
			event, err := stream.Next()
			if err != nil {
				return streamEnd(cmd.Context(), err)
			}
			if err := writeStreamValue(cmd, asJSON, "activity", event); err != nil {
				return err
			}
		}
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func activityViewCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "view <call_id>", Short: "Show one retained tool-call activity record", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		value, err := runtimecontrol.GetToolCallDetail(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, value)
		}
		p := commandPresenter(cmd)
		p.Fields(
			presentation.Field{Label: "call", Value: value.CallID},
			presentation.Field{Label: "tool", Value: value.Tool},
			presentation.Field{Label: "status", Value: value.Status},
			presentation.Field{Label: "workspace", Value: value.WorkspaceID},
			presentation.Field{Label: "source", Value: value.Source},
			presentation.Field{Label: "duration", Value: value.DurationMS},
		)
		renderCLIObservabilitySection(p, "Request", value.Request)
		if value.Error != nil {
			renderCLIObservabilitySection(p, "Error", value.Error)
		} else {
			renderCLIObservabilitySection(p, "Response", value.Response)
		}
		if value.Diagnostic.Redacted || value.Diagnostic.Truncated {
			renderCLIObservabilitySection(p, "Diagnostic", value.Diagnostic)
		}
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderCLIObservabilitySection(p *presentation.Presenter, title string, value any) {
	if p == nil || value == nil {
		return
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(value))
	}
	p.Section(title)
	p.Note("", string(data))
}

func writeStreamValue(cmd *cobra.Command, asJSON bool, label string, value any) error {
	if asJSON || commandResultModeFor(cmd) == resultModeJSON {
		return json.NewEncoder(commandResultWriter(cmd)).Encode(value)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Fprintf(commandResultWriter(cmd), "%s %s\n", label, data)
	return nil
}

func streamEnd(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return nil
	}
	return err
}

func requireDestructiveConfirmation(value bool, noun string) error {
	if value {
		return nil
	}
	return fmt.Errorf("%s requires --yes", noun)
}
