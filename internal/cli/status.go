package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
}

const statusTunnelWatchTimeout = 35 * time.Second

func statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Aliases: []string{"st"},
		Short:   "Show runtime health and local configuration",
		Args:    cobra.NoArgs,
		RunE:    runStatus,
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
		renderStatusUninitialized(cmd.OutOrStdout())
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
	upstreamSpan := tracepkg.Start(ctx, "STATUS", "status.upstreams.query", "Querying upstream MCP count")
	upstreams, err := loadUpstreamManagerForCommand(cmd)
	if err != nil {
		upstreamSpan.FailMessage("Upstream MCP count query failed", err)
		return err
	}
	upstreamCount := len(upstreams.List())
	upstreamSpan.EndMessage("Upstream MCP count queried", tracepkg.Int("count", upstreamCount))
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
	listenerSpan := tracepkg.Start(ctx, "STATUS", "status.listener-plan.resolve", "Resolving status listener plan", tracepkg.String("exposure_mode", string(cfg.Server.Expose.Mode)), tracepkg.Any("interfaces", append([]string(nil), cfg.Server.Expose.Interfaces...)))
	plan, listenerErr := resolveListenerPlan(cfg.Server.Expose)
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
	snapshot := statusSnapshot{Source: source, Config: cfg, Runtime: runtimeStatus, Running: running, Workspaces: len(workspaces), Upstreams: upstreamCount, ListenerPlan: plan, ListenerError: listenerErr, Tunnel: tunnelStatus, Update: cachedUpdate}
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
	if snapshot.Running && transientTunnelState(statusTunnelState(snapshot.Runtime, true)) && commandAnimationEligible(cmd) {
		renderStatusBaseText(cmd.OutOrStdout(), snapshot, verbose)
		fmt.Fprintln(cmd.OutOrStdout(), "\n"+cliHeading(cmd.OutOrStdout(), "Tunnel"))
		snapshot.Runtime = animateRuntimeTunnelState(cmd, snapshot.Runtime, statusTunnelWatchTimeout)
		snapshot.Tunnel.Running = snapshot.Runtime.TunnelRunning
		snapshot.Tunnel.Ready = snapshot.Runtime.TunnelReady
		snapshot.Tunnel.Restarting = snapshot.Runtime.TunnelRestarting
		snapshot.Tunnel.LastError = snapshot.Runtime.TunnelLastError
		renderStatusTunnelBody(cmd.OutOrStdout(), snapshot, verbose)
		return nil
	}
	renderStatusText(cmd.OutOrStdout(), snapshot, verbose)
	return nil
}

func renderStatusText(out io.Writer, snapshot statusSnapshot, verbose bool) {
	renderStatusBaseText(out, snapshot, verbose)
	renderStatusTunnel(out, snapshot, verbose)
}

func renderStatusBaseText(out io.Writer, snapshot statusSnapshot, verbose bool) {
	glyphs := cliGlyphs(out)
	if snapshot.Running {
		if snapshot.Runtime.Starting {
			fmt.Fprintln(out, cliTone(out, presentation.RoleWarning, glyphs.Active), "CodeMCP is starting")
		} else {
			fmt.Fprintln(out, cliTone(out, presentation.RoleSuccess, glyphs.Success), "CodeMCP is running")
		}
		renderRunningStatus(out, snapshot, verbose)
		return
	}
	fmt.Fprintln(out, cliTone(out, presentation.RoleDanger, glyphs.Error), "CodeMCP is stopped")
	renderStoppedStatus(out, snapshot, verbose)
}

func renderRunningStatus(out io.Writer, snapshot statusSnapshot, verbose bool) {
	status := snapshot.Runtime
	fmt.Fprintln(out, "\n"+cliHeading(out, "Runtime"))
	statusField(out, "pid", status.PID)
	if status.RunID != "" {
		statusField(out, "session", shortSessionID(status.RunID))
	}
	if verbose && !status.StartedAt.IsZero() {
		statusField(out, "started", status.StartedAt.Local().Format(time.RFC3339))
	}
	if !status.StartedAt.IsZero() {
		statusField(out, "uptime", formatStatusUptime(status.StartedAt))
	}
	if verbose {
		statusField(out, "managed", status.Managed)
		if status.Managed {
			statusField(out, "scope", status.ServiceScope)
			statusField(out, "backend", runtimeBackendLabel(status.ServiceScope))
			statusField(out, "service", status.ServiceID)
		}
	} else if status.Managed {
		statusField(out, "managed", strings.TrimSpace(status.ServiceScope+" "+cliSeparator(out)+" "+runtimeBackendLabel(status.ServiceScope)))
		statusField(out, "service", status.ServiceID)
	} else {
		statusField(out, "mode", "foreground")
	}
	renderStatusEndpoints(out, snapshot, verbose)
	renderStatusConfig(out, snapshot, verbose)
}

