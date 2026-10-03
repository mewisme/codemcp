package capability

import "strings"

const (
	OwnerApplicationDiagnostics   = "application.diagnostics"
	OwnerApplicationCompletion    = "application.completion"
	OwnerApplicationMCPRuntime    = "application.mcp-runtime"
	OwnerApplicationExecution     = "application.execution"
	OwnerApplicationNotifications = "application.notifications"
	OwnerApplicationTools         = "application.tools"
)

func CanonicalOwnerFor(id ID) (string, bool) {
	if ownership, ok := MutationOwnershipFor(id); ok {
		return string(ownership.ValidationOwner), true
	}
	value := string(id)
	switch {
	case id == ServerForeground, id == UpdateCheck,
		strings.HasPrefix(value, "runtime."):
		return string(MutationOwnerApplicationLifecycle), true
	case strings.HasPrefix(value, "config."):
		return string(MutationOwnerApplicationSettings), true
	case strings.HasPrefix(value, "logs."):
		return string(MutationOwnerApplicationLogs), true
	case strings.HasPrefix(value, "request."):
		return string(MutationOwnerApproval), true
	case strings.HasPrefix(value, "completion."):
		return OwnerApplicationCompletion, true
	case strings.HasPrefix(value, "managed-agent."):
		return string(MutationOwnerManagedAgent), true
	case id == DoctorRead, id == HealthRead, id == StatusOverview, id == VersionAbout,
		strings.HasPrefix(value, "network."):
		return OwnerApplicationDiagnostics, true
	case strings.HasPrefix(value, "prompt."):
		return string(MutationOwnerApplicationPrompts), true
	case strings.HasPrefix(value, "auth."):
		return string(MutationOwnerApplicationAuth), true
	case strings.HasPrefix(value, "workspace."):
		return string(MutationOwnerApplicationWorkspace), true
	case strings.HasPrefix(value, "mcp."):
		return OwnerApplicationMCPRuntime, true
	case strings.HasPrefix(value, "upstream."), strings.HasPrefix(value, "oauth."):
		return string(MutationOwnerApplicationUpstream), true
	case strings.HasPrefix(value, "tunnel."):
		return string(MutationOwnerApplicationTunnel), true
	case strings.HasPrefix(value, "telemetry."):
		return string(MutationOwnerApplicationTelemetry), true
	case strings.HasPrefix(value, "telegram."):
		return string(MutationOwnerApplicationTelegram), true
	case strings.HasPrefix(value, "integration."):
		return string(MutationOwnerApplicationIntegrations), true
	case strings.HasPrefix(value, "instructions."), strings.HasPrefix(value, "project.context."):
		return string(MutationOwnerApplicationInstructions), true
	case strings.HasPrefix(value, "tools."):
		return OwnerApplicationTools, true
	case strings.HasPrefix(value, "execution."), strings.HasPrefix(value, "activity."):
		return OwnerApplicationExecution, true
	case strings.HasPrefix(value, "process."):
		return string(MutationOwnerApplicationProcesses), true
	case strings.HasPrefix(value, "llm."):
		return string(MutationOwnerApplicationLLM), true
	case strings.HasPrefix(value, "notification."):
		return OwnerApplicationNotifications, true
	case id == InstallRun:
		return string(MutationOwnerApplicationLifecycle), true
	default:
		return "", false
	}
}
