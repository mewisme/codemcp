package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
)

const defaultConfigBundleFile = "codemcp-config.json"

func configCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Aliases: []string{"cfg"}, Short: "Read and update validated runtime configuration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	cmd.AddCommand(
		&cobra.Command{Use: "path", Short: "Show the active configuration path, format, and root", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			source, err := application.ConfigSource(cmd.Context())
			if err != nil {
				return err
			}
			log := commandLogger(cmd)
			log.Detail("config", source.Path)
			log.Detail("format", source.Format)
			log.Detail("root", config.RootPath())
			return nil
		}},
		configGetCommand(),
		configListCommand(),
		configExplainCommand(),
		configSetCommand(),
		configMigrateCommand(),
		configExportCommand(),
		configImportCommand(),
		configVerifyCommand(),
	)
	return cmd
}

func configExportCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "export [file]",
		Short: "Export portable non-secret configuration and state as JSON",
		Long:  "Export portable non-secret configuration and state as a versioned JSON envelope. Managed secrets are excluded. The default file is " + defaultConfigBundleFile + " in the current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := configBundleFile(args)
			logCommandStep(cmd, "CONFIG", "config.export.preparing", "Exporting configuration envelope", logger.WithVerbose("file", file))
			result, err := application.ExportConfigContext(cmd.Context(), file, force)
			if err != nil {
				return fmt.Errorf("export configuration envelope: %w", err)
			}
			fields := []presentation.Field{{Label: "file", Value: result.Path}, {Label: "files", Value: result.Files}, {Label: "source", Value: result.Source.OS + "/" + result.Source.Arch}}
			if result.SkippedFiles > 0 {
				fields = append(fields, presentation.Field{Label: "non-portable/runtime files skipped", Value: result.SkippedFiles})
			}
			renderMutationSuccess(cmd, "Export configuration", "Configuration exported", fields...)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing export file")
	return cmd
}

func configImportCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "import [file]",
		Short: "Import a portable JSON configuration envelope",
		Long:  "Import a versioned JSON configuration envelope. The envelope never carries managed secrets; existing target secrets are preserved on forced imports. The default file is " + defaultConfigBundleFile + " in the current directory.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := configBundleFile(args)
			logCommandStep(cmd, "CONFIG", "config.import.preparing", "Importing configuration envelope", logger.WithVerbose("file", file))
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			result, err := application.ImportConfig(ctx, file, force)
			if err != nil {
				return fmt.Errorf("import configuration envelope: %w", err)
			}
			fields := []presentation.Field{{Label: "files", Value: result.Files}, {Label: "source", Value: result.Source.OS + "/" + result.Source.Arch}, {Label: "target", Value: result.Target.OS + "/" + result.Target.Arch}}
			if result.SkippedPaths > 0 {
				fields = append(fields, presentation.Field{Label: "unavailable platform paths skipped", Value: result.SkippedPaths})
			}
			if result.SkippedFiles > 0 {
				fields = append(fields, presentation.Field{Label: "orphaned workspace state skipped", Value: result.SkippedFiles})
			}
			if result.BackupPath != "" {
				fields = append(fields, presentation.Field{Label: "previous config backup retained", Value: result.BackupPath})
			}
			renderMutationSuccess(cmd, "Import configuration", "Configuration imported", fields...)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace existing configuration/state")
	return cmd
}

func configBundleFile(args []string) string {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return args[0]
	}
	return defaultConfigBundleFile
}

func configGetCommand() *cobra.Command {
	options := configOutputOptions{}
	cmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Get a redacted config value or subtree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "CONFIG", "config.loading", "Loading configuration")
			cfg, err := application.LoadConfig(cmd.Context())
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}
			key := ""
			if len(args) > 0 {
				key = args[0]
			}
			return printConfigSelection(cmd, cfg, key, false, options)
		},
	}
	addConfigOutputFlags(cmd, &options)
	cmd.ValidArgsFunction = completeConfigSelection
	return cmd
}

