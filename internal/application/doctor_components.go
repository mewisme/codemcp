package application

import (
	"strings"

	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/doctor"
	codegraph "go.mewis.me/codemcp/internal/integrations/codegraph"
	rtk "go.mewis.me/codemcp/internal/integrations/rtk"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/workspace"
)

func runtimeControlDoctorComponent(status runtimecontrol.RuntimeStatus, running bool) doctor.Component {
	if !running {
		return disabled("managed runtime is not running")
	}
	component := doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "runtime control endpoint is reachable",
		Metrics: []doctor.Metric{
			{ID: "pid", Value: int64(status.PID)},
			{ID: "server_port", Value: int64(status.ServerPort)},
			{ID: "admin_port", Value: int64(status.AdminPort)},
			{ID: "tool_count", Value: int64(status.ToolCount)},
		},
		Flags: []doctor.Flag{
			{ID: "managed", Value: status.Managed},
			{ID: "starting", Value: status.Starting},
			{ID: "server_enabled", Value: status.ServerEnabled},
			{ID: "admin_enabled", Value: status.AdminEnabled},
			{ID: "tunnel_enabled", Value: status.TunnelEnabled},
			{ID: "tunnel_configured", Value: status.TunnelConfigured},
			{ID: "tunnel_running", Value: status.TunnelRunning},
			{ID: "tunnel_ready", Value: status.TunnelReady},
			{ID: "tunnel_restarting", Value: status.TunnelRestarting},
		},
	}
	lifecycle := strings.ToLower(strings.TrimSpace(status.Lifecycle))
	if status.Starting || lifecycle != "" && lifecycle != "ready" {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "managed runtime is not ready"
	}
	if status.TunnelEnabled && status.TunnelConfigured && (status.TunnelRestarting || status.TunnelRunning && !status.TunnelReady) {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "managed runtime tunnel is reconnecting or not ready"
	}
	return component
}

func serviceDoctorComponent(status ServiceOverview) doctor.Component {
	if !status.Supported {
		return disabled("managed service scope is unsupported")
	}
	component := doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "managed service state is readable",
		Metrics: []doctor.Metric{{ID: "pid", Value: int64(status.PID)}},
		Flags: []doctor.Flag{
			{ID: "installed", Value: status.Installed},
			{ID: "running", Value: status.Running},
		},
	}
	if strings.TrimSpace(status.Err) != "" || strings.TrimSpace(status.Warning) != "" {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "managed service requires attention"
	}
	return component
}

func shellDoctorComponent(status shellruntime.ProviderDiagnostic) doctor.Component {
	if !status.Available {
		return doctor.Component{
			State: doctor.StateDegraded, Severity: doctor.SeverityWarning, Summary: "no usable shell provider is available",
			Metrics: []doctor.Metric{{ID: "configured_paths", Value: int64(status.ConfiguredPaths)}},
			Flags: []doctor.Flag{
				{ID: "invalid_configuration", Value: status.ErrorCode == "invalid_configuration"},
			},
			Remediations: []doctor.Remediation{{ID: "shell_path", Summary: "Configure a supported shell search path", Operation: string(capability.ConfigSet)}},
		}
	}
	return doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "shell provider is available",
		Metrics: []doctor.Metric{{ID: "configured_paths", Value: int64(status.ConfiguredPaths)}},
		Flags: []doctor.Flag{
			{ID: "configured_source", Value: status.Source == shellruntime.ProviderSourceConfigured},
			{ID: "posix", Value: status.Kind == shellruntime.ProviderPOSIX},
			{ID: "git_bash", Value: status.Kind == shellruntime.ProviderGitBash},
			{ID: "powershell7", Value: status.Kind == shellruntime.ProviderPowerShell7},
			{ID: "windows_powershell", Value: status.Kind == shellruntime.ProviderWindowsPowerShell},
		},
	}
}

func rtkDoctorComponent(status rtk.Status) doctor.Component {
	if !status.Enabled {
		return disabled("RTK integration is disabled")
	}
	if status.Source == rtk.SourceUnavailable {
		return degraded("RTK integration is enabled but unavailable", doctor.Remediation{ID: "rtk_configure", Summary: "Configure or install RTK", Operation: string(capability.ConfigSet)})
	}
	return doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "RTK integration is available",
		Flags: []doctor.Flag{
			{ID: "verified", Value: status.Verified},
			{ID: "managed_supported", Value: status.ManagedSupported},
			{ID: "managed_installed", Value: status.ManagedInstalled},
		},
	}
}

func codeGraphDoctorComponent(status codegraph.Status) doctor.Component {
	if !status.Enabled {
		return disabled("CodeGraph integration is disabled")
	}
	if status.Resolution.Source == codegraph.ExecutableUnavailable {
		return degraded("CodeGraph integration is enabled but unavailable", doctor.Remediation{ID: "codegraph_configure", Summary: "Configure or install CodeGraph", Operation: string(capability.ConfigSet)})
	}
	return doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "CodeGraph integration is available",
		Flags: []doctor.Flag{
			{ID: "verified", Value: status.Resolution.Verified},
			{ID: "managed_supported", Value: status.ManagedSupported},
			{ID: "managed_installed", Value: status.ManagedInstalled},
		},
	}
}

