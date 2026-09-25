package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/logger"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/upstream"
)

type upstreamFlags struct {
	name                string
	transport           string
	enabled             bool
	command             string
	args                []string
	env                 []string
	cwd                 string
	url                 string
	headers             []string
	bearerTokenEnvVar   string
	authType            string
	authScope           string
	toolPrefix          string
	expose              string
	tools               []string
	disabledTools       []string
	idleTimeout         int
	allowPrivateNetwork bool
}

type upstreamServerNotFoundError struct{ ServerID string }

func (err *upstreamServerNotFoundError) Error() string {
	return "unknown upstream server: " + strings.TrimSpace(err.ServerID)
}

type upstreamServerExistsError struct{ ServerID string }

func (err *upstreamServerExistsError) Error() string {
	return "upstream server already exists: " + strings.TrimSpace(err.ServerID)
}

func upstreamCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "upstream", Short: "Manage Upstream servers"}
	cmd.AddCommand(upstreamServerCommand())
	return cmd
}

func upstreamServerCommand() *cobra.Command {
	server := &cobra.Command{Use: "server", Short: "Manage configured Upstream servers"}
	server.AddCommand(
		upstreamServerListCommand(),
		upstreamServerAddCommand(),
		upstreamServerConfigureCommand(),
		upstreamServerShowCommand(),
		upstreamServerRemoveCommand(),
		upstreamServerToggleCommand(true),
		upstreamServerToggleCommand(false),
		upstreamServerStatusCommand(),
		upstreamServerToolsCommand(),
		upstreamServerAuthCommand(),
	)
	return server
}

func upstreamServerListCommand() *cobra.Command {
	var asJSON, refresh bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured Upstream servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			if refresh {
				var progress *commandProgress
				if !asJSON {
					progress = newCommandProgress(cmd, "UPSTREAM")
					progress.Start("upstream.status.refreshing", "Refreshing Upstream status", "Refreshed Upstream status")
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
				statuses := manager.ListStatuses(ctx, true)
				cancel()
				if asJSON {
					return writeResultJSON(cmd, statuses)
				}
				progress.Complete()
				renderUpstreamStatusList(commandPresenter(cmd), statuses)
				return nil
			}
			servers := manager.List()
			views := make([]upstream.Server, len(servers))
			for index, server := range servers {
				views[index] = redactUpstreamServer(server)
			}
			if asJSON {
				return writeResultJSON(cmd, views)
			}
			renderUpstreamServerList(commandPresenter(cmd), views)
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	cmd.Flags().BoolVar(&refresh, "refresh", false, "connect to each enabled server and refresh health")
	return cmd
}

func upstreamServerAddCommand() *cobra.Command {
	var flags upstreamFlags
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Add an Upstream server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			if _, exists := manager.Get(args[0]); exists {
				return &upstreamServerExistsError{ServerID: args[0]}
			}
			server := upstream.Server{ID: args[0], Name: args[0], Enabled: true, Expose: "all"}
			server, err = applyUpstreamFlags(cmd, server, flags, true)
			if err != nil {
				return err
			}
			if err := manager.Add(server); err != nil {
				return err
			}
			normalized, ok := manager.Get(args[0])
			if !ok {
				return fmt.Errorf("upstream server disappeared after save: %s", args[0])
			}
			renderEntityMutationSuccess(cmd, "Upstream server", "Upstream server added", normalized.ID, presentation.Field{Label: "transport", Value: normalized.Transport}, presentation.Field{Label: "prefix", Value: normalized.ToolPrefix}, presentation.Field{Label: "expose", Value: normalized.Expose})
			return nil
		},
	}
	bindUpstreamFlags(cmd, &flags, true)
	return cmd
}

func upstreamServerConfigureCommand() *cobra.Command {
	var flags upstreamFlags
	cmd := &cobra.Command{
		Use:               "configure <id>",
		Aliases:           []string{"set"},
		Short:             "Update selected fields on an existing Upstream server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := application.LoadUpstreamService(cmd.Context())
			if err != nil {
				return err
			}
			current, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			server, err := applyUpstreamFlags(cmd, current.Value, flags, false)
			if err != nil {
				return err
			}
			result, err := service.Update(cmd.Context(), args[0], server)
			if err != nil {
				return err
			}
			renderEntityMutationSuccess(cmd, "Upstream server", "Upstream server updated", result.Value.ID)
			return nil
		},
	}
	bindUpstreamFlags(cmd, &flags, false)
	return markScopedSettings(cmd,
		"upstream.servers[<id>].enabled",
		"upstream.servers[<id>].name",
		"upstream.servers[<id>].transport",
		"upstream.servers[<id>].command",
		"upstream.servers[<id>].args",
		"upstream.servers[<id>].cwd",
		"upstream.servers[<id>].url",
		"upstream.servers[<id>].bearer_token_env_var",
		"upstream.servers[<id>].auth.type",
		"upstream.servers[<id>].auth.scope",
		"upstream.servers[<id>].tool_prefix",
		"upstream.servers[<id>].expose",
		"upstream.servers[<id>].tools",
		"upstream.servers[<id>].disabled_tools",
		"upstream.servers[<id>].idle_timeout_sec",
		"upstream.servers[<id>].allow_private_network",
	)
}

func upstreamServerShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "show <id>",
		Short:             "Show one upstream server with secrets redacted",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			server, ok := manager.Get(args[0])
			if !ok {
				return &upstreamServerNotFoundError{ServerID: args[0]}
			}
			server = redactUpstreamServer(server)
			if asJSON {
				return writeResultJSON(cmd, server)
			}
			renderUpstreamServer(commandPresenter(cmd), server)
			return nil
		},
	}
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func upstreamServerRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "remove <id>",
		Short:             "Remove an Upstream server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			if _, ok := manager.Get(args[0]); !ok {
				return &upstreamServerNotFoundError{ServerID: args[0]}
			}
			if err := manager.Remove(args[0]); err != nil {
				return err
			}
			renderEntityMutationSuccess(cmd, "Upstream server", "Upstream server removed", args[0])
			return nil
		},
	}
}

func upstreamServerToggleCommand(enabled bool) *cobra.Command {
	action := "disable"
	if enabled {
		action = "enable"
	}
	cmd := &cobra.Command{
		Use:               action + " <id>",
		Short:             action + " an Upstream server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := application.LoadUpstreamService(cmd.Context())
			if err != nil {
				return err
			}
			if enabled {
				_, err = service.Enable(cmd.Context(), args[0])
			} else {
				_, err = service.Disable(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			renderEntityMutationSuccess(cmd, "Upstream server", strings.ToUpper(action[:1])+action[1:]+"d", args[0])
			return nil
		},
	}
	return markScopedSettings(cmd, "upstream.servers[<id>].enabled")
}

func upstreamServerStatusCommand() *cobra.Command {
	var refresh, asJSON bool
	cmd := &cobra.Command{
		Use:               "status <id>",
		Aliases:           []string{"st"},
		Short:             "Check one Upstream server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			var progress *commandProgress
			if !asJSON {
				progress = newCommandProgress(cmd, "UPSTREAM")
				progress.Start("upstream.status.checking", "Checking Upstream status", "Checked Upstream status")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			status := manager.CheckHealth(ctx, args[0], refresh)
			if asJSON {
				return writeResultJSON(cmd, status)
			}
			progress.Complete()
			renderUpstreamStatus(commandPresenter(cmd), status)
			return nil
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", true, "force a new upstream connection/tool list")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func renderUpstreamServer(presenter *presentation.Presenter, server upstream.Server) {
	endpoint := server.URL
	if server.Transport == "stdio" {
		endpoint = server.Command
	}
	presenter.Frame("Upstream server")
	presenter.Subsection(server.ID)
	fields := []presentation.Field{
		{Label: "name", Value: server.Name},
		{Label: "transport", Value: server.Transport},
		{Label: "enabled", Value: server.Enabled},
		{Label: "endpoint", Value: endpoint},
		{Label: "auth", Value: server.Auth.Type},
	}
	if server.Auth.Scope != "" {
		fields = append(fields, presentation.Field{Label: "auth scope", Value: server.Auth.Scope})
	}
	fields = append(fields,
		presentation.Field{Label: "expose", Value: server.Expose},
		presentation.Field{Label: "tool prefix", Value: server.ToolPrefix},
		presentation.Field{Label: "idle timeout", Value: fmt.Sprintf("%ds", server.IdleTimeoutSec)},
	)
	if server.AllowPrivateNetwork {
		fields = append(fields, presentation.Field{Label: "allow private network", Value: true})
	}
	if server.CWD != "" {
		fields = append(fields, presentation.Field{Label: "cwd", Value: server.CWD})
	}
	if len(server.Args) > 0 {
		fields = append(fields, presentation.Field{Label: "args", Value: strings.Join(server.Args, " ")})
	}
	if server.BearerTokenEnvVar != "" {
		fields = append(fields, presentation.Field{Label: "bearer env", Value: server.BearerTokenEnvVar})
	}
	if len(server.Headers) > 0 {
		fields = append(fields, presentation.Field{Label: "headers", Value: strings.Join(sortedAssignments(server.Headers), ", ")})
	}
	if len(server.Env) > 0 {
		fields = append(fields, presentation.Field{Label: "env", Value: strings.Join(sortedAssignments(server.Env), ", ")})
	}
	if len(server.Tools) > 0 {
		fields = append(fields, presentation.Field{Label: "tools", Value: strings.Join(server.Tools, ", ")})
	}
	if len(server.DisabledTools) > 0 {
		fields = append(fields, presentation.Field{Label: "disabled tools", Value: strings.Join(server.DisabledTools, ", ")})
	}
	presenter.NestedFields(fields...)
	presenter.Complete("Done")
}

func renderUpstreamStatus(presenter *presentation.Presenter, status upstream.Status) {
	presenter.Frame("Upstream server status")
	presenter.StateSection(upstreamHealthPresentationKind(status.Health), upstreamHealthLabel(status.Health))
	presenter.Subsection(status.ID)
	fields := []presentation.Field{
		{Label: "name", Value: status.Name},
		{Label: "health", Value: status.Health},
		{Label: "enabled", Value: status.Enabled},
		{Label: "connected", Value: status.Connected},
		{Label: "transport", Value: status.Transport},
		{Label: "auth", Value: status.Auth},
		{Label: "tools", Value: status.ToolCount},
		{Label: "expose", Value: status.Expose},
	}
	if len(status.ProxiedTools) > 0 {
		fields = append(fields, presentation.Field{Label: "proxied tools", Value: strings.Join(status.ProxiedTools, ", ")})
	}
	if status.PID != nil {
		fields = append(fields, presentation.Field{Label: "pid", Value: *status.PID})
	}
	presenter.NestedFields(fields...)
	if status.LastError != "" {
		presenter.ChildStatus(presentation.StatusWarning, status.LastError)
	}
	presenter.Complete("Status complete")
}

func sortedAssignments(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func upstreamServerToolsCommand() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:               "tools <id>",
		Short:             "List tools exposed by one Upstream server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			progress := newCommandProgress(cmd, "UPSTREAM")
			progress.Start("upstream.tools.loading", "Loading Upstream tools", "Loaded Upstream tools")
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			values, err := manager.Tools(ctx, args[0], refresh)
			if err != nil {
				progress.Stop()
				return err
			}
			server, ok := manager.Get(args[0])
			if !ok {
				progress.Stop()
				return fmt.Errorf("upstream server disappeared while loading tools: %s", args[0])
			}
			progress.Complete()
			proxied := map[string]bool{}
			for _, name := range manager.ProxiedToolNames(server, values) {
				proxied[name] = true
			}
			presenter := commandPresenter(cmd)
			presenter.Frame("Upstream tools")
			if len(values) == 0 {
				presenter.StateSection(presentation.StatusInactive, "No tools exposed by upstream server")
				presenter.Fields(presentation.Field{Label: "server", Value: args[0]})
				presenter.Complete("Done")
				return nil
			}
			presenter.Section(fmt.Sprintf("Upstream tools · %d", len(values)))
			for _, tool := range values {
				proxy := upstream.ProxyName(server.ToolPrefix, tool.Name)
				state := "hidden"
				if proxied[proxy] {
					state = proxy
				}
				presenter.Subsection(tool.Name)
				presenter.NestedFields(presentation.Field{Label: "exposed as", Value: state})
			}
			presenter.Complete("Done")
			return nil
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "force a new upstream connection/tool list")
	return cmd
}

func renderUpstreamServerList(presenter *presentation.Presenter, servers []upstream.Server) {
	presenter.Frame("Upstream servers")
	if len(servers) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No Upstream servers configured")
		presenter.Complete("Done")
		return
	}
	presenter.Section(fmt.Sprintf("Upstream servers · %d", len(servers)))
	for _, server := range servers {
		endpoint := server.URL
		if server.Transport == "stdio" {
			endpoint = server.Command
		}
		presenter.Subsection(server.ID)
		presenter.NestedFields(
			presentation.Field{Label: "transport", Value: server.Transport},
			presentation.Field{Label: "enabled", Value: server.Enabled},
			presentation.Field{Label: "expose", Value: server.Expose},
			presentation.Field{Label: "endpoint", Value: endpoint},
		)
	}
	presenter.Complete("Done")
}

func renderUpstreamStatusList(presenter *presentation.Presenter, statuses []upstream.Status) {
	presenter.Frame("Upstream status")
	if len(statuses) == 0 {
		presenter.StateSection(presentation.StatusInactive, "No Upstream servers configured")
		presenter.Complete("Done")
		return
	}
	presenter.Section(fmt.Sprintf("Upstream status · %d", len(statuses)))
	for _, status := range statuses {
		presenter.Subsection(status.ID)
		presenter.NestedFields(
			presentation.Field{Label: "transport", Value: status.Transport},
			presentation.Field{Label: "enabled", Value: status.Enabled},
			presentation.Field{Label: "health", Value: status.Health},
			presentation.Field{Label: "tools", Value: status.ToolCount},
			presentation.Field{Label: "expose", Value: status.Expose},
		)
	}
	presenter.Complete("Done")
}

func upstreamHealthPresentationKind(health upstream.Health) presentation.StatusKind {
	switch health {
	case upstream.HealthConnected:
		return presentation.StatusSuccess
	case upstream.HealthUnreachable:
		return presentation.StatusWarning
	case upstream.HealthDisabled:
		return presentation.StatusInactive
	default:
		return presentation.StatusInfo
	}
}

func upstreamHealthLabel(health upstream.Health) string {
	switch health {
	case upstream.HealthConnected:
		return "Upstream server connected"
	case upstream.HealthUnreachable:
		return "Upstream server unreachable"
	case upstream.HealthDisabled:
		return "Upstream server disabled"
	default:
		return "Upstream server status"
	}
}

func bindUpstreamFlags(cmd *cobra.Command, flags *upstreamFlags, create bool) {
	cmd.Flags().StringVar(&flags.name, "name", "", "display name")
	cmd.Flags().StringVar(&flags.transport, "transport", "", "transport: http or stdio")
	cmd.Flags().BoolVar(&flags.enabled, "enabled", true, "enable server")
	cmd.Flags().StringVar(&flags.command, "command", "", "stdio command")
	cmd.Flags().StringSliceVar(&flags.args, "arg", nil, "stdio command argument")
	cmd.Flags().StringSliceVar(&flags.env, "env", nil, "stdio environment KEY=VALUE")
	cmd.Flags().StringVar(&flags.cwd, "cwd", "", "stdio working directory")
	cmd.Flags().StringVar(&flags.url, "url", "", "HTTP MCP URL")
	cmd.Flags().StringSliceVar(&flags.headers, "header", nil, "HTTP header KEY=VALUE")
	cmd.Flags().StringVar(&flags.bearerTokenEnvVar, "bearer-token-env", "", "environment variable containing HTTP bearer token")
	cmd.Flags().StringVar(&flags.authType, "auth", "", "auth mode: auto, oauth, none")
	cmd.Flags().StringVar(&flags.authScope, "auth-scope", "", "OAuth scope")
	cmd.Flags().StringVar(&flags.toolPrefix, "tool-prefix", "", "dynamic proxy tool prefix")
	cmd.Flags().StringVar(&flags.expose, "expose", "", "none, meta_only, allowlist, or all")
	cmd.Flags().StringSliceVar(&flags.tools, "tool", nil, "allowlisted upstream tool")
	cmd.Flags().StringSliceVar(&flags.disabledTools, "disable-tool", nil, "hidden upstream tool")
	cmd.Flags().IntVar(&flags.idleTimeout, "idle-timeout", 0, "idle timeout in seconds")
	cmd.Flags().BoolVar(&flags.allowPrivateNetwork, "allow-private-network", false, "allow loopback/private upstream URLs for this server")
	if create {
		_ = cmd.MarkFlagRequired("transport")
	}
}

func applyUpstreamFlags(cmd *cobra.Command, server upstream.Server, flags upstreamFlags, create bool) (upstream.Server, error) {
	changed := func(name string) bool { return create || cmd.Flags().Changed(name) }
	if changed("name") && strings.TrimSpace(flags.name) != "" {
		server.Name = flags.name
	}
	if cmd.Flags().Changed("transport") || create {
		server.Transport = flags.transport
	}
	if cmd.Flags().Changed("enabled") || create {
		server.Enabled = flags.enabled
	}
	if changed("command") {
		server.Command = flags.command
	}
	if cmd.Flags().Changed("arg") {
		server.Args = append([]string(nil), flags.args...)
	}
	if cmd.Flags().Changed("env") {
		env, err := upstream.ParseAssignments(flags.env, "env")
		if err != nil {
			return upstream.Server{}, err
		}
		server.Env = env
	}
	if changed("cwd") {
		server.CWD = flags.cwd
	}
	if changed("url") {
		server.URL = flags.url
	}
	if cmd.Flags().Changed("header") {
		headers, err := upstream.ParseAssignments(flags.headers, "header")
		if err != nil {
			return upstream.Server{}, err
		}
		server.Headers = headers
	}
	if changed("bearer-token-env") {
		server.BearerTokenEnvVar = flags.bearerTokenEnvVar
	}
	if changed("auth") {
		server.Auth.Type = flags.authType
	}
	if changed("auth-scope") {
		server.Auth.Scope = flags.authScope
	}
	if changed("tool-prefix") {
		server.ToolPrefix = flags.toolPrefix
	}
	if changed("expose") && strings.TrimSpace(flags.expose) != "" {
		server.Expose = flags.expose
	}
	if cmd.Flags().Changed("tool") {
		server.Tools = append([]string(nil), flags.tools...)
	}
	if cmd.Flags().Changed("disable-tool") {
		server.DisabledTools = append([]string(nil), flags.disabledTools...)
	}
	if cmd.Flags().Changed("idle-timeout") {
		server.IdleTimeoutSec = flags.idleTimeout
	}
	if cmd.Flags().Changed("allow-private-network") {
		server.AllowPrivateNetwork = flags.allowPrivateNetwork
	}
	return upstream.NormalizeServer(server)
}

func redactUpstreamServer(server upstream.Server) upstream.Server {
	return upstream.RedactServer(server)
}

func parseAssignments(values []string, label string) (map[string]string, error) {
	return upstream.ParseAssignments(values, label)
}

func loadUpstreamManager() (*upstream.Manager, error) {
	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		return nil, err
	}
	return manager, nil
}