func configListCommand() *cobra.Command {
	options := configOutputOptions{}
	cmd := &cobra.Command{
		Use:     "list [key]",
		Aliases: []string{"ls"},
		Short:   "List redacted configuration with optional subtree",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "CONFIG", "config.loading", "Loading configuration")
			cfg, err := application.LoadConfig(cmd.Context())
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}
			key := ""
			if len(args) > 0 {
				key = args[0]
			}
			return printConfigSelection(cmd, cfg, key, true, options)
		},
	}
	addConfigOutputFlags(cmd, &options)
	cmd.ValidArgsFunction = completeConfigSelection
	return cmd
}

func configSetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one typed configuration value; key=value is also accepted",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, raw, err := parseConfigSetArgs(args)
			if err != nil {
				return err
			}
			logCommandStep(cmd, "CONFIG", "config.value.updating", "Updating configuration value", logger.WithVerbose("key", key))
			beginMutationProgress(cmd, "Update configuration")
			if _, err := application.SetConfigField(cmd.Context(), key, raw); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Update configuration", "Value saved", presentation.Field{Label: "key", Value: key})
			return nil
		},
	}
	cmd.ValidArgsFunction = completeConfigSet
	return cmd
}

func parseConfigSetArgs(args []string) (string, string, error) {
	if len(args) == 2 {
		key := strings.TrimSpace(args[0])
		if key == "" {
			return "", "", errors.New("config key is required")
		}
		return key, args[1], nil
	}
	key, value, ok := strings.Cut(args[0], "=")
	if !ok || strings.TrimSpace(key) == "" {
		return "", "", errors.New("use config set <key> <value> or config set key=value")
	}
	return strings.TrimSpace(key), value, nil
}

func setConfigValue(cfg *config.Config, key, raw string) error {
	return config.SetValue(cfg, key, raw)
}

func getConfigValue(cfg config.Config, key string) (any, error) {
	return config.RedactedValueAt(cfg, key)
}

func configMigrateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "migrate", Short: "Migrate legacy plaintext credentials into the secret file store", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandStep(cmd, "CONFIG", "config.secrets.migrating", "Migrating legacy credentials")
		if err := application.MigrateLegacySecretsContext(cmd.Context()); err != nil {
			return fmt.Errorf("migrate legacy credentials: %w", err)
		}
		renderMutationSuccess(cmd, "Migrate credentials", "Credentials migrated to secret file store")
		return nil
	}}
	cmd.AddCommand(configMigrateSecretsCommand())
	return cmd
}

func configMigrateSecretsCommand() *cobra.Command {
	return &cobra.Command{Use: "secrets", Short: "Migrate legacy secret files to encrypted JSON envelopes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandStep(cmd, "CONFIG", "config.secrets.envelope.migrating", "Migrating legacy secret files to encrypted JSON envelopes")
		migrated, err := application.MigrateSecretEnvelopesContext(cmd.Context())
		if err != nil {
			return fmt.Errorf("migrate secret envelopes: %w", err)
		}
		renderMutationSuccess(cmd, "Migrate secret files", "Legacy secret files migrated to encrypted JSON envelopes", presentation.Field{Label: "migrated", Value: migrated})
		return nil
	}}
}

func configVerifyCommand() *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:     "verify",
		Aliases: []string{"validate"},
		Short:   "Verify JSON configuration and structured state consistency",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "CONFIG", "config.verifying", "Verifying configuration and state")
			result, err := application.VerifyConfigContext(cmd.Context())
			if err != nil {
				return fmt.Errorf("verify configuration: %w", err)
			}
			log := commandLogger(cmd)
			for _, warning := range result.Warnings {
				log.Warning("CONFIG", "config.verify.warning", warning, nil)
			}
			if strict && len(result.Warnings) > 0 {
				return fmt.Errorf("configuration verified with %d warning(s); re-run without --strict to treat warnings as advisory", len(result.Warnings))
			}
			log.Success("CONFIG", "configuration verified", "format", result.Format, "files", result.Files, "warnings", len(result.Warnings))
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "fail when security policy warnings are present")
	return cmd
}
