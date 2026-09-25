package cli

import (
	"errors"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	installpkg "go.mewis.me/codemcp/internal/install"
	managed "go.mewis.me/codemcp/internal/service"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

type commandFailureAction struct {
	Title   string
	Command string
}

type commandFailure struct {
	Title   string
	Summary string
	Actions []commandFailureAction
}

type tunnelDeleteConfirmationRequiredError struct {
	TunnelID string
}

func (err *tunnelDeleteConfirmationRequiredError) Error() string {
	return "refusing to delete tunnel without --confirm"
}

func classifyCommandFailure(cmd *cobra.Command, err error) commandFailure {
	message := sanitizedCommandError(err)
	path := relativeCommandPath(cmd)

	var runtimeKeyErr *application.ManagedTunnelRuntimeKeyRequiredError
	if errors.As(err, &runtimeKeyErr) {
		id := safeCommandToken(runtimeKeyErr.TunnelID, "<tunnel_id>")
		return commandFailure{
			Title: "Runtime API key required",
			Actions: []commandFailureAction{
				{Title: "Generate automatically", Command: "cm tunnel use " + id + " --auto-runtime-key"},
				{Title: "Use an existing runtime key", Command: "cm tunnel use " + id + " --runtime-api-key <key>"},
				{Title: "More options", Command: "cm tunnel use --help"},
			},
		}
	}

	var deleteConfirmErr *tunnelDeleteConfirmationRequiredError
	if errors.As(err, &deleteConfirmErr) {
		id := safeCommandToken(deleteConfirmErr.TunnelID, "<tunnel_id>")
		return commandFailure{
			Title: "Tunnel deletion requires confirmation",
			Actions: []commandFailureAction{
				{Title: "Confirm permanent deletion", Command: "cm tunnel delete " + id + " --confirm"},
				{Title: "Review deletion options", Command: "cm tunnel delete --help"},
			},
		}
	}

	var oauthErr *upstream.OAuthLoginRequiredError
	if errors.As(err, &oauthErr) {
		id := safeCommandToken(oauthErr.ServerID, "<server_id>")
		return commandFailure{
			Title: "Upstream OAuth authorization required",
			Actions: []commandFailureAction{
				{Title: "Authorize the Upstream server", Command: "cm upstream server auth login " + id},
				{Title: "Inspect authorization state", Command: "cm upstream server auth status " + id},
			},
		}
	}

	var ownerErr *managed.RuntimeOwnerConflictError
	if errors.As(err, &ownerErr) {
		failure := commandFailure{Title: "Managed runtime ownership conflict", Summary: message}
		switch ownerErr.Kind {
		case managed.RuntimeOwnerSystem:
			failure.Actions = []commandFailureAction{{Title: "Use the system managed service", Command: "cm " + safeCommandToken(ownerErr.Action, "status") + " --system"}}
		case managed.RuntimeOwnerUser:
			failure.Actions = []commandFailureAction{{Title: "Use the user managed service", Command: "cm " + safeCommandToken(ownerErr.Action, "status")}}
		case managed.RuntimeOwnerForeground:
			failure.Actions = []commandFailureAction{{Title: "Inspect the running runtime", Command: "cm status"}}
		default:
			failure.Actions = []commandFailureAction{{Title: "Inspect managed runtime state", Command: "cm status"}}
		}
		return failure
	}

	var runtimeErr *runtimeUnavailableError
	if errors.As(err, &runtimeErr) {
		return commandFailure{Title: "CodeMCP runtime is not running", Actions: []commandFailureAction{
			{Title: "Start the managed runtime", Command: "cm up"},
			{Title: "Inspect runtime status", Command: "cm status"},
		}}
	}

	var upstreamMissing *upstreamServerNotFoundError
	if errors.As(err, &upstreamMissing) {
		return commandFailure{Title: "Upstream server not found", Actions: []commandFailureAction{{Title: "List configured Upstream servers", Command: "cm upstream server list"}}}
	}
	var upstreamExists *upstreamServerExistsError
	if errors.As(err, &upstreamExists) {
		id := safeCommandToken(upstreamExists.ServerID, "<server_id>")
		return commandFailure{Title: "Upstream server already exists", Actions: []commandFailureAction{
			{Title: "Configure the existing server", Command: "cm upstream server configure " + id + " --help"},
			{Title: "Inspect configured servers", Command: "cm upstream server list"},
		}}
	}

	switch {
	case errors.Is(err, application.ErrTunnelAdminDisabled):
		return commandFailure{Title: "Tunnel administration is disabled", Actions: []commandFailureAction{{Title: "Enable tunnel administration", Command: "cm tunnel admin enable"}}}
	case errors.Is(err, application.ErrTunnelAdminNotConfigured), errors.Is(err, application.ErrTunnelAdminKeyRequired):
		return commandFailure{Title: "Tunnel admin credentials required", Actions: []commandFailureAction{
			{Title: "Configure an admin key", Command: "cm tunnel admin key set <admin-api-key>"},
			{Title: "Verify configured access", Command: "cm tunnel admin verify"},
		}}
	case errors.Is(err, application.ErrTunnelAdminReadRequired), errors.Is(err, application.ErrTunnelAdminManageRequired):
		return commandFailure{Title: "Tunnel admin access is not verified", Actions: []commandFailureAction{{Title: "Verify tunnel admin access", Command: "cm tunnel admin verify"}}}
	case errors.Is(err, application.ErrTunnelRuntimeConfigRequired):
		return commandFailure{Title: "Tunnel runtime configuration required", Actions: []commandFailureAction{
			{Title: "Configure the tunnel runtime", Command: "cm tunnel configure --help"},
			{Title: "Inspect tunnel status", Command: "cm tunnel status"},
		}}
	case errors.Is(err, application.ErrTunnelAdminAPIKeyRequired):
		return commandFailure{Title: "OpenAI admin key required", Actions: []commandFailureAction{{Title: "Configure an admin key", Command: "cm tunnel admin key set <admin-api-key>"}}}
	case errors.Is(err, application.ErrNotInitialized):
		return commandFailure{Title: "CodeMCP is not initialized", Actions: []commandFailureAction{{Title: "Initialize CodeMCP", Command: "cm init"}}}
	case errors.Is(err, errMCPAuthCredentialMissing):
		return commandFailure{Title: "MCP authentication credential required", Actions: []commandFailureAction{{Title: "Create an MCP credential", Command: "cm auth mcp create"}}}
	case errors.Is(err, errLogsClearConfirmationRequired):
		return commandFailure{Title: "Clearing runtime logs requires confirmation", Actions: []commandFailureAction{{Title: "Confirm log deletion", Command: "cm logs clear --force"}}}
	case errors.Is(err, application.ErrConfigurationExists):
		return commandFailure{Title: "Configuration already exists", Actions: []commandFailureAction{{Title: "Reinitialize and rotate tokens", Command: "cm init --force"}}}
	case errors.Is(err, application.ErrRuntimeImportActive):
		return commandFailure{Title: "Running runtime blocks configuration import", Actions: []commandFailureAction{{Title: "Stop the managed runtime", Command: "cm down"}}}
	case errors.Is(err, workspace.ErrNotFound):
		return commandFailure{Title: "Workspace not found", Actions: []commandFailureAction{{Title: "List registered workspaces", Command: "cm workspace list"}}}
	case errors.Is(err, workspace.ErrUnavailable):
		return commandFailure{Title: "Workspace is unavailable", Actions: []commandFailureAction{{Title: "Inspect registered workspaces", Command: "cm workspace list"}}}
	case errors.Is(err, workspace.ErrAlreadyActive):
		return commandFailure{Title: "Workspace is already active", Actions: []commandFailureAction{{Title: "Inspect registered workspaces", Command: "cm workspace list"}}}
	case errors.Is(err, workspace.ErrStateLost):
		return commandFailure{Title: "Workspace runtime state changed unexpectedly", Actions: []commandFailureAction{{Title: "Inspect registered workspaces", Command: "cm workspace list"}}}
	case errors.Is(err, workspace.ErrPurgeNotConfirmed):
		return commandFailure{Title: "Workspace purge requires confirmation", Actions: []commandFailureAction{{Title: "Review purge options", Command: "cm workspace purge --help"}}}
	case errors.Is(err, workspace.ErrRegistryBusy):
		return commandFailure{Title: "Workspace registry is busy", Actions: []commandFailureAction{{Title: "Inspect workspace state", Command: "cm workspace list"}}}
	case errors.Is(err, workspace.ErrContainerNotFound):
		return commandFailure{Title: "Workspace container not found", Actions: []commandFailureAction{{Title: "List workspace containers", Command: "cm workspace container list"}}}
	case errors.Is(err, approval.ErrRequestNotFound), errors.Is(err, approval.ErrRequestAmbiguous):
		return commandFailure{Title: "Approval request could not be resolved", Actions: []commandFailureAction{{Title: "List approval requests", Command: "cm request list"}}}
	case errors.Is(err, approval.ErrRequestResolved):
		return commandFailure{Title: "Approval request is already resolved", Actions: []commandFailureAction{{Title: "Inspect approval requests", Command: "cm request list"}}}
	case errors.Is(err, approval.ErrRequestNotApproved):
		return commandFailure{Title: "Approval request is not approved", Actions: []commandFailureAction{{Title: "Inspect approval requests", Command: "cm request list"}}}
	case errors.Is(err, approval.ErrSessionRequestActive):
		return commandFailure{Title: "An approval request is already active for this session", Actions: []commandFailureAction{{Title: "Inspect approval requests", Command: "cm request list"}}}
	case errors.Is(err, approval.ErrPendingLimit):
		return commandFailure{Title: "Approval request limit reached", Actions: []commandFailureAction{{Title: "Resolve pending approval requests", Command: "cm request list"}}}
	case errors.Is(err, approval.ErrChallengeExpired), errors.Is(err, approval.ErrCapabilityExpired):
		return commandFailure{Title: "Approval authorization expired", Actions: []commandFailureAction{{Title: "Inspect current approval requests", Command: "cm request list"}}}
	case errors.Is(err, approval.ErrRuntimeGrantNotFound):
		return commandFailure{Title: "Runtime session grant not found", Actions: []commandFailureAction{{Title: "List runtime session grants", Command: "cm request grant list"}}}
	case errors.Is(err, installpkg.ErrDevelopmentBuild):
		return commandFailure{Title: "Development build requires explicit installation", Actions: []commandFailureAction{{Title: "Install the development build", Command: "cm install --force"}}}
	case errors.Is(err, installpkg.ErrCanonicalConflict):
		return commandFailure{Title: "Canonical cm command path is occupied", Actions: []commandFailureAction{{Title: "Inspect installation options", Command: "cm install --help"}}}
	case errors.Is(err, installpkg.ErrVersionConflict):
		return commandFailure{Title: "Installed version conflicts with this binary", Actions: []commandFailureAction{{Title: "Inspect installation options", Command: "cm install --help"}}}
	case errors.Is(err, installpkg.ErrMetadataNotFound):
		return commandFailure{Title: "Managed installation metadata not found", Actions: []commandFailureAction{{Title: "Install CodeMCP", Command: "cm install"}}}
	case errors.Is(err, installpkg.ErrCurrentNotManaged):
		return commandFailure{Title: "Current installation is not managed by CodeMCP", Actions: []commandFailureAction{{Title: "Inspect installation options", Command: "cm install --help"}}}
	case errors.Is(err, updatepkg.ErrDevelopmentUpdate):
		return commandFailure{Title: "Development builds cannot self-update", Actions: []commandFailureAction{{Title: "Inspect installation options", Command: "cm install --help"}}}
	case errors.Is(err, updatepkg.ErrSelfUpdateUnavailable):
		return commandFailure{Title: "Self-update is unavailable for this installation", Actions: []commandFailureAction{{Title: "Check update options", Command: "cm upgrade --help"}}}
	case errors.Is(err, updatepkg.ErrChecksumMismatch):
		return commandFailure{Title: "Release checksum verification failed"}
	case errors.Is(err, updatepkg.ErrCurrentVersionMismatch):
		return commandFailure{Title: "Running version does not match the managed installation", Actions: []commandFailureAction{{Title: "Inspect the current installation", Command: "cm status"}}}
	case errors.Is(err, updatepkg.ErrInvalidVersion):
		return commandFailure{Title: "Invalid release version", Actions: []commandFailureAction{{Title: "Review upgrade options", Command: "cm upgrade --help"}}}
	}

	var operationErr *application.OperationError
	if errors.As(err, &operationErr) {
		title := operationFailureTitle(operationErr.Code)
		actions := operationFailureActions(path, operationErr.Code)
		if title != "" {
			return commandFailure{Title: title, Summary: message, Actions: actions}
		}
	}

	return commandFailure{Title: message}
}

func operationFailureTitle(code application.ErrorCode) string {
	switch code {
	case application.ErrorInvalidArgument:
		return "Invalid input"
	case application.ErrorNotFound:
		return "Resource not found"
	case application.ErrorConflict:
		return "Resource conflict"
	case application.ErrorUnavailable:
		return "Resource unavailable"
	case application.ErrorUnsupported:
		return "Operation unsupported"
	case application.ErrorInternal:
		return "Operation failed"
	default:
		return ""
	}
}

func operationFailureActions(path string, code application.ErrorCode) []commandFailureAction {
	if code != application.ErrorNotFound && code != application.ErrorUnavailable && code != application.ErrorConflict {
		return nil
	}
	switch {
	case strings.HasPrefix(path, "workspace"):
		return []commandFailureAction{{Title: "List registered workspaces", Command: "cm workspace list"}}
	case strings.HasPrefix(path, "upstream"):
		return []commandFailureAction{{Title: "List Upstream servers", Command: "cm upstream server list"}}
	}
	return nil
}

func sanitizedCommandError(err error) string {
	if err == nil {
		return "Command failed"
	}
	message := strings.TrimSpace(tracepkg.SanitizeText(tracepkg.SanitizeError(err)))
	if message == "" {
		return "Command failed"
	}
	return message
}

func safeCommandToken(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._:-", r) {
			continue
		}
		return fallback
	}
	return value
}
