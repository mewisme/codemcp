package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/logger"
)

const requestControlTimeout = 5 * time.Second

func requestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "request", Aliases: []string{"req"}, Short: "Review and resolve control approval requests"}
	cmd.AddCommand(requestListCommand(), requestViewCommand(), requestResolveCommand(true), requestResolveCommand(false), requestGrantCommand(), requestCreateCommand())
	return cmd
}

func requestGrantCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "grant", Short: "Inspect and revoke similar-command runtime session grants"}
	cmd.AddCommand(requestGrantListCommand(), requestGrantRevokeCommand())
	return cmd
}

func requestGrantListCommand() *cobra.Command {
	var asJSON bool
	var workspaceID string
	cmd := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List active similar-command runtime grants", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint")
		log := commandLogger(cmd)
		if !asJSON {
			startCommandSpinner(cmd, log, "REQUEST", "request.loading", "Loading runtime session grants")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		grants, err := application.ListRuntimeGrants(ctx, workspaceID)
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, grants)
		}
		log.StopAnimation()
		renderRuntimeGrants(commandPresenter(cmd), grants)
		return nil
	}}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "filter grants by workspace ID")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestGrantRevokeCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "revoke <request_id>", Short: "Revoke one similar-command runtime grant by request ID or unique prefix", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint", logger.WithVerbose("request", args[0]))
		log := commandLogger(cmd)
		if !asJSON {
			startCommandSpinner(cmd, log, "REQUEST", "request.resolving", "Revoking runtime session grant")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		request, err := application.RevokeRuntimeGrant(ctx, args[0])
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, request)
		}
		log.Success("REQUEST", "runtime session grant revoked", "id", request.ID)
		log.Detail("status", request.Status)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestCreateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "create", Short: "Create control approval requests for testing"}
	cmd.AddCommand(requestCreateDummyCommand())
	return cmd
}

func requestCreateDummyCommand() *cobra.Command {
	var workspaceID, title, command string
	var asJSON bool
	cmd := &cobra.Command{Use: "dummy", Short: "Create a dummy pending approval request for UI testing", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint")
		log := commandLogger(cmd)
		if !asJSON {
			startCommandSpinner(cmd, log, "REQUEST", "request.creating", "Creating dummy approval request")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		request, err := requestRuntimeApprovalCreateDummy(ctx, workspaceID, title, command)
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, request)
		}
		log.Success("REQUEST", "dummy control approval request created", "id", request.ID)
		log.Detail("workspace", request.WorkspaceID)
		log.Detail("expires", request.ExpiresAt.Format(time.RFC3339Nano))
		return nil
	}}
	cmd.Flags().StringVar(&workspaceID, "workspace", "ws_dummy", "workspace ID shown on the dummy request")
	cmd.Flags().StringVar(&title, "title", "Allow dummy command", "request title shown in approval UIs")
	cmd.Flags().StringVar(&command, "command", "echo dummy approval", "dummy run_command value shown in exact arguments")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List control approval requests from the running runtime", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint")
		log := commandLogger(cmd)
		if !asJSON {
			startCommandSpinner(cmd, log, "REQUEST", "request.loading", "Loading approval requests")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		requests, err := requestRuntimeApprovalList(ctx)
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, requests)
		}
		log.StopAnimation()
		renderApprovalRequests(commandPresenter(cmd), requests)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestViewCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "view <request_id>", Aliases: []string{"show", "info"}, Short: "Show one control approval request by ID or unique prefix", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint", logger.WithVerbose("request", args[0]))
		log := commandLogger(cmd)
		if !asJSON {
			startCommandSpinner(cmd, log, "REQUEST", "request.loading", "Loading approval request")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		request, err := requestRuntimeApprovalView(ctx, args[0])
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, request)
		}
		log.StopAnimation()
		renderApprovalRequest(commandPresenter(cmd), request)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestResolveCommand(approve bool) *cobra.Command {
	action, past := "deny", "denied"
	label := "Deny"
	aliases := []string{"reject"}
	if approve {
		action, past = "approve", "approved"
		label = "Approve"
		aliases = []string{"accept", "allow"}
	}
	progress := "Denying approval request"
	if approve {
		progress = "Approving approval request"
	}
	var asJSON bool
	var reason string
	cmd := &cobra.Command{Use: action + " <request_id>", Aliases: aliases, Short: label + " one pending control approval request by ID or unique prefix", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint", logger.WithVerbose("action", action), logger.WithVerbose("request", args[0]))
		log := commandLogger(cmd)
		if !asJSON {
			startCommandSpinner(cmd, log, "REQUEST", "request.resolving", progress)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		var request approval.Request
		var err error
		if approve {
			request, err = requestRuntimeApprovalApprove(ctx, args[0], reason)
		} else {
			request, err = requestRuntimeApprovalDeny(ctx, args[0], reason)
		}
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, request)
		}
		log.Success("REQUEST", "control approval request "+past, "id", request.ID)
		log.Detail("status", request.Status)
		if !request.RetryUntil.IsZero() {
			log.Detail("retry_until", request.RetryUntil.Format(time.RFC3339Nano))
		}
		if request.Reason != "" {
			log.Detail("reason", request.Reason)
		}
		return nil
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "record an optional approval resolution reason")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderApprovalRequests(presenter *presentation.Presenter, requests []approval.Request) {
	if len(requests) == 0 {
		presenter.Status(presentation.StatusInfo, "No control approval requests")
		return
	}
	rows := make([]presentation.Row, 0, len(requests))
	for _, request := range requests {
		rows = append(rows, presentation.Row{request.ID, string(request.Status), request.WorkspaceID, request.TargetTool, request.Title})
	}
	presenter.Section("Control approval requests")
	presenter.Rows([]string{"ID", "Status", "Workspace", "Tool", "Title"}, rows...)
}

