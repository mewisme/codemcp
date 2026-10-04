package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/tunnel"
)

const tunnelAdminTimeout = 30 * time.Second

type tunnelAdminScopeFlags struct {
	organizationID string
	workspaceID    string
	tenantID       string
}

func (flags *tunnelAdminScopeFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&flags.organizationID, "organization-id", "", "OpenAI organization scope")
	cmd.Flags().StringVar(&flags.workspaceID, "workspace-id", "", "OpenAI workspace scope")
	cmd.Flags().StringVar(&flags.tenantID, "tenant-id", "", "OpenAI tenant scope")
}

func (flags tunnelAdminScopeFlags) changed(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("organization-id") || cmd.Flags().Changed("workspace-id") || cmd.Flags().Changed("tenant-id")
}

func (flags tunnelAdminScopeFlags) scope() tunnel.AdminScope {
	return tunnel.AdminScope{OrganizationID: strings.TrimSpace(flags.organizationID), WorkspaceID: strings.TrimSpace(flags.workspaceID), TenantID: strings.TrimSpace(flags.tenantID)}
}

func resolveTunnelAdminScope(cmd *cobra.Command, cfg tunnel.Config, flags tunnelAdminScopeFlags) (tunnel.AdminScope, error) {
	scope := tunnel.AdminScopeFromConfig(cfg)
	if flags.changed(cmd) {
		scope = flags.scope()
	}
	if err := tunnel.ValidateAdminScope(scope); err != nil {
		return tunnel.AdminScope{}, err
	}
	return scope, nil
}

func tunnelAdminCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Manage OpenAI tunnel administration"}
	cmd.AddCommand(
		tunnelAdminKeyCommand(),
		tunnelAdminScopeCommand("organization", "tunnel.admin.organization_id"),
		tunnelAdminScopeCommand("workspace", "tunnel.admin.workspace_id"),
		tunnelAdminScopeCommand("tenant", "tunnel.admin.tenant_id"),
		scopedToggleCommand("enable", "Enable OpenAI tunnel administration", "Tunnel administration enabled", "tunnel.admin.enabled", true),
		scopedToggleCommand("disable", "Disable OpenAI tunnel administration", "Tunnel administration disabled", "tunnel.admin.enabled", false),
		tunnelAdminVerifyCommand(),
	)
	return cmd
}

func tunnelAdminScopeCommand(name, key string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "Manage OpenAI tunnel admin " + name + " scope"}
	cmd.AddCommand(scopedValueCommand("set", "Set OpenAI tunnel admin "+name+" scope", "Tunnel admin scope updated", key))
	return cmd
}

func tunnelAdminKeyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Manage the stored OpenAI tunnel admin key"}
	cmd.AddCommand(tunnelAdminKeySetCommand(), tunnelAdminKeyStatusCommand(), tunnelAdminKeyVerifyCommand(), tunnelAdminKeyRemoveCommand())
	return cmd
}

