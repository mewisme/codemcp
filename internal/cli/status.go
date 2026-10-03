package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/logger"
	mcpnetwork "go.mewis.me/codemcp/internal/network"
	managed "go.mewis.me/codemcp/internal/service"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
	updatepkg "go.mewis.me/codemcp/internal/update"
)

type statusSnapshot struct {
	Source        configformat.Source
	Config        config.Config
	Runtime       runtimeStatusResult
	Running       bool
	Workspaces    int
	Upstreams     int
	Services      []installedManagedService
	ListenerPlan  listenerPlan
	ListenerError error
	Tunnel        tunnel.Status
	Update        *updatepkg.CachedCheck
	Browser       application.BrowserIntegrationStatus
	ChatGPTWeb    application.ChatGPTWebStatus
}

const statusTunnelWatchTimeout = 35 * time.Second

func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show runtime health and local configuration",
		Args:  cobra.NoArgs,
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, _ []string) (runErr error) {
	ctx := cmd.Context()
	snapshotSpan := tracepkg.Start(ctx, "STATUS", "status.snapshot", "Acquiring status snapshot")
	snapshotComplete := false
	defer func() {
		if snapshotComplete {
			return
		}
		if runErr != nil {
			snapshotSpan.FailMessage("Status snapshot acquisition failed", runErr)
		} else {
			snapshotSpan.EndMessage("Status snapshot acquisition completed")
		}
	}()
	logCommandStep(cmd, "STATUS", "status.scope.resolving", "Resolving service scope")
	serviceSpan := tracepkg.Start(ctx, "STATUS", "status.service-context", "Resolving status service context")
	scope := managed.DetectScope()
	account, err := managed.InvokingAccountContext(ctx, scope)
	if err != nil {
		serviceSpan.FailMessage("Status service account resolution failed", err, tracepkg.String("scope", string(scope)))
		return err
	}
	if err := resolveManagedConfigRoot(cmd, scope, account); err != nil {
		serviceSpan.FailMessage("Status configuration root resolution failed", err, tracepkg.String("scope", string(scope)), tracepkg.String("account", account.Username))
		return err
	}
	serviceSpan.EndMessage("Status service context resolved", tracepkg.String("scope", string(scope)), tracepkg.String("account", account.Username), tracepkg.String("config_root", config.RootPath()))
	configSpan := tracepkg.Start(ctx, "STATUS", "status.config.load", "Loading status configuration", tracepkg.String("config_root", config.RootPath()))
	source, err := config.Source()
	if err != nil {
		configSpan.FailMessage("Status config source discovery failed", err)
		return err
	}
	format, err := commandLogFormat(cmd)
	if err != nil {
		configSpan.FailMessage("Status output format resolution failed", err, tracepkg.String("path", source.Path))
		return err
	}
	verbose, debug := commandLogMode(cmd)
	if !source.Exists {
		configSpan.EndMessage("Status configuration is not initialized", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", false))
		snapshotSpan.EndMessage("Status snapshot acquired", tracepkg.Bool("initialized", false), tracepkg.Bool("running", false))
		snapshotComplete = true
		if debug || format == logger.FormatJSON {
			log := commandLogger(cmd)
			log.Warning("STATUS", "status.not-initialized", "CodeMCP is not initialized", nil)
			log.Detail("config", source.Path)
			return nil
		}
		renderStatusUninitialized(commandPresenter(cmd))
		return nil
	}
	logCommandStep(cmd, "STATUS", "status.config.loading", "Loading runtime configuration")
	cfg, err := config.Load()
	if err != nil {
		configSpan.FailMessage("Status configuration load failed", err, tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)))
		return err
	}
	configSpan.EndMessage("Status configuration loaded", tracepkg.String("path", source.Path), tracepkg.String("format", string(source.Format)), tracepkg.Bool("exists", true))
	workspaceSpan := tracepkg.Start(ctx, "STATUS", "status.workspaces.query", "Querying workspace count")
	workspaces, err := workspaceManagerForCommand(cmd).List()
	if err != nil {
		workspaceSpan.FailMessage("Workspace count query failed", err)
		return err
	}
	workspaceSpan.EndMessage("Workspace count queried", tracepkg.Int("count", len(workspaces)))
	upstreamSpan := tracepkg.Start(ctx, "STATUS", "status.upstreams.query", "Querying Upstream count")
	upstreams, err := loadUpstreamManagerForCommand(cmd)
	if err != nil {
		upstreamSpan.FailMessage("Upstream count query failed", err)
		return err
	}
	upstreamCount := len(upstreams.List())
	upstreamSpan.EndMessage("Upstream count queried", tracepkg.Int("count", upstreamCount))
	logCommandStep(cmd, "STATUS", "status.runtime.inspecting", "Inspecting runtime control endpoint")
	runtimeSpan := tracepkg.Start(ctx, "STATUS", "status.runtime-control.query", "Querying runtime control status")
	runtimeCtx, cancel := context.WithTimeout(ctx, time.Second)
	runtimeStatus, running, runtimeErr := managedRuntimeStatus(runtimeCtx)
	cancel()
	if runtimeErr != nil {
		runtimeSpan.FailMessage("Runtime control status query failed", runtimeErr)
		return runtimeErr
	}
	runtimeSpan.EndMessage("Runtime control status queried", tracepkg.Bool("running", running), tracepkg.Int("pid", runtimeStatus.PID), tracepkg.String("lifecycle", runtimeStatus.Lifecycle))
	listenerSpan := tracepkg.Start(ctx, "STATUS", "status.listener-plan.resolve", "Resolving status listener plan", tracepkg.String("exposure_mode", string(cfg.HTTP.Exposure.Mode)), tracepkg.Any("interfaces", append([]string(nil), cfg.HTTP.Exposure.Interfaces...)))
	plan, listenerErr := resolveListenerPlan(cfg.HTTP.Exposure)
	if listenerErr != nil {
		listenerSpan.FailMessage("Status listener plan resolution failed", listenerErr)
	} else {
		listenerSpan.EndMessage("Status listener plan resolved", tracepkg.Int("host_count", len(plan.Hosts)), tracepkg.Int("address_count", len(plan.Addresses)))
	}
	tunnelStatus := fetchTunnelStatus(ctx, cfg.Tunnel)
	if tunnelStatus.MetadataError != "" {
		logCommandDebug(cmd, "STATUS", "status.tunnel.metadata-unavailable", "Cached tunnel metadata unavailable", logger.WithDebug("error", tunnelStatus.MetadataError))
	}
	updateSpan := tracepkg.Start(ctx, "STATUS", "status.update-cache.lookup", "Looking up cached update status")
	cachedUpdate := cachedUpdateStatus(time.Now())
	updateSpan.EndMessage("Cached update status lookup completed", tracepkg.Bool("cached", cachedUpdate != nil))
	browserService := application.NewBrowserIntegrationService()
	browserService.LoadConfig = func() (config.Config, error) { return cfg, nil }
	browserStatus, _ := browserService.Status(ctx)
	chatGPTWebService := application.NewChatGPTWebService()
	chatGPTWebService.LoadConfig = func() (config.Config, error) { return cfg, nil }
	chatGPTWebStatus, _ := chatGPTWebService.Status(ctx)
	snapshot := statusSnapshot{Source: source, Config: cfg, Runtime: runtimeStatus, Running: running, Workspaces: len(workspaces), Upstreams: upstreamCount, ListenerPlan: plan, ListenerError: listenerErr, Tunnel: tunnelStatus, Update: cachedUpdate, Browser: browserStatus, ChatGPTWeb: chatGPTWebStatus}
	if !running {
		serviceInspectSpan := tracepkg.Start(ctx, "STATUS", "status.managed-services.inspect", "Inspecting installed managed services")
		snapshot.Services = installedManagedServices(ctx, account)
		serviceInspectSpan.EndMessage("Installed managed services inspected", tracepkg.Int("count", len(snapshot.Services)))
	}
	snapshotSpan.EndMessage("Status snapshot acquired", tracepkg.Bool("initialized", true), tracepkg.Bool("running", running), tracepkg.Int("workspaces", snapshot.Workspaces), tracepkg.Int("upstreams", snapshot.Upstreams), tracepkg.Int("managed_services", len(snapshot.Services)), tracepkg.Bool("listener_plan_available", listenerErr == nil), tracepkg.Bool("tunnel_metadata_available", tunnelStatus.MetadataError == ""), tracepkg.Bool("update_cached", cachedUpdate != nil))
	snapshotComplete = true
	if debug || format == logger.FormatJSON {
		renderLegacyStatus(cmd, snapshot)
		return nil
	}
	presenter := commandPresenter(cmd)
	if snapshot.Running && transientTunnelState(statusTunnelState(snapshot.Runtime, true)) && commandAnimationEligible(cmd) {
		presenter.Frame("CodeMCP status")
		renderStatusBase(presenter, snapshot, verbose)
		presenter.Spacer()
		snapshot.Runtime = animateRuntimeTunnelState(cmd, snapshot.Runtime, statusTunnelWatchTimeout)
		snapshot.Tunnel.Running = snapshot.Runtime.TunnelRunning
		snapshot.Tunnel.Ready = snapshot.Runtime.TunnelReady
		snapshot.Tunnel.Restarting = snapshot.Runtime.TunnelRestarting
		snapshot.Tunnel.LastError = snapshot.Runtime.TunnelLastError
		renderStatusTunnelSection(presenter, statusTunnelState(snapshot.Runtime, true))
		renderStatusTunnelBody(presenter, snapshot, verbose)
		presenter.Complete("Status complete")
		return nil
	}
	renderStatus(presenter, snapshot, verbose)
	return nil
}