func renderRuntimeGrants(presenter *presentation.Presenter, grants []approval.Request) {
	if len(grants) == 0 {
		presenter.Status(presentation.StatusInfo, "No active runtime session grants")
		return
	}
	rows := make([]presentation.Row, 0, len(grants))
	for _, grant := range grants {
		rows = append(rows, presentation.Row{grant.ID, grant.WorkspaceID, grant.SimilarCommandPattern, formatRequestTime(grant.GrantExpiresAt)})
	}
	presenter.Section("Runtime session grants")
	presenter.Rows([]string{"ID", "Workspace", "Pattern", "Expires"}, rows...)
}

func renderApprovalRequest(presenter *presentation.Presenter, request approval.Request) {
	fields := []presentation.Field{
		{Label: "id", Value: request.ID},
		{Label: "status", Value: request.Status},
		{Label: "title", Value: request.Title},
		{Label: "workspace", Value: request.WorkspaceID},
		{Label: "tool", Value: request.TargetTool},
	}
	if request.Source != "" {
		fields = append(fields, presentation.Field{Label: "source", Value: request.Source})
	}
	if request.SessionHash != "" {
		fields = append(fields, presentation.Field{Label: "session", Value: request.SessionHash})
	}
	fields = append(fields,
		presentation.Field{Label: "guard", Value: request.GuardCode},
		presentation.Field{Label: "created", Value: formatRequestTime(request.CreatedAt)},
		presentation.Field{Label: "expires", Value: formatRequestTime(request.ExpiresAt)},
	)
	if !request.ResolvedAt.IsZero() {
		fields = append(fields, presentation.Field{Label: "resolved", Value: formatRequestTime(request.ResolvedAt)})
	}
	if request.ResolvedBy != "" {
		fields = append(fields, presentation.Field{Label: "resolved by", Value: request.ResolvedBy})
	}
	if request.Reason != "" {
		fields = append(fields, presentation.Field{Label: "reason", Value: request.Reason})
	}
	if !request.RetryUntil.IsZero() {
		fields = append(fields, presentation.Field{Label: "retry until", Value: formatRequestTime(request.RetryUntil)})
	}
	if request.RuntimeSessionGrant {
		fields = append(fields, presentation.Field{Label: "runtime grant", Value: "all MCP sessions until expiry"})
		if !request.GrantExpiresAt.IsZero() {
			fields = append(fields, presentation.Field{Label: "grant expires", Value: formatRequestTime(request.GrantExpiresAt)})
		}
		if request.SimilarCommandPattern != "" {
			fields = append(fields, presentation.Field{Label: "similar pattern", Value: request.SimilarCommandPattern})
		}
	}
	if !request.ConsumedAt.IsZero() {
		fields = append(fields, presentation.Field{Label: "consumed", Value: formatRequestTime(request.ConsumedAt)})
	}
	if request.GuardReason != "" {
		fields = append(fields, presentation.Field{Label: "guard reason", Value: request.GuardReason})
	}
	presenter.Section("Control approval request")
	presenter.Fields(fields...)
	if len(request.Arguments) > 0 {
		presenter.Spacer()
		presenter.Note("Arguments", formatApprovalArguments(request.Arguments))
	}
}

func formatApprovalArguments(arguments json.RawMessage) string {
	if len(arguments) == 0 {
		return ""
	}
	var output bytes.Buffer
	if err := json.Indent(&output, arguments, "", "  "); err == nil {
		return output.String()
	}
	return strings.TrimSpace(string(arguments))
}

func formatRequestTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format(time.RFC3339Nano)
}