func tunnelAdminKeySetCommand() *cobra.Command {
	var adminKey string
	var fromEnv string
	var scopeFlags tunnelAdminScopeFlags
	cmd := &cobra.Command{
		Use:   "set [admin-api-key]",
		Short: "Store an OpenAI tunnel admin key",
		Long:  "Store the admin key in the secret store without contacting the control plane. Omit the key for protected interactive input; OPENAI_ADMIN_KEY remains a compatibility fallback. An optional organization, workspace, or tenant flag configures the exclusive admin scope in the same atomic mutation. Run verify explicitly after the key and scope are configured.\n\n" + protectedArgumentWarning,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.key.preparing", "Preparing tunnel admin key configuration")
			explicit := ""
			explicitSet := false
			keySource := ""
			if len(args) == 1 {
				explicit = args[0]
				explicitSet = true
				keySource = "argument"
			}
			if explicitSet && cmd.Flags().Changed("admin-key") {
				return errors.New("provide the OpenAI admin key either positionally or with --admin-key, not both")
			}
			if cmd.Flags().Changed("admin-key") {
				explicit = adminKey
				explicitSet = true
				keySource = "flag"
			}
			if explicitSet && strings.TrimSpace(fromEnv) != "" {
				return errors.New("provide the OpenAI admin key either directly or with --from-env, not both")
			}
			if !explicitSet && strings.TrimSpace(fromEnv) != "" {
				keySource = "environment"
			}
			if !explicitSet && strings.TrimSpace(fromEnv) == "" {
				if _, ok := os.LookupEnv("OPENAI_ADMIN_KEY"); ok {
					keySource = "environment"
				} else {
					keySource = "protected-input"
				}
			}
			key, err := readProtectedInput(cmd, protectedInputOptions{
				Label: "Admin API key", Explicit: explicit, ExplicitSet: explicitSet, FromEnv: fromEnv, FallbackEnv: "OPENAI_ADMIN_KEY",
			})
			if err != nil {
				if errors.Is(err, errProtectedInputCancelled) {
					return err
				}
				return fmt.Errorf("OpenAI admin API key: %w", err)
			}
			defer zeroProtectedString(&key)
			var scope *tunnel.AdminScope
			if scopeFlags.changed(cmd) {
				value := scopeFlags.scope()
				scope = &value
			}
			if _, err := settingService().SetWithOptions(cmd.Context(), "tunnel.admin.key", key, application.SettingSetOptions{TunnelAdminScope: scope, SecretSource: keySource}); err != nil {
				return err
			}
			status, err := application.TunnelAdminKeyStatusContext(cmd.Context())
			if err != nil {
				return err
			}
			fields := []presentation.Field{
				{Label: "verification", Value: "required"},
				{Label: "secret store", Value: "secret file store"},
			}
			if tunnel.ValidateAdminScope(status.Scope) == nil {
				fields = append([]presentation.Field{{Label: "scope", Value: formatTunnelAdminScope(status.Scope)}}, fields...)
			}
			renderMutationSuccess(cmd, "Admin key saved", fields...)
			return nil
		},
	}
	cmd.Flags().StringVar(&adminKey, "admin-key", "", "OpenAI admin API key (compatibility; may be retained by process listings)")
	cmd.Flags().StringVar(&fromEnv, "from-env", "", "Read the admin API key from this environment variable")
	scopeFlags.add(cmd)
	return markScopedSettings(cmd, "tunnel.admin.key", "tunnel.admin.organization_id", "tunnel.admin.workspace_id", "tunnel.admin.tenant_id")
}

func tunnelAdminKeyStatusCommand() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show stored tunnel admin key state without revealing the key", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.key.loading", "Loading tunnel admin key state")
		status, err := application.TunnelAdminKeyStatusContext(cmd.Context())
		if err != nil {
			return err
		}
		renderTunnelAdminKeyStatus(commandPresenter(cmd), status)
		return nil
	}}
}

func tunnelAdminKeyVerifyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "verify", Short: "Re-verify the stored admin key has Tunnels Manage access", Args: cobra.NoArgs, RunE: runTunnelAdminVerify}
	return markTunnelAdminVerifySettings(cmd)
}

func tunnelAdminVerifyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "verify", Short: "Verify the configured OpenAI tunnel admin key and scope", Args: cobra.NoArgs, RunE: runTunnelAdminVerify}
	return markTunnelAdminVerifySettings(cmd)
}

func markTunnelAdminVerifySettings(cmd *cobra.Command) *cobra.Command {
	return markScopedSettings(cmd,
		"tunnel.admin.key",
		"tunnel.admin.organization_id",
		"tunnel.admin.workspace_id",
		"tunnel.admin.tenant_id",
		"tunnel.admin.verified",
		"tunnel.admin.read_access",
		"tunnel.admin.manage_access",
	)
}

func runTunnelAdminVerify(cmd *cobra.Command, _ []string) error {
	logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.key.verifying", "Preparing stored tunnel admin key verification")
	ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
	defer cancel()
	if _, err := settingService().Verify(ctx, "tunnel.admin.key"); err != nil {
		return err
	}
	status, err := application.TunnelAdminKeyStatusContext(ctx)
	if err != nil {
		return err
	}
	fields := []presentation.Field{{Label: "access", Value: formatTunnelAdminAccess(status.Access)}}
	if tunnel.ValidateAdminScope(status.Scope) == nil {
		fields = append([]presentation.Field{{Label: "scope", Value: formatTunnelAdminScope(status.Scope)}}, fields...)
	}
	renderMutationSuccess(cmd, "Admin key verified", fields...)
	return nil
}

