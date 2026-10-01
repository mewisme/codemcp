package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
)

const scopedSettingsAnnotation = "cm.scoped_settings"

func markScopedSettings(cmd *cobra.Command, keys ...string) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	current := strings.Split(cmd.Annotations[scopedSettingsAnnotation], ",")
	seen := map[string]struct{}{}
	for _, key := range append(current, keys...) {
		if key = strings.TrimSpace(key); key != "" {
			seen[key] = struct{}{}
		}
	}
	merged := make([]string, 0, len(seen))
	for key := range seen {
		merged = append(merged, key)
	}
	sort.Strings(merged)
	cmd.Annotations[scopedSettingsAnnotation] = strings.Join(merged, ",")
	return cmd
}

func bindCanonicalScopedSettings(root *cobra.Command) {
	if root == nil {
		return
	}
	for _, spec := range config.Settings() {
		for _, path := range spec.ScopedCommands {
			command, remaining, err := root.Find(strings.Fields(path))
			if err != nil || command == nil || len(remaining) != 0 {
				continue
			}
			markScopedSettings(command, spec.Key)
		}
	}
}

func settingService() *application.SettingService {
	return application.NewSettingService()
}

func scopedSettingSet(cmd *cobra.Command, key, value string) error {
	_, err := settingService().Set(cmd.Context(), key, value)
	return err
}

func scopedSettingUnset(cmd *cobra.Command, key string) error {
	_, err := settingService().Unset(cmd.Context(), key)
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
			renderMutationSuccess(cmd, success, presentation.Field{Label: "setting", Value: key})
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
			renderMutationSuccess(cmd, success, presentation.Field{Label: "setting", Value: key})
			return nil
		},
	}
	cmd.ValidArgsFunction = completeScopedSettingValue(key)
	return markScopedSettings(cmd, key)
}

func completeScopedSettingValue(key string) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		spec, ok := config.SettingByKey(key)
		if !ok {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		switch spec.Kind {
		case config.FieldEnum:
			return filterCompletions(spec.Options, toComplete), cobra.ShellCompDirectiveNoFileComp
		case config.FieldBool:
			return filterCompletions([]string{"true", "false"}, toComplete), cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
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
						renderMutationSuccess(cmd, success, presentation.Field{Label: "setting", Value: key})
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
			renderMutationSuccess(cmd, success, presentation.Field{Label: "setting", Value: key})
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

func httpSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "http", Short: "Manage local HTTP endpoints and exposure"}

	mcp := &cobra.Command{Use: "mcp", Short: "Manage the MCP HTTP endpoint"}
	mcp.AddCommand(
		scopedToggleCommand("enable", "Enable MCP HTTP server", "MCP HTTP server enabled", "http.mcp.enabled", true),
		scopedToggleCommand("disable", "Disable MCP HTTP server", "MCP HTTP server disabled", "http.mcp.enabled", false),
		scopedValueCommand("port", "Set MCP HTTP port", "MCP HTTP port updated", "http.mcp.port"),
	)

	admin := &cobra.Command{Use: "admin", Short: "Manage the Admin HTTP endpoint"}
	admin.AddCommand(
		scopedToggleCommand("enable", "Enable Admin HTTP server", "Admin HTTP server enabled", "http.admin.enabled", true),
		scopedToggleCommand("disable", "Disable Admin HTTP server", "Admin HTTP server disabled", "http.admin.enabled", false),
		scopedValueCommand("port", "Set Admin HTTP port", "Admin HTTP port updated", "http.admin.port"),
	)

	exposure := &cobra.Command{Use: "exposure", Short: "Manage shared HTTP network exposure"}
	exposure.AddCommand(scopedValueCommand("mode", "Set HTTP exposure mode", "Exposure mode updated", "http.exposure.mode"))
	interfaces := &cobra.Command{Use: "interface", Short: "Manage exposed network interfaces"}
	interfaces.AddCommand(
		scopedListAddRemoveCommand("interface", "Add exposed network interface", "Network interface added", "http.exposure.interfaces", true),
		scopedListAddRemoveCommand("interface", "Remove exposed network interface", "Network interface removed", "http.exposure.interfaces", false),
	)
	exposure.AddCommand(interfaces)

	security := &cobra.Command{Use: "security", Short: "Manage shared HTTP security policy"}
	insecure := &cobra.Command{Use: "insecure", Short: "Manage insecure HTTP policy"}
	insecure.AddCommand(
		scopedToggleCommand("allow", "Allow authenticated insecure HTTP", "Insecure HTTP allowed", "http.security.allow_insecure", true),
		scopedToggleCommand("deny", "Disallow insecure HTTP", "Insecure HTTP disallowed", "http.security.allow_insecure", false),
	)
	loopback := &cobra.Command{Use: "loopback", Short: "Manage loopback authentication policy"}
	auth := &cobra.Command{Use: "auth", Short: "Manage loopback authentication requirement"}
	auth.AddCommand(
		scopedToggleCommand("allow", "Allow unauthenticated loopback", "Unauthenticated loopback allowed", "http.security.allow_unauthenticated_loopback", true),
		scopedToggleCommand("require", "Require loopback authentication", "Loopback authentication required", "http.security.allow_unauthenticated_loopback", false),
	)
	loopback.AddCommand(auth)
	security.AddCommand(insecure, loopback)

	cmd.AddCommand(mcp, admin, exposure, security)
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
		integrationModeSettingsCommand("ponytail"),
		integrationModeSettingsCommand("caveman"),
		integrationBinarySettingsCommand("rtk"),
		integrationBinarySettingsCommand("codegraph"),
		cfTunnelCommand(),
		integrationTypeSafeSettingsCommand(),
	)
	return cmd
}