func typeSafeDoctorComponent(enabled, configured bool) doctor.Component {
	if !enabled {
		return disabled("TypeSafe integration is disabled")
	}
	if !configured {
		return degraded("TypeSafe integration is enabled without a configured credential", doctor.Remediation{ID: "typesafe_key", Summary: "Configure the TypeSafe API key", Operation: string(capability.ConfigSet)})
	}
	return healthy("TypeSafe integration is configured")
}

func tunnelDoctorComponent(inspection config.Inspection, status *tunnel.Status) doctor.Component {
	if !inspection.Config.Tunnel.Enabled {
		return disabled("OpenAI Secure MCP Tunnel is disabled")
	}
	if strings.TrimSpace(inspection.Config.Tunnel.ID) == "" || !inspection.TunnelRuntimeKeyConfigured {
		return degraded("OpenAI Secure MCP Tunnel configuration is incomplete", doctor.Remediation{ID: "tunnel_configure", Summary: "Configure the Secure MCP Tunnel", Operation: string(capability.TunnelConfigure)})
	}
	if status == nil {
		return doctor.Component{
			State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "OpenAI Secure MCP Tunnel is configured",
			Flags: []doctor.Flag{{ID: "runtime_attached", Value: false}},
		}
	}
	component := doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "OpenAI Secure MCP Tunnel runtime is healthy",
		Flags: []doctor.Flag{
			{ID: "runtime_attached", Value: true},
			{ID: "running", Value: status.Running},
			{ID: "ready", Value: status.Ready},
			{ID: "restarting", Value: status.Restarting},
		},
	}
	if status.Restarting || status.Running && !status.Ready {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "OpenAI Secure MCP Tunnel runtime is reconnecting or not ready"
	}
	return component
}

func backgroundDeliveryDoctorComponent(status backgrounddelivery.Diagnostics) doctor.Component {
	component := doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "background delivery lifecycle is readable",
		Metrics: []doctor.Metric{
			{ID: "pending", Value: int64(status.Pending)},
			{ID: "claimed", Value: int64(status.Claimed)},
			{ID: "retrying", Value: int64(status.Retrying)},
			{ID: "dead_letters", Value: int64(status.DeadLetters)},
			{ID: "committed", Value: int64(status.Committed)},
			{ID: "acknowledged", Value: int64(status.Acknowledged)},
			{ID: "suppressed", Value: int64(status.Suppressed)},
			{ID: "oldest_pending_age_ms", Value: status.OldestPendingAgeMS},
			{ID: "oldest_retry_age_ms", Value: status.OldestRetryAgeMS},
			{ID: "oldest_dead_letter_age_ms", Value: status.OldestDeadLetterAgeMS},
			{ID: "subscribers", Value: int64(status.Subscribers)},
			{ID: "overflow_dropped", Value: int64(status.OverflowDropped)},
			{ID: "continuation_adapters", Value: int64(status.ContinuationAdapters)},
			{ID: "continuation_owners", Value: int64(status.ContinuationOwners)},
			{ID: "oldest_continuation_age_ms", Value: status.OldestContinuationAgeMS},
			{ID: "persistence_failures", Value: status.PersistenceFailures},
		},
	}
	if status.DeadLetters > 0 || status.OverflowDropped > 0 || status.PersistenceFailures > 0 {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "background delivery requires attention"
	}
	return component
}

func workspaceLocalStateDoctorComponent(values []workspace.LocalStateDiagnostic, failures int) doctor.Component {
	attention := int64(failures)
	for _, value := range values {
		if value.Locked || strings.TrimSpace(value.Error) != "" || value.Health != workspace.LocalStateHealthy {
			attention++
		}
	}
	component := doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "workspace local state is readable",
		Metrics: []doctor.Metric{
			{ID: "workspaces", Value: int64(len(values) + failures)},
			{ID: "attention", Value: attention},
		},
	}
	if attention > 0 {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "one or more workspaces require attention"
		component.Remediations = []doctor.Remediation{{ID: "workspace_review", Summary: "Inspect affected workspace state", Operation: string(capability.WorkspaceShow)}}
	}
	return component
}

func checkpointHistoryDoctorComponent(values []checkpoint.StorageHealth) doctor.Component {
	degraded, corrupt := int64(0), int64(0)
	orphanActive, orphanArchive, temporary := int64(0), int64(0), int64(0)
	for _, value := range values {
		switch value.Status {
		case checkpoint.HealthDegraded:
			degraded++
		case checkpoint.HealthCorrupt:
			corrupt++
		}
		orphanActive += int64(value.OrphanActivePayloads)
		orphanArchive += int64(value.OrphanArchivePayloads)
		temporary += int64(value.TemporaryPayloads)
	}
	component := doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "checkpoint history is readable",
		Metrics: []doctor.Metric{
			{ID: "workspaces", Value: int64(len(values))},
			{ID: "degraded", Value: degraded},
			{ID: "corrupt", Value: corrupt},
			{ID: "orphan_active_payloads", Value: orphanActive},
			{ID: "orphan_archive_payloads", Value: orphanArchive},
			{ID: "temporary_payloads", Value: temporary},
		},
	}
	if degraded+corrupt > 0 {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "checkpoint history requires attention"
		component.Remediations = []doctor.Remediation{{ID: "checkpoint_review", Summary: "Review checkpoint history diagnostics", Operation: string(capability.HealthRead)}}
	}
	return component
}