func renderStatus(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	presenter.Frame("CodeMCP status")
	renderStatusBase(presenter, snapshot, verbose)
	renderStatusTunnel(presenter, snapshot, verbose)
	presenter.Complete("Status complete")
}

func renderStatusBase(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	if snapshot.Running {
		if snapshot.Runtime.Starting {
			presenter.Status(presentation.StatusWarning, "CodeMCP is starting")
		} else {
			presenter.Status(presentation.StatusSuccess, "CodeMCP is running")
		}
		renderRunningStatus(presenter, snapshot, verbose)
		return
	}
	presenter.Status(presentation.StatusError, "CodeMCP is stopped")
	renderStoppedStatus(presenter, snapshot, verbose)
}

func renderRunningStatus(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	status := snapshot.Runtime
	presenter.Spacer()
	presenter.Section("Runtime")
	fields := []presentation.Field{{Label: "pid", Value: status.PID}}
	if status.RunID != "" {
		fields = append(fields, presentation.Field{Label: "session", Value: shortSessionID(status.RunID)})
	}
	if verbose && !status.StartedAt.IsZero() {
		fields = append(fields, presentation.Field{Label: "started", Value: status.StartedAt.Local().Format(time.RFC3339)})
	}
	if !status.StartedAt.IsZero() {
		fields = append(fields, presentation.Field{Label: "uptime", Value: formatStatusUptime(status.StartedAt)})
	}
	if verbose {
		fields = append(fields, presentation.Field{Label: "managed", Value: status.Managed})
		if status.Managed {
			fields = append(fields,
				presentation.Field{Label: "scope", Value: status.ServiceScope},
				presentation.Field{Label: "backend", Value: runtimeBackendLabel(status.ServiceScope)},
				presentation.Field{Label: "service", Value: status.ServiceID},
			)
		}
	} else if status.Managed {
		fields = append(fields,
			presentation.Field{Label: "managed", Value: strings.TrimSpace(status.ServiceScope + " " + presenter.Separator() + " " + runtimeBackendLabel(status.ServiceScope))},
			presentation.Field{Label: "service", Value: status.ServiceID},
		)
	} else {
		fields = append(fields, presentation.Field{Label: "mode", Value: "foreground"})
	}
	presenter.Fields(fields...)
	renderStatusEndpoints(presenter, snapshot, verbose)
	renderStatusConfig(presenter, snapshot, verbose)
	renderStatusOptionalIntegrations(presenter, snapshot, verbose)
}