func tunnelAdminKeyRemoveCommand() *cobra.Command {
	return &cobra.Command{Use: "remove", Short: "Remove the stored tunnel admin key and verification scope", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.key.removing", "Removing stored tunnel admin key")
		if _, err := settingService().Unset(cmd.Context(), "tunnel.admin.key"); err != nil {
			return err
		}
		renderMutationSuccess(cmd, "Admin key removed")
		return nil
	}}
}

func tunnelListCommand() *cobra.Command {
	var scopeFlags tunnelAdminScopeFlags
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Short: "List tunnels manageable by the stored admin key", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.config.loading", "Loading tunnel administration configuration")
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if !tunnel.AdminEnabled(cfg.Tunnel) {
			return application.ErrTunnelAdminDisabled
		}
		if strings.TrimSpace(cfg.Tunnel.Admin.Key) == "" {
			return application.ErrTunnelAdminNotConfigured
		}
		scope, err := resolveTunnelAdminScope(cmd, cfg.Tunnel, scopeFlags)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
		defer cancel()
		session := commandProgressSession(cmd)
		session.Update(presentation.ProgressPhase{ID: "tunnel.admin.list", Label: "Loading managed tunnels", State: presentation.ProgressRunning})
		items, err := tunnel.ListManaged(ctx, cfg.Tunnel, scope)
		if err != nil {
			return err
		}
		session.Success("tunnel.admin.list", "Loading managed tunnels", fmt.Sprintf("Loaded managed tunnels · %d", len(items)))
		if asJSON {
			return writeResultJSON(cmd, items)
		}
		renderManagedTunnelList(commandPresenter(cmd), items)
		return nil
	}}
	scopeFlags.add(cmd)
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func tunnelGetCommand() *cobra.Command {
	var configure, enable bool
	var asJSON bool
	var runtimeAPIKey string
	cmd := &cobra.Command{Use: "get <tunnel_id>", Short: "Fetch a managed tunnel by id", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.get.preparing", "Preparing managed tunnel lookup", logger.WithVerbose("tunnel_id", args[0]))
		ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
		defer cancel()
		result, err := application.GetManagedTunnel(ctx, args[0], application.ManagedTunnelOptions{Configure: configure, RuntimeAPIKey: runtimeAPIKey, Enable: enable})
		if err != nil {
			return err
		}
		metadata := result.Metadata
		if asJSON {
			return writeResultJSON(cmd, metadata)
		}
		if configure {
			renderEntityMutationSuccess(cmd, "Managed tunnel loaded", metadata.ID, append(managedTunnelMutationFields(metadata), presentation.Field{Label: "cm", Value: "configured"})...)
			return nil
		}
		renderManagedTunnel(commandPresenter(cmd), metadata)
		return nil
	}}
	addManagedConfigureFlags(cmd, &configure, &runtimeAPIKey, &enable)
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderTunnelAdminKeyStatus(presenter *presentation.Presenter, status application.TunnelAdminStatus) {
	keyConfigured := status.KeyConfigured || status.Configured
	if keyConfigured {
		presenter.StateSection(presentation.StatusSuccess, "Admin key configured")
	} else {
		presenter.StateSection(presentation.StatusInactive, "Admin key not configured")
	}
	fields := []presentation.Field{
		{Label: "enabled", Value: status.Enabled},
		{Label: "key configured", Value: keyConfigured},
		{Label: "admin configured", Value: status.Configured},
		{Label: "verified", Value: status.Verified},
	}
	if keyConfigured {
		preview := status.KeyPreview
		if strings.TrimSpace(preview) == "" {
			preview = "admin_********legacy"
		}
		fields = append(fields, presentation.Field{Label: "key", Value: preview})
	}
	if tunnel.ValidateAdminScope(status.Scope) == nil {
		fields = append(fields, presentation.Field{Label: "scope", Value: formatTunnelAdminScope(status.Scope)})
	}
	fields = append(fields,
		presentation.Field{Label: "access", Value: formatTunnelAdminAccess(status.Access)},
		presentation.Field{Label: "secret store", Value: "secret file store"},
	)
	presenter.Fields(fields...)
}