type upstreamCommandService struct {
	ctx     context.Context
	service *application.UpstreamService
}

func (adapter *upstreamCommandService) List() []upstream.Server {
	result, err := adapter.service.List(adapter.ctx)
	if err != nil {
		return nil
	}
	return result.Value
}

func (adapter *upstreamCommandService) Get(id string) (upstream.Server, bool) {
	result, err := adapter.service.Get(adapter.ctx, id)
	return result.Value, err == nil
}

func (adapter *upstreamCommandService) Add(server upstream.Server) error {
	if _, exists := adapter.service.Manager().Get(server.ID); exists {
		_, err := adapter.service.Update(adapter.ctx, server.ID, server)
		return err
	}
	_, err := adapter.service.Create(adapter.ctx, server)
	return err
}

func (adapter *upstreamCommandService) Remove(id string) error {
	_, err := adapter.service.Remove(adapter.ctx, id)
	return err
}

func (adapter *upstreamCommandService) ListStatuses(ctx context.Context, refresh bool) []upstream.Status {
	result, err := adapter.service.Statuses(ctx, refresh)
	if err != nil {
		return nil
	}
	return result.Value
}

func (adapter *upstreamCommandService) CheckHealth(ctx context.Context, id string, refresh bool) upstream.Status {
	result, err := adapter.service.Status(ctx, id, refresh)
	if err != nil {
		return upstream.Status{ID: id, Health: upstream.HealthUnreachable, LastError: err.Error()}
	}
	return result.Value
}

