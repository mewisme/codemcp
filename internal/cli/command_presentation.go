package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

const (
	presentationTitleAnnotation  = "cm.presentation-title"
	presentationExemptAnnotation = "cm.presentation-exempt"
)

var commandPresentationTitleOverrides = map[string]string{
	"":                "Run CodeMCP",
	"init":            "Initialize CodeMCP",
	"uninit":          "Uninitialize CodeMCP",
	"install":         "Install CodeMCP",
	"upgrade":         "Upgrade CodeMCP",
	"upgrade check":   "Check for updates",
	"up":              "Start CodeMCP",
	"down":            "Stop CodeMCP",
	"restart":         "Restart CodeMCP",
	"status":          "CodeMCP status",
	"doctor":          "CodeMCP doctor",
	"auth status":     "Authentication",
	"config path":     "Configuration path",
	"config get":      "Configuration",
	"config list":     "Configuration",
	"config diff":     "Configuration diff",
	"config why":      "Config why",
	"config set":      "Update configuration",
	"config clear":    "Clear configuration setting",
	"config verify":   "Verify configuration",
	"workspace list":  "Registered workspaces",
	"upstream list":   "Upstream servers",
	"upstream status": "Upstream status",
	"tunnel list":     "Managed OpenAI tunnels",
	"tunnel get":      "Managed OpenAI tunnel",
	"tunnel use":      "Select managed OpenAI tunnel",

	"config export":          "Export configuration",
	"config import":          "Import configuration",
	"config unset":           "Clear configuration setting",
	"config migrate":         "Migrate credentials",
	"config migrate secrets": "Migrate secret files",

	"auth mcp create":    "Authentication",
	"auth mcp enable":    "Authentication",
	"auth mcp disable":   "Authentication",
	"auth admin create":  "Authentication",
	"auth admin enable":  "Authentication",
	"auth admin disable": "Authentication",

	"request grant revoke":     "Runtime session grant",
	"request approve":          "Control approval request",
	"request deny":             "Control approval request",
	"agent completion current": "Current agent completion",
	"agent completion doctor":  "Agent completion health",
	"agent completion list":    "Agent completion history",
	"agent completion view":    "Agent completion",

	"tunnel key set":          "OpenAI tunnel runtime key",
	"tunnel key remove":       "OpenAI tunnel runtime key",
	"tunnel sync":             "Sync OpenAI Secure MCP Tunnel",
	"tunnel configure":        "Configure OpenAI Secure MCP Tunnel",
	"tunnel enable":           "OpenAI Secure MCP Tunnel",
	"tunnel disable":          "OpenAI Secure MCP Tunnel",
	"tunnel admin key set":    "Configure OpenAI tunnel admin key",
	"tunnel admin key verify": "Verify OpenAI tunnel admin key",
	"tunnel admin verify":     "Verify OpenAI tunnel admin key",
	"tunnel admin key remove": "OpenAI tunnel admin key",
	"tunnel create":           "Create managed OpenAI tunnel",
	"tunnel update":           "Update managed OpenAI tunnel",
	"tunnel delete":           "Delete managed OpenAI tunnel",

	"upstream server add":         "Upstream server",
	"upstream server configure":   "Upstream server",
	"upstream server remove":      "Upstream server",
	"upstream server enable":      "Upstream server",
	"upstream server disable":     "Upstream server",
	"upstream server auth login":  "Authorize Upstream server",
	"upstream server auth logout": "Upstream OAuth authorization",

	"workspace relocate":         "Workspace",
	"workspace register":         "Workspace",
	"workspace unregister":       "Workspace",
	"workspace purge":            "Workspace",
	"workspace container create": "Workspace container",
	"workspace container rename": "Workspace container",
	"workspace container delete": "Workspace container",
	"workspace container add":    "Workspace container",
	"workspace container remove": "Workspace container",
	"workspace access add":       "Workspace access",
	"workspace access remove":    "Workspace access",
}

func bindCommandPresentation(root *cobra.Command) {
	if root == nil {
		return
	}
	var bind func(*cobra.Command)
	bind = func(cmd *cobra.Command) {
		if cmd.Runnable() {
			if commandExplicitMachineOutput(cmd) {
				setCommandPresentationExempt(cmd, "machine-output")
			} else if commandPresentationExempt(cmd) {
				// Alternate UIs and internal runtime commands own their output contract.
			} else if commandPresentationTitle(cmd) == "" {
				setCommandPresentationTitle(cmd, defaultCommandPresentationTitle(cmd))
			}
		}
		for _, child := range cmd.Commands() {
			bind(child)
		}
	}
	bind(root)
}

func setCommandPresentationTitle(cmd *cobra.Command, title string) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[presentationTitleAnnotation] = strings.TrimSpace(title)
	delete(cmd.Annotations, presentationExemptAnnotation)
}

func setCommandPresentationExempt(cmd *cobra.Command, reason string) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[presentationExemptAnnotation] = strings.TrimSpace(reason)
	delete(cmd.Annotations, presentationTitleAnnotation)
}

func commandPresentationTitle(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	return strings.TrimSpace(cmd.Annotations[presentationTitleAnnotation])
}

func commandPresentationExempt(cmd *cobra.Command) bool {
	return cmd != nil && strings.TrimSpace(cmd.Annotations[presentationExemptAnnotation]) != ""
}

func defaultCommandPresentationTitle(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	path := relativeCommandPath(cmd)
	if title := strings.TrimSpace(commandPresentationTitleOverrides[path]); title != "" {
		return title
	}
	if title := strings.TrimSpace(cmd.Short); title != "" {
		return strings.TrimSuffix(title, ".")
	}
	name := strings.ReplaceAll(strings.TrimSpace(cmd.Name()), "-", " ")
	if name == "" {
		return "CodeMCP"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func ensureCommandPresentationFallback(cmd *cobra.Command) {
	if cmd == nil || commandPresentationExempt(cmd) || commandExplicitMachineOutput(cmd) || commandResultModeFor(cmd) == resultModeJSON {
		return
	}
	prepareCommandPresentation(cmd)
	commandProgressSession(cmd).EnsureBegun()
}