func renderStoppedStatus(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	presenter.Spacer()
	presenter.Section("Runtime")
	presenter.Fields(presentation.Field{Label: "status", Value: "stopped"})
	renderStatusEndpoints(presenter, snapshot, verbose)
	renderStatusConfig(presenter, snapshot, verbose)
	renderStatusOptionalIntegrations(presenter, snapshot, verbose)
	if len(snapshot.Services) == 0 {
		return
	}
	presenter.Spacer()
	presenter.Section("Service")
	fields := make([]presentation.Field, 0, len(snapshot.Services))
	for _, item := range snapshot.Services {
		fields = append(fields, presentation.Field{Label: string(item.spec.Scope), Value: fmt.Sprintf("installed %s %s", presenter.Separator(), managedBackendLabel(item.manager, item.spec))})
	}
	presenter.Fields(fields...)
}

func renderStatusOptionalIntegrations(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	presenter.Spacer()
	presenter.Section("Optional integrations")
	browserValue := string(snapshot.Browser.State)
	if snapshot.Browser.Running {
		browserValue = "running"
	}
	if browserValue == "" {
		browserValue = "unavailable"
	}
	chatGPTValue := string(snapshot.ChatGPTWeb.State)
	if chatGPTValue == "" {
		chatGPTValue = "unavailable"
	}
	if snapshot.ChatGPTWeb.RuntimePending {
		chatGPTValue += " " + presenter.Separator() + " runtime change pending"
	}
	presenter.Fields(
		presentation.Field{Label: "browser", Value: browserValue},
		presentation.Field{Label: "chatgpt web", Value: chatGPTValue},
	)
	if verbose {
		fields := []presentation.Field{
			{Label: "browser family", Value: snapshot.Browser.Family},
			{Label: "browser transport", Value: snapshot.Browser.Transport},
			{Label: "connector", Value: snapshot.ChatGPTWeb.ConnectorName},
			{Label: "agent capacity", Value: snapshot.ChatGPTWeb.MaxAgents},
		}
		presenter.NestedFields(fields...)
	}
}