func (adapter *upstreamCommandService) Tools(ctx context.Context, id string, refresh bool) ([]upstream.Tool, error) {
	result, err := adapter.service.Tools(ctx, id, refresh)
	return result.Value.Tools, err
}

func (adapter *upstreamCommandService) ProxiedToolNames(server upstream.Server, values []upstream.Tool) []string {
	return adapter.service.Manager().ProxiedToolNames(server, values)
}

func (adapter *upstreamCommandService) Disconnect(id string) error {
	return adapter.service.Disconnect(id)
}

func loadUpstreamManagerForCommand(cmd *cobra.Command) (*upstreamCommandService, error) {
	logCommandStep(cmd, "UPSTREAM", "upstream.store.loading", "Loading Upstream configuration")
	logCommandDebug(cmd, "UPSTREAM", "upstream.store.path", "Upstream configuration path resolved", logger.WithDebug("path", upstream.Path()))
	manager := upstream.NewManager(upstream.NewStore(upstream.Path())).SetTraceObserver(tracepkg.ObserverFromContext(cmd.Context()))
	if err := manager.Load(); err != nil {
		return nil, fmt.Errorf("load Upstream configuration: %w", err)
	}
	logCommandDebug(cmd, "UPSTREAM", "upstream.store.loaded", "Upstream configuration loaded", logger.WithDebug("count", len(manager.List())))
	return &upstreamCommandService{ctx: cmd.Context(), service: application.NewUpstreamService(manager)}, nil
}
