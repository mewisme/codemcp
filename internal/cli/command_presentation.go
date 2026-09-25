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