func renderStatusEndpoints(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	cfg := snapshot.Config
	presenter.Spacer()
	presenter.Section("Endpoints")
	if !verbose {
		fields := make([]presentation.Field, 0, 3)
		if cfg.HTTP.MCP.Enabled {
			fields = append(fields, presentation.Field{Label: "mcp http", Value: endpointURL(mcpnetwork.LoopbackHost, cfg.HTTP.MCP.Port, "/mcp")})
		} else {
			fields = append(fields, presentation.Field{Label: "mcp http", Value: "disabled"})
		}
		if cfg.HTTP.Admin.Enabled {
			fields = append(fields, presentation.Field{Label: "admin", Value: endpointURL(mcpnetwork.LoopbackHost, cfg.HTTP.Admin.Port, "/")})
		} else {
			fields = append(fields, presentation.Field{Label: "admin", Value: "disabled"})
		}
		fields = append(fields, presentation.Field{Label: "exposure", Value: statusExposureSummary(snapshot, presenter.Separator())})
		presenter.Fields(fields...)
		return
	}
	fields := []presentation.Field{{Label: "expose", Value: cfg.HTTP.Exposure.Mode}}
	if len(cfg.HTTP.Exposure.Interfaces) > 0 {
		fields = append(fields, presentation.Field{Label: "interfaces", Value: strings.Join(cfg.HTTP.Exposure.Interfaces, ", ")})
	}
	if snapshot.ListenerError != nil {
		fields = append(fields, presentation.Field{Label: "network", Value: snapshot.ListenerError.Error()})
		presenter.Fields(fields...)
		return
	}
	presenter.Fields(fields...)
	addresses := append([]mcpnetwork.Address(nil), snapshot.ListenerPlan.Addresses...)
	sort.SliceStable(addresses, func(i, j int) bool {
		left, right := statusAddressPriority(addresses[i]), statusAddressPriority(addresses[j])
		if left != right {
			return left < right
		}
		if addresses[i].Interface != addresses[j].Interface {
			return addresses[i].Interface < addresses[j].Interface
		}
		return addresses[i].Host < addresses[j].Host
	})
	for _, address := range addresses {
		name := address.Interface
		if name == "" {
			name = address.Scope
		}
		presenter.Spacer()
		presenter.Subsection(name)
		addressFields := []presentation.Field{}
		if cfg.HTTP.MCP.Enabled {
			addressFields = append(addressFields, presentation.Field{Label: "mcp http", Value: endpointURL(address.Host, cfg.HTTP.MCP.Port, "/mcp")})
		}
		if cfg.HTTP.Admin.Enabled {
			addressFields = append(addressFields, presentation.Field{Label: "admin", Value: endpointURL(address.Host, cfg.HTTP.Admin.Port, "/")})
		}
		presenter.NestedFields(addressFields...)
	}
	trailing := []presentation.Field{}
	if !cfg.HTTP.MCP.Enabled {
		trailing = append(trailing, presentation.Field{Label: "mcp http", Value: "disabled"})
	}
	if !cfg.HTTP.Admin.Enabled && len(addresses) == 0 {
		trailing = append(trailing, presentation.Field{Label: "admin", Value: "disabled"})
	}
	presenter.Fields(trailing...)
}

