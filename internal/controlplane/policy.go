package controlplane

import (
	"os"
	"strings"
)

const (
	ToolContextEnv     = "CHATGPT_MCP_TOOL_CONTEXT"
	ControlApprovalEnv = "CHATGPT_MCP_CONTROL_APPROVAL"
)

var readOnlyPaths = map[string]bool{
	"help": true, "version": true, "status": true, "completion": true,
	"config path": true, "config get": true, "config list": true, "config explain": true, "config verify": true, "config validate": true,
	"auth status": true, "upgrade check": true,
	"request list": true, "request view": true, "request grant list": true,
	"workspace list": true, "workspace show": true, "workspace access list": true,
	"mcp server list": true, "mcp server show": true, "mcp server status": true, "mcp server tools": true,
	"tunnel status": true, "logs": true, "logs follow": true, "logs path": true,
}

var ancestorContextCheck = true

func ToolContextActive() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ToolContextEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return ancestorContextCheck && ancestorToolContextActive()
}

func DisableAncestorContextForTesting() func() {
	previous := ancestorContextCheck
	ancestorContextCheck = false
	return func() { ancestorContextCheck = previous }
}

func IsReadOnlyPath(path string) bool {
	path = strings.Join(strings.Fields(path), " ")
	return readOnlyPaths[path] || strings.HasPrefix(path, "completion ")
}

func IsReadOnlyArgs(args []string) bool {
	return IsReadOnlyPath(PathFromArgs(args))
}

func ApprovalEligibleArgs(args []string) bool {
	if IsReadOnlyArgs(args) {
		return false
	}
	path := PathFromArgs(args)
	if path == "" || path == "_service" || strings.HasPrefix(path, "_service ") {
		return false
	}
	if path == "request" || strings.HasPrefix(path, "request ") {
		return false
	}
	return true
}

func PathFromArgs(args []string) string {
	args = stripGlobalFlags(args)
	if len(args) == 0 {
		return ""
	}
	args = canonicalCommandArgs(args)
	if args[0] == "help" || args[0] == "version" || args[0] == "status" {
		return args[0]
	}
	if len(args) < 2 {
		return args[0]
	}
	switch args[0] {
	case "config":
		return strings.Join(args[:2], " ")
	case "auth", "request", "workspace", "tunnel", "upgrade":
		if args[0] == "workspace" && args[1] == "access" && len(args) >= 3 {
			return strings.Join(args[:3], " ")
		}
		if args[0] == "request" && args[1] == "grant" && len(args) >= 3 {
			return strings.Join(args[:3], " ")
		}
		return strings.Join(args[:2], " ")
	case "mcp":
		if args[1] == "server" && len(args) >= 3 {
			return strings.Join(args[:3], " ")
		}
		return strings.Join(args[:2], " ")
	case "logs", "log":
		if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
			path := strings.Join(args[:2], " ")
			if args[0] == "log" {
				path = "logs" + strings.TrimPrefix(path, "log")
			}
			return path
		}
		return "logs"
	default:
		return args[0]
	}
}

func canonicalCommandArgs(args []string) []string {
	result := append([]string(nil), args...)
	for index, value := range result {
		if strings.HasPrefix(value, "-") {
			continue
		}
		if index == 0 && value == "update" {
			result[index] = "upgrade"
			continue
		}
		switch value {
		case "cfg":
			result[index] = "config"
		case "ws":
			result[index] = "workspace"
		case "ls":
			result[index] = "list"
		case "st":
			result[index] = "status"
		case "log":
			result[index] = "logs"
		case "req":
			result[index] = "request"
		}
	}
	if len(result) >= 2 && result[0] == "request" {
		switch result[1] {
		case "show", "info":
			result[1] = "view"
		case "accept", "allow":
			result[1] = "approve"
		case "reject":
			result[1] = "deny"
		}
	}
	return result
}

func stripGlobalFlags(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		flag := strings.ToLower(args[0])
		switch {
		case flag == "-h" || flag == "--help":
			return []string{"help"}
		case flag == "-v" || flag == "--version":
			return []string{"version"}
		case flag == "--debug" || flag == "--verbose" || flag == "--expose" || strings.HasPrefix(flag, "--expose="):
			args = args[1:]
		case strings.HasPrefix(flag, "--config-dir=") || strings.HasPrefix(flag, "--log-format="):
			args = args[1:]
		case flag == "--config-dir" || flag == "--log-format":
			if len(args) < 2 {
				return nil
			}
			args = args[2:]
		default:
			return nil
		}
	}
	return args
}