func renderStoppedStatus(out io.Writer, snapshot statusSnapshot, verbose bool) {
	renderStatusEndpoints(out, snapshot, verbose)
	renderStatusConfig(out, snapshot, verbose)
	if len(snapshot.Services) == 0 {
		return
	}
	fmt.Fprintln(out, "\n"+cliHeading(out, "Service"))
	for _, item := range snapshot.Services {
		statusField(out, string(item.spec.Scope), fmt.Sprintf("installed %s %s", cliSeparator(out), managedBackendLabel(item.manager, item.spec)))
	}
}

func renderStatusEndpoints(out io.Writer, snapshot statusSnapshot, verbose bool) {
	cfg := snapshot.Config
	fmt.Fprintln(out, "\n"+cliHeading(out, "Endpoints"))
	if !verbose {
		if cfg.Server.Enabled {
			statusField(out, "mcp http", endpointURL(mcpnetwork.LoopbackHost, cfg.Server.Port, "/mcp"))
		} else {
			statusField(out, "mcp http", "disabled")
		}
		if cfg.Admin.Enabled {
			statusField(out, "admin", endpointURL(mcpnetwork.LoopbackHost, cfg.Admin.Port, "/"))
		} else {
			statusField(out, "admin", "disabled")
		}
		statusField(out, "exposure", statusExposureSummary(out, snapshot))
		return
	}
	statusField(out, "expose", cfg.Server.Expose.Mode)
	if len(cfg.Server.Expose.Interfaces) > 0 {
		statusField(out, "interfaces", strings.Join(cfg.Server.Expose.Interfaces, ", "))
	}
	if snapshot.ListenerError != nil {
		statusField(out, "network", snapshot.ListenerError.Error())
		return
	}
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
		fmt.Fprintf(out, "\n  %s\n", cliHeading(out, name))
		if cfg.Server.Enabled {
			statusNestedField(out, "mcp http", endpointURL(address.Host, cfg.Server.Port, "/mcp"))
		}
		if cfg.Admin.Enabled {
			statusNestedField(out, "admin", endpointURL(address.Host, cfg.Admin.Port, "/"))
		}
	}
	if !cfg.Server.Enabled {
		statusField(out, "mcp http", "disabled")
	}
	if !cfg.Admin.Enabled && len(addresses) == 0 {
		statusField(out, "admin", "disabled")
	}
}

func renderStatusTunnel(out io.Writer, snapshot statusSnapshot, verbose bool) {
	fmt.Fprintln(out, "\n"+cliHeading(out, "Tunnel"))
	renderStatusTunnelBody(out, snapshot, verbose)
}

func renderStatusTunnelBody(out io.Writer, snapshot statusSnapshot, verbose bool) {
	status := snapshot.Runtime
	if !snapshot.Running {
		status = runtimeStatusResult{TunnelEnabled: snapshot.Config.Tunnel.Enabled, TunnelConfigured: tunnel.Configured(snapshot.Config.Tunnel), TunnelID: snapshot.Config.Tunnel.ID}
	}
	state := statusTunnelState(status, snapshot.Running)
	renderTunnelStateLine(out, state)
	if verbose {
		statusField(out, "enabled", status.TunnelEnabled)
		statusField(out, "configured", status.TunnelConfigured)
	}
	if status.TunnelID != "" {
		statusField(out, "id", status.TunnelID)
	}
	if snapshot.Tunnel.Metadata != nil {
		metadata := snapshot.Tunnel.Metadata
		if metadata.Name != "" {
			statusField(out, "name", metadata.Name)
		}
		if verbose {
			if metadata.Description != "" {
				statusField(out, "description", metadata.Description)
			}
			if metadata.Creator != "" {
				statusField(out, "creator", metadata.Creator)
			}
			if len(metadata.WorkspaceIDs) > 0 {
				statusField(out, "workspaces", strings.Join(metadata.WorkspaceIDs, ", "))
			}
			if len(metadata.OrganizationIDs) > 0 {
				statusField(out, "organizations", strings.Join(metadata.OrganizationIDs, ", "))
			}
		}
	}
	if verbose && snapshot.Tunnel.AdminKeyConfigured && snapshot.Tunnel.AdminScope != nil {
		statusField(out, "admin", "configured "+cliSeparator(out)+" "+formatTunnelAdminScope(*snapshot.Tunnel.AdminScope))
	}
	if verbose && snapshot.Tunnel.MetadataError != "" {
		statusField(out, "metadata", "unavailable: "+snapshot.Tunnel.MetadataError)
	}
	if verbose && status.TunnelLastError != "" {
		statusField(out, "error", status.TunnelLastError)
	}
}