func renderStatusTunnel(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	presenter.Spacer()
	status := snapshot.Runtime
	if !snapshot.Running {
		status = runtimeStatusResult{TunnelEnabled: snapshot.Config.Tunnel.Enabled, TunnelConfigured: tunnel.Configured(snapshot.Config.Tunnel), TunnelID: snapshot.Config.Tunnel.ID}
	}
	renderStatusTunnelSection(presenter, statusTunnelState(status, snapshot.Running))
	renderStatusTunnelBody(presenter, snapshot, verbose)
}

func renderStatusTunnelSection(presenter *presentation.Presenter, state string) {
	kind := statusPresentationKind(state)
	if kind == presentation.StatusSuccess || kind == presentation.StatusInfo {
		presenter.Section("Tunnel")
		return
	}
	presenter.StateSection(kind, "Tunnel")
}

func renderStatusTunnelBody(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	status := snapshot.Runtime
	if !snapshot.Running {
		status = runtimeStatusResult{TunnelEnabled: snapshot.Config.Tunnel.Enabled, TunnelConfigured: tunnel.Configured(snapshot.Config.Tunnel), TunnelID: snapshot.Config.Tunnel.ID}
	}
	state := statusTunnelState(status, snapshot.Running)
	presenter.ChildState(statusPresentationKind(state), "OpenAI Secure MCP Tunnel", state)
	fields := []presentation.Field{}
	if verbose {
		fields = append(fields,
			presentation.Field{Label: "enabled", Value: status.TunnelEnabled},
			presentation.Field{Label: "configured", Value: status.TunnelConfigured},
		)
	}
	if status.TunnelID != "" {
		fields = append(fields, presentation.Field{Label: "id", Value: status.TunnelID})
	}
	if snapshot.Tunnel.Metadata != nil {
		metadata := snapshot.Tunnel.Metadata
		if metadata.Name != "" {
			fields = append(fields, presentation.Field{Label: "name", Value: metadata.Name})
		}
		if verbose {
			if metadata.Description != "" {
				fields = append(fields, presentation.Field{Label: "description", Value: metadata.Description})
			}
			if metadata.Creator != "" {
				fields = append(fields, presentation.Field{Label: "creator", Value: metadata.Creator})
			}
			if len(metadata.WorkspaceIDs) > 0 {
				fields = append(fields, presentation.Field{Label: "workspaces", Value: strings.Join(metadata.WorkspaceIDs, ", ")})
			}
			if len(metadata.OrganizationIDs) > 0 {
				fields = append(fields, presentation.Field{Label: "organizations", Value: strings.Join(metadata.OrganizationIDs, ", ")})
			}
		}
	}
	if verbose && snapshot.Tunnel.Admin.Configured {
		fields = append(fields, presentation.Field{Label: "admin", Value: "configured " + presenter.Separator() + " " + formatTunnelAdminScope(snapshot.Tunnel.Admin.Scope())})
	}
	if verbose && snapshot.Tunnel.MetadataError != "" {
		fields = append(fields, presentation.Field{Label: "metadata", Value: "unavailable: " + snapshot.Tunnel.MetadataError})
	}
	if verbose && status.TunnelLastError != "" {
		fields = append(fields, presentation.Field{Label: "error", Value: status.TunnelLastError})
	}
	presenter.NestedFields(fields...)
}

