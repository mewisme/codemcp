package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
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
		log.Success("REQUEST", "runtime session grants loaded", "count", len(grants))
		for _, grant := range grants {
			log.Detail(grant.ID, fmt.Sprintf("workspace=%s pattern=%s expires=%s", grant.WorkspaceID, grant.SimilarCommandPattern, grant.GrantExpiresAt.Format(time.RFC3339Nano)))
		}
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
		log.Success("REQUEST", "control approval requests loaded", "count", len(requests))
		for _, request := range requests {
			log.Detail(request.ID, fmt.Sprintf("status=%s workspace=%s tool=%s title=%s", request.Status, request.WorkspaceID, request.TargetTool, request.Title))
		}
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
		printApprovalRequest(cmd, request)
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

func printApprovalRequest(cmd *cobra.Command, request approval.Request) {
	log := commandLogger(cmd)
	log.Info("REQUEST", "control approval request", "id", request.ID)
	log.Detail("status", request.Status)
	log.Detail("title", request.Title)
	log.Detail("workspace", request.WorkspaceID)
	log.Detail("tool", request.TargetTool)
	if request.Source != "" {
		log.Detail("source", request.Source)
	}
	if request.SessionHash != "" {
		log.Detail("session", request.SessionHash)
	}
	log.Detail("guard", request.GuardCode)
	log.Detail("created", request.CreatedAt.Format(time.RFC3339Nano))
	log.Detail("expires", request.ExpiresAt.Format(time.RFC3339Nano))
	if !request.ResolvedAt.IsZero() {
		log.Detail("resolved", request.ResolvedAt.Format(time.RFC3339Nano))
	}
	if request.ResolvedBy != "" {
		log.Detail("resolved_by", request.ResolvedBy)
	}
	if request.Reason != "" {
		log.Detail("reason", request.Reason)
	}
	if !request.RetryUntil.IsZero() {
		log.Detail("retry_until", request.RetryUntil.Format(time.RFC3339Nano))
	}
	if request.RuntimeSessionGrant {
		log.Detail("runtime_grant", "all MCP sessions until expiry")
		if !request.GrantExpiresAt.IsZero() {
			log.Detail("grant_expires", request.GrantExpiresAt.Format(time.RFC3339Nano))
		}
		if request.SimilarCommandPattern != "" {
			log.Detail("similar_pattern", request.SimilarCommandPattern)
		}
	}
	if !request.ConsumedAt.IsZero() {
		log.Detail("consumed", request.ConsumedAt.Format(time.RFC3339Nano))
	}
	if len(request.Arguments) > 0 {
		log.Detail("arguments", string(request.Arguments))
	}
	if request.GuardReason != "" {
		log.Detail("reason_guard", request.GuardReason)
	}
}