func integrationTypeSafeSettingsCommand() *cobra.Command {
	prefix := "integrations.typesafe"
	cmd := &cobra.Command{Use: "typesafe", Short: "Manage TypeSafe integration settings"}
	cmd.AddCommand(
		typeSafeToggleCommand(true),
		typeSafeToggleCommand(false),
		scopedValueCommand("model", "Set TypeSafe model", "TypeSafe model updated", prefix+".model"),
		scopedValueCommand("timeout", "Set TypeSafe timeout in milliseconds", "TypeSafe timeout updated", prefix+".timeout_ms"),
		typeSafeStatusCommand(),
		typeSafeDoctorCommand(),
		typeSafeProbeCommand(),
	)
	key := &cobra.Command{Use: "key", Short: "Manage the TypeSafe API key"}
	var fromEnv string
	set := &cobra.Command{
		Use: "set [api-key]", Short: "Set the TypeSafe API key", Long: "Set the TypeSafe API key.\n\n" + protectedArgumentWarning, Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			explicit := ""
			if len(args) == 1 {
				explicit = args[0]
			}
			secret, err := readProtectedInput(cmd, protectedInputOptions{Label: "API key", Explicit: explicit, ExplicitSet: len(args) == 1, FromEnv: fromEnv})
			if err != nil {
				return err
			}
			defer zeroProtectedString(&secret)
			if err := scopedSettingSet(cmd, prefix+".api_key", secret); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "TypeSafe API key configured", presentation.Field{Label: "setting", Value: prefix + ".api_key"})
			return nil
		},
	}
	set.Flags().StringVar(&fromEnv, "from-env", "", "Read the API key from this environment variable")
	remove := &cobra.Command{
		Use: "remove", Short: "Remove the TypeSafe API key", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := scopedSettingUnset(cmd, prefix+".api_key"); err != nil {
				return err
			}
			renderMutationSuccess(cmd, "TypeSafe API key removed", presentation.Field{Label: "setting", Value: prefix + ".api_key"})
			return nil
		},
	}
	key.AddCommand(markScopedSettings(set, prefix+".api_key"), markScopedSettings(remove, prefix+".api_key"))
	cmd.AddCommand(key)
	return cmd
}

func typeSafeDoctorCommand() *cobra.Command {
	var probe bool
	cmd := &cobra.Command{
		Use: "doctor", Short: "Check TypeSafe integration health", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewTypeSafeService().Doctor(cmd.Context(), probe)
			presenter := commandPresenter(cmd)
			presenter.Frame("TypeSafe doctor")
			for _, check := range result.Checks {
				status := presentation.StatusSuccess
				if !check.OK {
					status = presentation.StatusWarning
				}
				presenter.StateSection(status, check.Message)
			}
			if err != nil {
				return err
			}
			presenter.Complete("Doctor complete")
			return nil
		},
	}
	cmd.Flags().BoolVar(&probe, "probe", false, "also probe the TypeSafe provider over the network")
	return cmd
}

func typeSafeStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show TypeSafe integration status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := application.NewTypeSafeService().Status(cmd.Context())
			if err != nil {
				return err
			}
			renderTypeSafeStatus(commandPresenter(cmd), status)
			return nil
		},
	}
}

func typeSafeToggleCommand(enabled bool) *cobra.Command {
	action, message := "disable", "TypeSafe integration disabled"
	if enabled {
		action, message = "enable", "TypeSafe integration enabled"
	}
	return &cobra.Command{
		Use: action, Short: strings.ToUpper(action[:1]) + action[1:] + " TypeSafe integration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := application.NewTypeSafeService()
			var status application.TypeSafeStatus
			var err error
			if enabled {
				status, err = service.Enable(cmd.Context())
			} else {
				status, err = service.Disable(cmd.Context())
			}
			if err != nil {
				return err
			}
			renderMutationSuccess(cmd, message, presentation.Field{Label: "state", Value: status.State})
			return nil
		},
	}
}

func typeSafeProbeCommand() *cobra.Command {
	return &cobra.Command{
		Use: "probe", Short: "Probe TypeSafe model availability", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewTypeSafeService().Probe(cmd.Context())
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			presenter.Frame("TypeSafe probe")
			presenter.StateSection(presentation.StatusSuccess, "TypeSafe model is available")
			presenter.NestedFields(
				presentation.Field{Label: "model", Value: result.Status.Model},
				presentation.Field{Label: "http", Value: result.Provider.HTTPStatus},
			)
			presenter.Complete("Probe complete")
			return nil
		},
	}
}

func renderTypeSafeStatus(presenter *presentation.Presenter, status application.TypeSafeStatus) {
	presenter.Frame("TypeSafe integration")
	kind := presentation.StatusInfo
	if status.State == application.TypeSafeReady {
		kind = presentation.StatusSuccess
	} else if status.State == application.TypeSafeMisconfigured || status.State == application.TypeSafeDegraded {
		kind = presentation.StatusWarning
	}
	presenter.StateSection(kind, "TypeSafe is "+string(status.State))
	presenter.NestedFields(
		presentation.Field{Label: "enabled", Value: status.Enabled},
		presentation.Field{Label: "api key", Value: configuredLabel(status.APIKeyConfigured)},
		presentation.Field{Label: "model", Value: status.Model},
		presentation.Field{Label: "timeout ms", Value: status.TimeoutMS},
	)
	presenter.Complete("Status complete")
}

func configuredLabel(configured bool) string {
	if configured {
		return "configured"
	}
	return "not configured"
}

func integrationModeSettingsCommand(name string) *cobra.Command {
	prefix := "integrations." + name
	cmd := &cobra.Command{Use: name, Short: "Manage " + name + " integration settings"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable "+name+" integration", name+" integration enabled", prefix+".active", true),
		scopedToggleCommand("disable", "Disable "+name+" integration", name+" integration disabled", prefix+".active", false),
	)
	cmd.AddCommand(scopedValueCommand("mode", "Set "+name+" integration mode", name+" integration mode updated", prefix+".mode"))
	return cmd
}

func integrationBinarySettingsCommand(name string) *cobra.Command {
	prefix := "integrations." + name
	cmd := &cobra.Command{Use: name, Short: "Manage " + name + " integration settings"}
	if name == "rtk" {
		cmd.AddCommand(rtkToggleCommand(true), rtkToggleCommand(false))
	} else {
		cmd.AddCommand(
			scopedToggleCommand("enable", "Enable "+name+" integration", name+" integration enabled", prefix+".enabled", true),
			scopedToggleCommand("disable", "Disable "+name+" integration", name+" integration disabled", prefix+".enabled", false),
		)
	}
	cmd.AddCommand(scopedValueCommand("path", "Set "+name+" executable path", name+" executable path updated", prefix+".path"))
	switch name {
	case "rtk":
		install := rtkInstallCommand()
		install.AddCommand(rtkInstallGlobalCommand())
		cmd.AddCommand(rtkStatusCommand(), rtkProbeCommand(), install)
	case "codegraph":
		install := codeGraphInstallCommand()
		install.AddCommand(codeGraphInstallGlobalCommand())
		cmd.AddCommand(
			codeGraphStatusCommand(),
			codeGraphProbeCommand(),
			install,
			codeGraphWorkspaceGroupCommand(),
			codeGraphWorkspaceCommand("init"),
			codeGraphWorkspaceCommand("sync"),
		)
	}
	return cmd
}