func renderStatusConfig(presenter *presentation.Presenter, snapshot statusSnapshot, verbose bool) {
	cfg := snapshot.Config
	presenter.Spacer()
	presenter.Section("Config")
	path := compactStatusPath(snapshot.Source.Path)
	fields := []presentation.Field{}
	if verbose {
		path = snapshot.Source.Path
		fields = append(fields, presentation.Field{Label: "initialized", Value: snapshot.Source.Exists})
	}
	fields = append(fields, presentation.Field{Label: "file", Value: path})
	if verbose {
		fields = append(fields, presentation.Field{Label: "format", Value: snapshot.Source.Format})
	}
	separator := presenter.Separator()
	fields = append(fields,
		presentation.Field{Label: "transports", Value: fmt.Sprintf("http %s %s tunnel %s", onOff(cfg.HTTP.MCP.Enabled), separator, onOff(cfg.Tunnel.Enabled))},
		presentation.Field{Label: "auth", Value: fmt.Sprintf("mcp %s %s admin %s", onOff(cfg.HTTP.MCP.Auth.Enabled), separator, onOff(cfg.HTTP.Admin.Auth.Enabled))},
	)
	presenter.Fields(fields...)
	for _, warning := range config.SecurityWarnings(cfg) {
		presenter.ChildStatus(presentation.StatusWarning, warning)
	}
	secondary := []presentation.Field{
		{Label: "workspaces", Value: snapshot.Workspaces},
		{Label: "upstreams", Value: snapshot.Upstreams},
	}
	if snapshot.Update != nil && (verbose || snapshot.Update.Status == updatepkg.StatusAvailable) {
		secondary = append(secondary, presentation.Field{Label: "update", Value: formatCachedUpdate(snapshot.Update)})
		if verbose {
			secondary = append(secondary, presentation.Field{Label: "checked", Value: snapshot.Update.CheckedAt.Local().Format(time.RFC3339)})
		}
	}
	presenter.Fields(secondary...)
}

func renderStatusUninitialized(presenter *presentation.Presenter) {
	presenter.Frame("CodeMCP status")
	presenter.Status(presentation.StatusWarning, "CodeMCP is not initialized")
	presenter.Spacer()
	presenter.Note("Run:", cliUseName()+" init")
	presenter.Complete("Not initialized")
}