func renderManagedTunnelList(presenter *presentation.Presenter, items []tunnel.Metadata) {
	if len(items) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No managed tunnels")
		return
	}
	entities := make([]presentation.Entity, 0, len(items))
	for _, item := range items {
		name := item.Name
		if name == "" {
			name = "unnamed"
		}
		fields := []presentation.Field{{Label: "name", Value: name}}
		if len(item.OrganizationIDs) > 0 {
			fields = append(fields, presentation.Field{Label: "organizations", Value: strings.Join(item.OrganizationIDs, ", ")})
		}
		if len(item.WorkspaceIDs) > 0 {
			fields = append(fields, presentation.Field{Label: "workspaces", Value: strings.Join(item.WorkspaceIDs, ", ")})
		}
		if len(item.TenantIDs) > 0 {
			fields = append(fields, presentation.Field{Label: "tenants", Value: strings.Join(item.TenantIDs, ", ")})
		}
		entities = append(entities, presentation.Entity{Title: item.ID, Fields: fields})
	}
	presenter.RenderBlock(presentation.EntityList{Items: entities})
}

func renderManagedTunnel(presenter *presentation.Presenter, metadata tunnel.Metadata) {
	presenter.Subsection(metadata.ID)
	fields := []presentation.Field{}
	if metadata.Name != "" {
		fields = append(fields, presentation.Field{Label: "name", Value: metadata.Name})
	}
	if metadata.Description != "" {
		fields = append(fields, presentation.Field{Label: "description", Value: metadata.Description})
	}
	if metadata.Creator != "" {
		fields = append(fields, presentation.Field{Label: "creator", Value: metadata.Creator})
	}
	if len(metadata.OrganizationIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "organizations", Value: strings.Join(metadata.OrganizationIDs, ", ")})
	}
	if len(metadata.WorkspaceIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "workspaces", Value: strings.Join(metadata.WorkspaceIDs, ", ")})
	}
	if len(metadata.TenantIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "tenants", Value: strings.Join(metadata.TenantIDs, ", ")})
	}
	if metadata.RequestID != "" {
		fields = append(fields, presentation.Field{Label: "request", Value: metadata.RequestID})
	}
	if !metadata.FetchedAt.IsZero() {
		fields = append(fields, presentation.Field{Label: "fetched", Value: metadata.FetchedAt.Local().Format(time.RFC3339)})
	}
	presenter.NestedFields(fields...)
}

func tunnelUseCommand() *cobra.Command {
	var runtimeAPIKey string
	var autoRuntimeKey bool
	var projectID string
	cmd := &cobra.Command{Use: "use <tunnel_id>", Short: "Select a managed tunnel for the local runtime", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.use.preparing", "Preparing managed tunnel selection", logger.WithVerbose("tunnel_id", args[0]))
		ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
		defer cancel()
		result, err := application.UseManagedTunnel(ctx, args[0], application.ManagedTunnelUseOptions{RuntimeAPIKey: runtimeAPIKey, AutoGenerateRuntimeKey: autoRuntimeKey, ProjectID: projectID})
		if err != nil {
			return err
		}
		renderEntityMutationSuccess(cmd, "Managed tunnel selected", result.Metadata.ID, append(managedTunnelMutationFields(result.Metadata), presentation.Field{Label: "runtime", Value: "configured"}, presentation.Field{Label: "enabled", Value: true})...)
		return nil
	}}
	cmd.Flags().StringVar(&runtimeAPIKey, "runtime-api-key", "", "runtime API key for cm; defaults to the currently configured runtime key")
	cmd.Flags().BoolVar(&autoRuntimeKey, "auto-runtime-key", false, "generate a Read + Use runtime key with the stored OpenAI admin key when no runtime key is configured")
	cmd.Flags().StringVar(&projectID, "project-id", "", "OpenAI project used for automatic runtime key generation; defaults to the only active project or Default project")
	return cmd
}

