package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	cmd := &cobra.Command{Use: "request", Short: "Review and resolve control approval requests"}
	cmd.AddCommand(requestListCommand(), requestViewCommand(), requestResolveCommand(true), requestResolveCommand(false), requestGrantCommand())
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
	cmd := &cobra.Command{Use: "list", Short: "List active similar-command runtime grants", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint")
		var progress *commandProgress
		if !asJSON {
			progress = newCommandProgress(cmd, "REQUEST")
			progress.Start("request.loading", "Loading runtime session grants", "Loaded runtime session grants")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		grants, err := application.ListRuntimeGrants(ctx, workspaceID)
		if err != nil {
			progress.Stop()
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, grants)
		}
		progress.Complete()
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
		var progress *commandProgress
		if !asJSON {
			progress = newCommandProgress(cmd, "REQUEST")
			progress.Start("request.resolving", "Revoking runtime session grant", "Runtime session grant revoked")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		request, err := application.RevokeRuntimeGrant(ctx, args[0])
		if err != nil {
			progress.Stop()
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, request)
		}
		progress.Complete()
		renderEntityMutationSuccess(cmd, "Runtime session grant revoked", request.ID, presentation.Field{Label: "status", Value: request.Status})
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Short: "List control approval requests from the running runtime", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint")
		var progress *commandProgress
		if !asJSON {
			progress = newCommandProgress(cmd, "REQUEST")
			progress.Start("request.loading", "Loading approval requests", "Loaded approval requests")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		requests, err := requestRuntimeApprovalList(ctx)
		if err != nil {
			progress.Stop()
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, requests)
		}
		progress.Complete()
		renderApprovalRequests(commandPresenter(cmd), requests)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestViewCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "view <request_id>", Short: "Show one control approval request by ID or unique prefix", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint", logger.WithVerbose("request", args[0]))
		var progress *commandProgress
		if !asJSON {
			progress = newCommandProgress(cmd, "REQUEST")
			progress.Start("request.loading", "Loading approval request", "Loaded approval request")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		request, err := requestRuntimeApprovalView(ctx, args[0])
		if err != nil {
			progress.Stop()
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, request)
		}
		progress.Complete()
		renderApprovalRequest(commandPresenter(cmd), request)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func requestResolveCommand(approve bool) *cobra.Command {
	action, past := "deny", "denied"
	label := "Deny"
	if approve {
		action, past = "approve", "approved"
		label = "Approve"
	}
	progress := "Denying approval request"
	if approve {
		progress = "Approving approval request"
	}
	var asJSON bool
	var reason string
	var allowSimilar bool
	cmd := &cobra.Command{Use: action + " <request_id>", Short: label + " one pending control approval request by ID or unique prefix", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandStep(cmd, "REQUEST", "request.runtime.contacting", "Contacting runtime approval endpoint", logger.WithVerbose("action", action), logger.WithVerbose("request", args[0]))
		var commandPhase *commandProgress
		if !asJSON {
			commandPhase = newCommandProgress(cmd, "REQUEST")
			commandPhase.Start("request.resolving", progress, "Approval request "+past)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), requestControlTimeout)
		defer cancel()
		var request approval.Request
		var err error
		if approve {
			if allowSimilar {
				request, err = application.ResolveApprovalRequestWithRuntimeGrant(ctx, args[0], true, true, reason)
			} else {
				request, err = requestRuntimeApprovalApprove(ctx, args[0], reason)
			}
		} else {
			request, err = requestRuntimeApprovalDeny(ctx, args[0], reason)
		}
		if err != nil {
			commandPhase.Stop()
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, request)
		}
		commandPhase.Complete()
		fields := []presentation.Field{{Label: "status", Value: request.Status}}
		if !request.RetryUntil.IsZero() {
			fields = append(fields, presentation.Field{Label: "retry until", Value: request.RetryUntil.Format(time.RFC3339Nano)})
		}
		if request.Reason != "" {
			fields = append(fields, presentation.Field{Label: "reason", Value: request.Reason})
		}
		renderEntityMutationSuccess(cmd, "Approval request "+past, request.ID, fields...)
		return nil
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "record an optional approval resolution reason")
	if approve {
		cmd.Flags().BoolVar(&allowSimilar, "allow-similar", false, "approve similar matching commands for the bounded runtime grant window")
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderApprovalRequests(presenter *presentation.Presenter, requests []approval.Request) {
	presenter.Frame("Control approval requests")
	if len(requests) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No control approval requests")
		presenter.Complete("Done")
		return
	}
	rows := make([]presentation.Row, 0, len(requests))
	for _, request := range requests {
		rows = append(rows, presentation.Row{request.ID, string(request.Status), request.WorkspaceID, request.TargetTool, request.Title})
	}
	presenter.Section(fmt.Sprintf("Requests · %d", len(requests)))
	presenter.Rows([]string{"ID", "Status", "Workspace", "Tool", "Title"}, rows...)
	presenter.Complete("Done")
}

func renderRuntimeGrants(presenter *presentation.Presenter, grants []approval.Request) {
	presenter.Frame("Runtime session grants")
	if len(grants) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No active runtime session grants")
		presenter.Complete("Done")
		return
	}
	rows := make([]presentation.Row, 0, len(grants))
	for _, grant := range grants {
		rows = append(rows, presentation.Row{grant.ID, grant.WorkspaceID, grant.SimilarCommandPattern, formatRequestTime(grant.GrantExpiresAt)})
	}
	presenter.Section(fmt.Sprintf("Active runtime grants · %d", len(grants)))
	presenter.Rows([]string{"ID", "Workspace", "Pattern", "Expires"}, rows...)
	presenter.Complete("Done")
}

func renderApprovalRequest(presenter *presentation.Presenter, request approval.Request) {
	presenter.Frame("Approval request")
	presenter.StateSection(approvalPresentationKind(request.Status), approvalStatusLabel(request.Status))
	presenter.Subsection(request.ID)
	fields := []presentation.Field{
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
	presenter.NestedFields(fields...)
	if len(request.Arguments) > 0 {
		presenter.Spacer()
		presenter.Section("Arguments")
		presenter.List(formatApprovalArguments(request.Arguments))
	}
	presenter.Complete(approvalOutro(request.Status))
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

func approvalPresentationKind(status approval.Status) presentation.StatusKind {
	switch status {
	case approval.StatusApproved, approval.StatusConsumed:
		return presentation.StatusSuccess
	case approval.StatusDenied, approval.StatusExpired, approval.StatusCancelled:
		return presentation.StatusError
	case approval.StatusPending:
		return presentation.StatusInactive
	default:
		return presentation.StatusInfo
	}
}

func approvalStatusLabel(status approval.Status) string {
	if status == "" {
		return "Unknown"
	}
	value := string(status)
	return strings.ToUpper(value[:1]) + value[1:]
}

func approvalOutro(status approval.Status) string {
	if status == approval.StatusPending {
		return "Awaiting decision"
	}
	return "Done"
}