func renderLegacyStatus(cmd *cobra.Command, snapshot statusSnapshot) {
	log := commandLogger(cmd)
	cfg, runtimeStatus := snapshot.Config, snapshot.Runtime
	log.Info("STATUS", "local runtime configuration")
	log.Detail("initialized", snapshot.Source.Exists)
	log.Detail("config", snapshot.Source.Path)
	log.Detail("format", snapshot.Source.Format)
	log.Detail("transports", fmt.Sprintf("http=%t tunnel=%t", cfg.HTTP.MCP.Enabled, cfg.Tunnel.Enabled))
	logEndpointDetails(log, cfg)
	log.Detail("auth", fmt.Sprintf("mcp=%t admin=%t", cfg.HTTP.MCP.Auth.Enabled, cfg.HTTP.Admin.Auth.Enabled))
	for _, warning := range config.SecurityWarnings(cfg) {
		name := "status.security-warning"
		switch {
		case strings.Contains(warning, "unauthenticated loopback"):
			name = "status.unauthenticated-loopback"
		case strings.Contains(warning, "cleartext HTTP"):
			name = "status.cleartext-http"
		}
		log.Warning("STATUS", name, warning, nil)
	}
	if snapshot.Running {
		runtimeState := "running"
		if runtimeStatus.Starting {
			runtimeState = "starting"
		}
		log.Detail("runtime", runtimeState)
		log.Detail("managed", runtimeStatus.Managed)
		if runtimeStatus.RunID != "" {
			log.Detail("session", shortSessionID(runtimeStatus.RunID))
		}
		log.Detail("pid", runtimeStatus.PID)
		if !runtimeStatus.StartedAt.IsZero() {
			log.Detail("started", runtimeStatus.StartedAt.Local().Format(time.RFC3339))
		}
		if runtimeStatus.Managed {
			log.Detail("scope", runtimeStatus.ServiceScope)
			log.Detail("backend", runtimeBackendLabel(runtimeStatus.ServiceScope))
			log.Detail("service", runtimeStatus.ServiceID)
		}
		log.Detail("tunnel", runtimeTunnelSummary(runtimeStatus))
		if runtimeStatus.TunnelID != "" {
			log.Detail("tunnel id", runtimeStatus.TunnelID)
		}
	} else {
		log.Detail("runtime", "stopped")
		state := runtimeStatusResult{TunnelEnabled: cfg.Tunnel.Enabled, TunnelConfigured: tunnel.Configured(cfg.Tunnel), TunnelID: cfg.Tunnel.ID}
		log.Detail("tunnel", runtimeTunnelSummary(state))
		if cfg.Tunnel.ID != "" {
			log.Detail("tunnel id", cfg.Tunnel.ID)
		}
		for _, item := range snapshot.Services {
			log.Detail("service "+string(item.spec.Scope), fmt.Sprintf("installed (%s)", managedBackendLabel(item.manager, item.spec)))
		}
	}
	log.Detail("browser", optionalBrowserStatusSummary(snapshot.Browser))
	log.Detail("chatgpt web", optionalChatGPTWebStatusSummary(snapshot.ChatGPTWeb))
	if snapshot.Tunnel.Metadata != nil {
		log.Detail("tunnel name", snapshot.Tunnel.Metadata.Name)
		log.Detail("tunnel description", snapshot.Tunnel.Metadata.Description)
		if len(snapshot.Tunnel.Metadata.WorkspaceIDs) > 0 {
			log.Detail("tunnel workspaces", strings.Join(snapshot.Tunnel.Metadata.WorkspaceIDs, ", "))
		}
		if len(snapshot.Tunnel.Metadata.OrganizationIDs) > 0 {
			log.Detail("tunnel organizations", strings.Join(snapshot.Tunnel.Metadata.OrganizationIDs, ", "))
		}
	}
	if snapshot.Tunnel.MetadataError != "" {
		log.Detail("tunnel metadata error", snapshot.Tunnel.MetadataError)
	}
	log.Detail("workspaces", snapshot.Workspaces)
	log.Detail("upstreams", snapshot.Upstreams)
	logCachedUpdate(log, snapshot.Update)
}

func optionalBrowserStatusSummary(status application.BrowserIntegrationStatus) string {
	if status.Running {
		return "running"
	}
	if status.State != "" {
		return string(status.State)
	}
	return "unavailable"
}

func optionalChatGPTWebStatusSummary(status application.ChatGPTWebStatus) string {
	value := string(status.State)
	if value == "" {
		value = "unavailable"
	}
	if status.RuntimePending {
		value += " (runtime change pending)"
	}
	return value
}

func statusExposureSummary(snapshot statusSnapshot, separator string) string {
	mode := string(snapshot.Config.HTTP.Exposure.Mode)
	if snapshot.ListenerError != nil {
		return mode + " " + separator + " network unavailable"
	}
	count := statusNetworkInterfaceCount(snapshot.ListenerPlan.Addresses)
	if count == 0 {
		return mode
	}
	label := "network interfaces"
	if count == 1 {
		label = "network interface"
	}
	return fmt.Sprintf("%s %s %d %s", mode, separator, count, label)
}

func statusPresentationKind(state string) presentation.StatusKind {
	switch state {
	case "connected", "ready", "running":
		return presentation.StatusSuccess
	case "failed", "error", "unreachable":
		return presentation.StatusError
	case "degraded":
		return presentation.StatusWarning
	case "starting", "connecting", "reconnecting", "disabled", "not configured", "offline":
		return presentation.StatusInactive
	default:
		return presentation.StatusInfo
	}
}

func statusNetworkInterfaceCount(addresses []mcpnetwork.Address) int {
	seen := map[string]struct{}{}
	for _, address := range addresses {
		if address.Interface != "" {
			seen[address.Interface] = struct{}{}
		}
	}
	return len(seen)
}