func tunnelCreateCommand() *cobra.Command {
	var name, description, runtimeAPIKey string
	var organizationIDs, workspaceIDs, tenantIDs []string
	var configure, enable bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a tunnel with the stored verified admin key",
		Long:  "Create tunnel metadata through the OpenAI Tunnel Management API. The stored admin key must already pass tunnel admin key verify. Use --configure to select the new tunnel for cm with a separate runtime API key.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.create.preparing", "Preparing managed tunnel creation")
			request := tunnel.CreateRequest{Name: strings.TrimSpace(name), Description: strings.TrimSpace(description), OrganizationIDs: normalizeTunnelIDs(organizationIDs), WorkspaceIDs: normalizeTunnelIDs(workspaceIDs), TenantIDs: normalizeTunnelIDs(tenantIDs)}
			ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
			defer cancel()
			result, err := application.CreateManagedTunnel(ctx, request, application.ManagedTunnelOptions{Configure: configure, RuntimeAPIKey: runtimeAPIKey, Enable: enable})
			if err != nil {
				return err
			}
			metadata := result.Metadata
			fields := managedTunnelMutationFields(metadata)
			if configure {
				fields = append(fields, presentation.Field{Label: "cm", Value: "configured"})
			}
			fields = append(fields, presentation.Field{Label: "ready", Value: "allow 25-30 seconds before expecting the new tunnel to be active"})
			renderEntityMutationSuccess(cmd, "Tunnel created", metadata.ID, fields...)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tunnel name (required)")
	cmd.Flags().StringVar(&description, "description", "", "tunnel description (required)")
	cmd.Flags().StringSliceVar(&organizationIDs, "organization-id", nil, "OpenAI organization identifier; repeatable")
	cmd.Flags().StringSliceVar(&workspaceIDs, "workspace-id", nil, "OpenAI workspace identifier; repeatable")
	cmd.Flags().StringSliceVar(&tenantIDs, "tenant-id", nil, "OpenAI tenant identifier; repeatable")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("description")
	addManagedConfigureFlags(cmd, &configure, &runtimeAPIKey, &enable)
	return cmd
}

func tunnelUpdateCommand() *cobra.Command {
	var name, description, runtimeAPIKey string
	var organizationIDs, workspaceIDs, tenantIDs []string
	var configure, enable bool
	cmd := &cobra.Command{Use: "update <tunnel_id>", Short: "Update a tunnel with the stored verified admin key", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.update.preparing", "Preparing managed tunnel update", logger.WithVerbose("tunnel_id", args[0]))
		request := tunnel.UpdateRequest{}
		if cmd.Flags().Changed("name") {
			value := strings.TrimSpace(name)
			request.Name = &value
		}
		if cmd.Flags().Changed("description") {
			value := description
			request.Description = &value
		}
		if cmd.Flags().Changed("organization-id") {
			value := normalizeTunnelIDs(organizationIDs)
			request.OrganizationIDs = &value
		}
		if cmd.Flags().Changed("workspace-id") {
			value := normalizeTunnelIDs(workspaceIDs)
			request.WorkspaceIDs = &value
		}
		if cmd.Flags().Changed("tenant-id") {
			value := normalizeTunnelIDs(tenantIDs)
			request.TenantIDs = &value
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
		defer cancel()
		result, err := application.UpdateManagedTunnel(ctx, args[0], request, application.ManagedTunnelOptions{Configure: configure, RuntimeAPIKey: runtimeAPIKey, Enable: enable})
		if err != nil {
			return err
		}
		metadata := result.Metadata
		fields := managedTunnelMutationFields(metadata)
		if configure {
			fields = append(fields, presentation.Field{Label: "cm", Value: "configured"})
		}
		renderEntityMutationSuccess(cmd, "Tunnel updated", metadata.ID, fields...)
		return nil
	}}
	cmd.Flags().StringVar(&name, "name", "", "new tunnel name")
	cmd.Flags().StringVar(&description, "description", "", "new tunnel description")
	cmd.Flags().StringSliceVar(&organizationIDs, "organization-id", nil, "replace organization identifiers; repeatable")
	cmd.Flags().StringSliceVar(&workspaceIDs, "workspace-id", nil, "replace workspace identifiers; repeatable")
	cmd.Flags().StringSliceVar(&tenantIDs, "tenant-id", nil, "replace tenant identifiers; repeatable")
	addManagedConfigureFlags(cmd, &configure, &runtimeAPIKey, &enable)
	return cmd
}

