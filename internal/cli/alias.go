package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/install"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/version"
)

func aliasCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "alias", Short: "Manage the cgm command alias"}
	cmd.AddCommand(aliasInstallCommand(), aliasRemoveCommand(), aliasStatusCommand())
	return cmd
}

func aliasInstallCommand() *cobra.Command {
	return &cobra.Command{Use: "install", Short: "Install the cgm command alias", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "ALIAS", "alias.layout.resolving", "Resolving managed installation layout")
		layout, err := managedAliasLayoutContext(cmd.Context())
		if err != nil {
			return err
		}
		status, err := install.InstallAliasContext(cmd.Context(), layout)
		if err != nil {
			return err
		}
		log := commandLogger(cmd)
		log.Success("ALIAS", "alias installed")
		log.Detail("path", status.Path)
		log.Detail("target", status.Target)
		return nil
	}}
}

func aliasRemoveCommand() *cobra.Command {
	return &cobra.Command{Use: "remove", Short: "Remove the cgm command alias", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "ALIAS", "alias.layout.resolving", "Resolving managed installation layout")
		layout, err := managedAliasLayoutContext(cmd.Context())
		if err != nil {
			return err
		}
		before, err := install.StatusAliasContext(cmd.Context(), layout)
		if err != nil {
			return err
		}
		status, err := install.RemoveAliasContext(cmd.Context(), layout)
		if err != nil {
			return err
		}
		log := commandLogger(cmd)
		if before.State == install.AliasMissing {
			log.Notice("ALIAS", "alias.missing", "Alias is not installed")
		} else {
			log.Success("ALIAS", "alias removed")
		}
		log.Detail("path", status.Path)
		return nil
	}}
}

func aliasStatusCommand() *cobra.Command {
	return &cobra.Command{Use: "status", Aliases: []string{"st"}, Short: "Show the cgm command alias status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		logCommandStep(cmd, "ALIAS", "alias.status.inspecting", "Inspecting cgm alias state")
		layout, err := managedAliasLayoutContext(cmd.Context())
		if err != nil {
			return err
		}
		status, err := install.StatusAliasContext(cmd.Context(), layout)
		if err != nil {
			return err
		}
		log := commandLogger(cmd)
		switch status.State {
		case install.AliasInstalled:
			log.Ready("ALIAS", "alias.installed", "Alias installed")
		case install.AliasMissing:
			log.Notice("ALIAS", "alias.missing", "Alias not installed")
		case install.AliasConflict:
			log.Warning("ALIAS", "alias.conflict", "Alias path is occupied by another file", nil)
		default:
			return fmt.Errorf("unknown alias state %q", status.State)
		}
		log.Detail("path", status.Path)
		log.Detail("target", status.Target)
		return nil
	}}
}

func managedAliasLayoutContext(ctx context.Context) (install.Layout, error) {
	span := tracepkg.Start(ctx, "INSTALL", "install.alias.layout", "Resolving managed alias layout")
	detection, err := install.DetectCurrent(version.Version)
	if err != nil {
		span.FailMessage("Managed alias installation detection failed", err)
		return install.Layout{}, err
	}
	layout, err := detection.ManagedLayout()
	if err != nil {
		span.FailMessage("Managed alias layout resolution failed", err, tracepkg.String("method", string(detection.Method)))
		return install.Layout{}, fmt.Errorf("managed installation not found; run chatgpt-mcp install: %w", err)
	}
	span.EndMessage("Managed alias layout resolved", tracepkg.String("method", string(detection.Method)), tracepkg.String("root", layout.Root), tracepkg.String("alias", layout.AliasPath), tracepkg.String("target", layout.CurrentBinary))
	return layout, nil
}