func statusAddressPriority(address mcpnetwork.Address) int {
	if address.Interface == "" {
		return 0
	}
	name := strings.ToLower(address.Interface)
	switch {
	case strings.HasPrefix(name, "br-"), strings.HasPrefix(name, "veth"), strings.HasPrefix(name, "virbr"), strings.HasPrefix(name, "docker_gwbridge"):
		return 3
	case name == "docker0", strings.HasPrefix(name, "podman"):
		return 2
	default:
		return 1
	}
}

func statusTunnelState(status runtimeStatusResult, runtimeRunning bool) string {
	if !status.TunnelEnabled {
		return "disabled"
	}
	if !status.TunnelConfigured {
		return "not configured"
	}
	if !runtimeRunning {
		return "offline"
	}
	switch {
	case status.TunnelReady:
		return "connected"
	case status.TunnelRestarting:
		return "reconnecting"
	case status.TunnelRunning:
		return "connecting"
	case status.TunnelLastError != "":
		return "failed"
	default:
		return "starting"
	}
}

func transientTunnelState(state string) bool {
	return state == "starting" || state == "connecting" || state == "reconnecting"
}

func animateRuntimeTunnelState(cmd *cobra.Command, status runtimeStatusResult, timeout time.Duration) runtimeStatusResult {
	progress := commandProgressSession(cmd)
	state := statusTunnelState(status, true)
	progress.Update(presentation.ProgressPhase{ID: "tunnel.status", Label: tunnelStateActionMessage(state), State: presentation.ProgressRunning})
	defer progress.Suspend()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(cmd.Context(), time.Second)
		next, running, err := managedRuntimeStatus(ctx)
		cancel()
		if err != nil || !running {
			return status
		}
		status = next
		nextState := statusTunnelState(status, true)
		if !transientTunnelState(nextState) {
			return status
		}
		if nextState != state {
			state = nextState
			progress.Update(presentation.ProgressPhase{ID: "tunnel.status", Label: tunnelStateActionMessage(state), State: presentation.ProgressRunning})
		}
		select {
		case <-cmd.Context().Done():
			return status
		case <-time.After(150 * time.Millisecond):
		}
	}
	return status
}

func tunnelStateActionMessage(state string) string {
	switch state {
	case "starting":
		return "Starting OpenAI Secure MCP Tunnel"
	case "reconnecting":
		return "Reconnecting OpenAI Secure MCP Tunnel"
	default:
		return "Connecting OpenAI Secure MCP Tunnel"
	}
}

func formatStatusUptime(started time.Time) string {
	duration := time.Since(started).Round(time.Second)
	if duration < 0 {
		duration = 0
	}
	days := duration / (24 * time.Hour)
	duration %= 24 * time.Hour
	hours := duration / time.Hour
	duration %= time.Hour
	minutes := duration / time.Minute
	seconds := duration % time.Minute / time.Second
	if days > 0 {
		return fmt.Sprintf("%dd %02dh %02dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func compactStatusPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	if relative == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + relative
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

type installedManagedService struct {
	spec    managed.Spec
	manager managed.Manager
}

func installedManagedServices(ctx context.Context, account managed.Account) []installedManagedService {
	manager := managed.NewManagerWithObserver(tracepkg.ObserverFromContext(ctx))
	scopes := []managed.Scope{managed.ScopeUser}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		scopes = append(scopes, managed.ScopeSystem)
	}
	result := make([]installedManagedService, 0, len(scopes))
	for _, scope := range scopes {
		spec := managed.Spec{ID: managed.ID(config.RootPath(), scope), Scope: scope, ConfigRoot: config.RootPath(), Account: account}
		status, err := manager.Status(spec)
		if err == nil && status.Installed {
			result = append(result, installedManagedService{spec: spec, manager: manager})
		}
	}
	return result
}

func runtimeBackendLabel(scope string) string {
	if runtime.GOOS == "linux" {
		if scope == string(managed.ScopeUser) {
			return "systemd --user"
		}
		return "systemd"
	}
	if runtime.GOOS == "darwin" {
		if scope == string(managed.ScopeUser) {
			return "launchd LaunchAgent"
		}
		return "launchd LaunchDaemon"
	}
	if runtime.GOOS == "windows" {
		return "task-scheduler"
	}
	return "unknown"
}