func tunnelDeleteCommand() *cobra.Command {
	var confirm, clearConfig bool
	cmd := &cobra.Command{Use: "delete <tunnel_id>", Short: "Delete a tunnel with the stored verified admin key", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		logCommandVerbose(cmd, "TUNNEL", "tunnel.admin.delete.preparing", "Preparing managed tunnel deletion", logger.WithVerbose("tunnel_id", args[0]))
		if !confirm {
			return &tunnelDeleteConfirmationRequiredError{TunnelID: args[0]}
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), tunnelAdminTimeout)
		defer cancel()
		result, err := application.DeleteManagedTunnel(ctx, args[0], clearConfig)
		if err != nil {
			return err
		}
		metadata, cleared := result.Metadata, result.Cleared
		fields := managedTunnelMutationFields(metadata)
		if cleared {
			fields = append(fields, presentation.Field{Label: "cm", Value: "configuration cleared"})
		}
		renderEntityMutationSuccess(cmd, "Tunnel deleted", metadata.ID, fields...)
		return nil
	}}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "confirm permanent tunnel deletion")
	cmd.Flags().BoolVar(&clearConfig, "clear-config", false, "clear cm runtime tunnel config when deleting the selected tunnel")
	return cmd
}

func addManagedConfigureFlags(cmd *cobra.Command, configure *bool, runtimeAPIKey *string, enable *bool) {
	cmd.Flags().BoolVar(configure, "configure", false, "configure cm to use this tunnel")
	cmd.Flags().StringVar(runtimeAPIKey, "runtime-api-key", "", "runtime API key for cm; defaults to the currently configured runtime key")
	cmd.Flags().BoolVar(enable, "enable", false, "enable the tunnel in cm when used with --configure")
}

func configureManagedTunnel(cfg *config.Config, metadata tunnel.Metadata, runtimeAPIKey string, enable bool) error {
	if cfg == nil {
		return errors.New("configuration is unavailable")
	}
	key := strings.TrimSpace(runtimeAPIKey)
	if key == "" {
		key = strings.TrimSpace(cfg.Tunnel.APIKey)
	}
	if key == "" {
		return errors.New("runtime API key is required to configure cm; use --runtime-api-key or configure one first")
	}
	cfg.Tunnel.ID = metadata.ID
	cfg.Tunnel.APIKey = key
	if len(metadata.OrganizationIDs) == 1 {
		cfg.Tunnel.OrganizationID = metadata.OrganizationIDs[0]
	}
	if enable {
		cfg.Tunnel.Enabled = true
	}
	return config.Validate(*cfg)
}

func managedTunnelMutationFields(metadata tunnel.Metadata) []presentation.Field {
	fields := []presentation.Field{}
	if metadata.Name != "" {
		fields = append(fields, presentation.Field{Label: "name", Value: metadata.Name})
	}
	if metadata.Description != "" {
		fields = append(fields, presentation.Field{Label: "description", Value: metadata.Description})
	}
	if metadata.Creator != "" {
		fields = append(fields, presentation.Field{Label: "creator", Value: metadata.Creator})
	}
	if len(metadata.OrganizationIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "organizations", Value: strings.Join(metadata.OrganizationIDs, ", ")})
	}
	if len(metadata.WorkspaceIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "workspaces", Value: strings.Join(metadata.WorkspaceIDs, ", ")})
	}
	if len(metadata.TenantIDs) > 0 {
		fields = append(fields, presentation.Field{Label: "tenants", Value: strings.Join(metadata.TenantIDs, ", ")})
	}
	if metadata.RequestID != "" {
		fields = append(fields, presentation.Field{Label: "request", Value: metadata.RequestID})
	}
	return fields
}

func formatTunnelAdminAccess(access tunnel.AdminAccess) string {
	if access.Manage {
		return "full management"
	}
	if access.Read {
		return "read only"
	}
	return "not verified"
}

func formatTunnelAdminScope(scope tunnel.AdminScope) string {
	if scope.OrganizationID != "" {
		return "organization:" + scope.OrganizationID
	}
	if scope.WorkspaceID != "" {
		return "workspace:" + scope.WorkspaceID
	}
	if scope.TenantID != "" {
		return "tenant:" + scope.TenantID
	}
	return "none"
}