func renderStatusConfig(out io.Writer, snapshot statusSnapshot, verbose bool) {
	cfg := snapshot.Config
	fmt.Fprintln(out, "\n"+cliHeading(out, "Config"))
	path := compactStatusPath(snapshot.Source.Path)
	if verbose {
		path = snapshot.Source.Path
		statusField(out, "initialized", snapshot.Source.Exists)
	}
	statusField(out, "file", path)
	if verbose {
		statusField(out, "format", snapshot.Source.Format)
	}
	separator := cliSeparator(out)
	statusField(out, "transports", fmt.Sprintf("http %s %s tunnel %s", onOff(cfg.Server.Enabled), separator, onOff(cfg.Tunnel.Enabled)))
	statusField(out, "auth", fmt.Sprintf("mcp %s %s admin %s", onOff(cfg.Auth.MCPEnabled), separator, onOff(cfg.Auth.AdminEnabled)))
	glyphs := cliGlyphs(out)
	for _, warning := range config.SecurityWarnings(cfg) {
		fmt.Fprintln(out, "  "+cliTone(out, presentation.RoleWarning, glyphs.Warning)+" "+warning)
	}
	statusField(out, "workspaces", snapshot.Workspaces)
	statusField(out, "upstreams", snapshot.Upstreams)
	if snapshot.Update != nil && (verbose || snapshot.Update.Status == updatepkg.StatusAvailable) {
		statusField(out, "update", formatCachedUpdate(snapshot.Update))
		if verbose {
			statusField(out, "checked", snapshot.Update.CheckedAt.Local().Format(time.RFC3339))
		}
	}
}

func renderStatusUninitialized(out io.Writer) {
	fmt.Fprintln(out, cliTone(out, presentation.RoleWarning, cliGlyphs(out).Warning), "CodeMCP is not initialized")
	fmt.Fprintln(out, "\n"+cliHeading(out, "Run:"))
	fmt.Fprintf(out, "  %s init\n", cliUseName())
}

func renderLegacyStatus(cmd *cobra.Command, snapshot statusSnapshot) {
	log := commandLogger(cmd)
	cfg, runtimeStatus := snapshot.Config, snapshot.Runtime
	log.Info("STATUS", "local runtime configuration")
	log.Detail("initialized", snapshot.Source.Exists)
	log.Detail("config", snapshot.Source.Path)
	log.Detail("format", snapshot.Source.Format)
	log.Detail("transports", fmt.Sprintf("http=%t tunnel=%t", cfg.Server.Enabled, cfg.Tunnel.Enabled))
	logEndpointDetails(log, cfg)
	log.Detail("auth", fmt.Sprintf("mcp=%t admin=%t", cfg.Auth.MCPEnabled, cfg.Auth.AdminEnabled))
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

func statusField(out io.Writer, label string, value any) {
	fmt.Fprintf(out, "  %s %v\n", cliDim(out, fmt.Sprintf("%-11s", label)), value)
}
func statusNestedField(out io.Writer, label string, value any) {
	fmt.Fprintf(out, "    %s %v\n", cliDim(out, fmt.Sprintf("%-9s", label)), value)
}

func statusStateField(out io.Writer, label string, value any) {
	fmt.Fprintf(out, "  %s %s\n", cliDim(out, fmt.Sprintf("%-11s", label)), cliState(out, value))
}

func statusExposureSummary(out io.Writer, snapshot statusSnapshot) string {
	mode := string(snapshot.Config.Server.Expose.Mode)
	if snapshot.ListenerError != nil {
		return mode + " " + cliSeparator(out) + " network unavailable"
	}
	count := statusNetworkInterfaceCount(snapshot.ListenerPlan.Addresses)
	if count == 0 {
		return mode
	}
	label := "network interfaces"
	if count == 1 {
		label = "network interface"
	}
	return fmt.Sprintf("%s %s %d %s", mode, cliSeparator(out), count, label)
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
	log := commandLogger(cmd)
	state := statusTunnelState(status, true)
	log.Action("TUNNEL", "tunnel.status."+state, tunnelStateActionMessage(state))
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
			log.Action("TUNNEL", "tunnel.status."+state, tunnelStateActionMessage(state))
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

func renderTunnelStateLine(out io.Writer, state string) {
	message := "OpenAI Secure MCP Tunnel is " + state
	glyphs := cliGlyphs(out)
	switch state {
	case "connected":
		fmt.Fprintln(out, cliTone(out, presentation.RoleSuccess, glyphs.Success), message)
	case "starting", "connecting", "reconnecting":
		fmt.Fprintln(out, cliTone(out, presentation.RoleAccent, glyphs.Active), message)
	case "failed":
		fmt.Fprintln(out, cliTone(out, presentation.RoleDanger, glyphs.Error), message)
	case "degraded":
		fmt.Fprintln(out, cliTone(out, presentation.RoleWarning, glyphs.Warning), message)
	default:
		fmt.Fprintln(out, cliDim(out, glyphs.PhasePending), message)
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
