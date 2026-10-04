package cli

import (
	"context"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/integrations/cftunnel"
)

func cfTunnelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cf",
		Short: "Manage the Cloudflare Quick Tunnel integration",
		Long:  "Manage the verified cf-tunnel integration used for ephemeral Telegram Logs Mini App ingress. OpenAI Secure MCP Tunnel remains the persistent MCP tunnel authority.",
	}
	cmd.AddCommand(
		tunnelCFStatusCommand(),
		tunnelCFProbeCommand(),
		tunnelCFInstallCommand(),
		tunnelCFUpdateCommand(),
		tunnelCFRemoveCommand(),
	)
	return cmd
}

func tunnelCFStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "status", Short: "Show Cloudflare Quick Tunnel integration status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		status, err := application.NewCFTunnelService().Status(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, status)
		}
		renderCFTunnelStatus(commandPresenter(cmd), status, "")
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func tunnelCFProbeCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "probe", Short: "Verify the resolved cf-tunnel executable contract", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := application.NewCFTunnelService().Probe(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result)
		}
		renderCFTunnelStatus(commandPresenter(cmd), result.Status, result.Version)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func tunnelCFInstallCommand() *cobra.Command {
	return tunnelCFInstallLikeCommand("install", "Install the latest verified cf-tunnel managed asset", func(ctx context.Context, service *application.CFTunnelService) (cftunnel.InstallResult, error) {
		return service.Install(ctx)
	})
}

func tunnelCFUpdateCommand() *cobra.Command {
	return tunnelCFInstallLikeCommand("update", "Update the managed cf-tunnel asset to the latest verified release", func(ctx context.Context, service *application.CFTunnelService) (cftunnel.InstallResult, error) {
		return service.Update(ctx)
	})
}

func tunnelCFInstallLikeCommand(use, short string, run func(context.Context, *application.CFTunnelService) (cftunnel.InstallResult, error)) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := run(cmd.Context(), application.NewCFTunnelService())
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result)
		}
		title := "cf-tunnel managed asset ready"
		if result.AlreadyInstalled {
			title = "cf-tunnel managed asset already ready"
		}
		renderMutationSuccess(cmd, title,
			presentation.Field{Label: "version", Value: result.Version},
			presentation.Field{Label: "source", Value: result.Status.Source},
			presentation.Field{Label: "path", Value: result.Path},
			presentation.Field{Label: "verified", Value: result.Status.Verified},
		)
		return nil
	}}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func tunnelCFRemoveCommand() *cobra.Command {
	var asJSON bool
	var confirm bool
	cmd := &cobra.Command{Use: "remove", Short: "Remove only the CodeMCP-managed cf-tunnel asset", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := requireDestructiveConfirmation(confirm, "cf-tunnel removal"); err != nil {
			return err
		}
		result, err := application.NewCFTunnelService().Remove(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result)
		}
		fields := []presentation.Field{{Label: "removed", Value: result.Removed}, {Label: "effective source", Value: result.Status.Source}}
		if result.Status.Path != "" {
			fields = append(fields, presentation.Field{Label: "effective path", Value: result.Status.Path})
		}
		renderMutationSuccess(cmd, "cf-tunnel managed asset removed", fields...)
		return nil
	}}
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm removal of the managed cf-tunnel asset")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderCFTunnelStatus(presenter *presentation.Presenter, status cftunnel.Status, version string) {
	state := "unavailable"
	kind := presentation.StatusInactive
	if status.Source != cftunnel.SourceUnavailable {
		state = "ready"
		kind = presentation.StatusSuccess
	}
	presenter.StateSection(kind, "cf-tunnel is "+state)
	fields := []presentation.Field{
		{Label: "source", Value: status.Source},
		{Label: "managed version", Value: status.Version},
		{Label: "platform", Value: status.Platform},
		{Label: "verified", Value: status.Verified},
		{Label: "managed installed", Value: status.ManagedInstalled},
		{Label: "consumer", Value: status.Consumer},
	}
	if status.Path != "" {
		fields = append(fields, presentation.Field{Label: "path", Value: status.Path})
	}
	if version != "" {
		fields = append(fields, presentation.Field{Label: "reported version", Value: version})
	}
	presenter.Fields(fields...)
}
