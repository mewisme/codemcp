package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
)

const scopedSettingsAnnotation = "cm.scoped_settings"

func markScopedSettings(cmd *cobra.Command, keys ...string) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[scopedSettingsAnnotation] = strings.Join(keys, ",")
	return cmd
}

func settingService() *application.SettingService {
	return application.NewSettingService()
}

func scopedSettingSet(cmd *cobra.Command, key, value string) error {
	_, err := settingService().Set(cmd.Context(), key, value)
	return err
}

func scopedToggleCommand(use, title, success, key string, enabled bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: title,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := scopedSettingSet(cmd, key, strconv.FormatBool(enabled)); err != nil {
				return err
			}
			renderMutationSuccess(cmd, title, success, presentation.Field{Label: "setting", Value: key})
			return nil
		},
	}
	return markScopedSettings(cmd, key)
}

func scopedValueCommand(use, title, success, key string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use + " <value>",
		Short: title,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := scopedSettingSet(cmd, key, args[0]); err != nil {
				return err
			}
			renderMutationSuccess(cmd, title, success, presentation.Field{Label: "setting", Value: key})
			return nil
		},
	}
	return markScopedSettings(cmd, key)
}

func scopedListAddRemoveCommand(use, title, success, key string, add bool) *cobra.Command {
	action := "remove"
	if add {
		action = "add"
	}
	cmd := &cobra.Command{
		Use:   action + " <value>",
		Short: title,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			current, err := settingService().Read(cmd.Context(), key)
			if err != nil {
				return err
			}
			values := splitScopedSettingList(current.Value)
			value := strings.TrimSpace(args[0])
			if value == "" {
				return fmt.Errorf("value is required")
			}
			changed := false
			if add {
				for _, existing := range values {
					if existing == value {
						renderMutationSuccess(cmd, title, success, presentation.Field{Label: "setting", Value: key})
						return nil
					}
				}
				values = append(values, value)
				changed = true
			} else {
				filtered := make([]string, 0, len(values))
				for _, existing := range values {
					if existing == value {
						changed = true
						continue
					}
					filtered = append(filtered, existing)
				}
				values = filtered
			}
			if changed {
				if err := scopedSettingSet(cmd, key, strings.Join(values, ",")); err != nil {
					return err
				}
			}
			renderMutationSuccess(cmd, title, success, presentation.Field{Label: "setting", Value: key})
			return nil
		},
	}
	return markScopedSettings(cmd, key)
}

func splitScopedSettingList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func serverSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "server", Short: "Manage MCP HTTP server settings"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable MCP HTTP server", "MCP HTTP server enabled", "server.enabled", true),
		scopedToggleCommand("disable", "Disable MCP HTTP server", "MCP HTTP server disabled", "server.enabled", false),
		scopedValueCommand("port", "Set MCP HTTP port", "MCP HTTP port updated", "server.port"),
	)
	expose := &cobra.Command{Use: "expose", Short: "Manage MCP HTTP exposure"}
	expose.AddCommand(scopedValueCommand("mode", "Set MCP HTTP exposure mode", "Exposure mode updated", "server.expose.mode"))
	interfaces := &cobra.Command{Use: "interface", Short: "Manage exposed network interfaces"}
	interfaces.AddCommand(
		scopedListAddRemoveCommand("interface", "Add exposed network interface", "Network interface added", "server.expose.interfaces", true),
		scopedListAddRemoveCommand("interface", "Remove exposed network interface", "Network interface removed", "server.expose.interfaces", false),
	)
	insecure := &cobra.Command{Use: "insecure", Short: "Manage insecure transport policy"}
	http := &cobra.Command{Use: "http", Short: "Manage insecure HTTP policy"}
	http.AddCommand(
		scopedToggleCommand("allow", "Allow authenticated insecure HTTP", "Insecure HTTP allowed", "server.allow_insecure_http", true),
		scopedToggleCommand("deny", "Disallow insecure HTTP", "Insecure HTTP disallowed", "server.allow_insecure_http", false),
	)
	insecure.AddCommand(http)
	loopback := &cobra.Command{Use: "loopback", Short: "Manage loopback authentication policy"}
	auth := &cobra.Command{Use: "auth", Short: "Manage loopback authentication requirement"}
	auth.AddCommand(
		scopedToggleCommand("allow", "Allow unauthenticated loopback", "Unauthenticated loopback allowed", "server.allow_unauthenticated_loopback", true),
		scopedToggleCommand("require", "Require loopback authentication", "Loopback authentication required", "server.allow_unauthenticated_loopback", false),
	)
	loopback.AddCommand(auth)
	cmd.AddCommand(expose, interfaces, insecure, loopback)
	return cmd
}

func adminSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Manage Admin HTTP server settings"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable Admin HTTP server", "Admin HTTP server enabled", "admin.enabled", true),
		scopedToggleCommand("disable", "Disable Admin HTTP server", "Admin HTTP server disabled", "admin.enabled", false),
		scopedValueCommand("port", "Set Admin HTTP port", "Admin HTTP port updated", "admin.port"),
	)
	return cmd
}

func permissionsSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "permissions", Short: "Manage global permission settings"}
	allow := &cobra.Command{Use: "allow", Short: "Manage global allow rules"}
	dir := &cobra.Command{Use: "dir", Short: "Manage globally allowed directories"}
	dir.AddCommand(
		scopedListAddRemoveCommand("dir", "Add globally allowed directory", "Allowed directory added", "permissions.allow_dirs", true),
		scopedListAddRemoveCommand("dir", "Remove globally allowed directory", "Allowed directory removed", "permissions.allow_dirs", false),
	)
	allow.AddCommand(dir)
	cmd.AddCommand(allow)
	return cmd
}

func shellSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "shell", Short: "Manage shell settings"}
	cmd.AddCommand(scopedValueCommand("path", "Set executable search paths", "Executable search paths updated", "shell.path"))
	return cmd
}

func integrationSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "integration", Short: "Manage first-party integration settings"}
	cmd.AddCommand(
		integrationModeSettingsCommand("ponytail", []string{"lite", "full", "ultra"}),
		integrationModeSettingsCommand("caveman", []string{"lite", "full", "ultra", "wenyan-lite", "wenyan-full", "wenyan-ultra"}),
		integrationBinarySettingsCommand("rtk"),
		integrationBinarySettingsCommand("codegraph"),
	)
	return cmd
}

func integrationModeSettingsCommand(name string, modes []string) *cobra.Command {
	prefix := "integrations." + name
	cmd := &cobra.Command{Use: name, Short: "Manage " + name + " integration settings"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable "+name+" integration", name+" integration enabled", prefix+".active", true),
		scopedToggleCommand("disable", "Disable "+name+" integration", name+" integration disabled", prefix+".active", false),
	)
	mode := scopedValueCommand("mode", "Set "+name+" integration mode", name+" integration mode updated", prefix+".mode")
	mode.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterCompletions(modes, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	cmd.AddCommand(mode)
	return cmd
}

func integrationBinarySettingsCommand(name string) *cobra.Command {
	prefix := "integrations." + name
	cmd := &cobra.Command{Use: name, Short: "Manage " + name + " integration settings"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable "+name+" integration", name+" integration enabled", prefix+".enabled", true),
		scopedToggleCommand("disable", "Disable "+name+" integration", name+" integration disabled", prefix+".enabled", false),
		scopedValueCommand("path", "Set "+name+" executable path", name+" executable path updated", prefix+".path"),
	)
	return cmd
}
