package upstream

import (
	"errors"
	"fmt"
	"strings"
)

var ErrToolNotExposed = errors.New("upstream tool is not exposed")

func ProxyName(prefix, tool string) string {
	prefix = invalidPrefix.ReplaceAllString(strings.TrimSpace(prefix), "_")
	tool = invalidPrefix.ReplaceAllString(strings.TrimSpace(tool), "_")
	return prefix + "__" + tool
}

func ToolIsExposed(server Server, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || !server.Enabled || server.Expose == "none" || server.Expose == "meta_only" {
		return false
	}
	if stringSet(server.DisabledTools)[toolName] {
		return false
	}
	if server.Expose == "allowlist" && !stringSet(server.Tools)[toolName] {
		return false
	}
	return true
}

func ToolIsProxied(server Server, tool Tool) bool {
	return ToolIsExposed(server, tool.Name)
}

func toolExposureError(serverID, tool string) error {
	return fmt.Errorf("%w: %s:%s", ErrToolNotExposed, strings.TrimSpace(serverID), strings.TrimSpace(tool))
}
