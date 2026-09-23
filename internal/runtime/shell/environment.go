package shell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/controlplane"
)

type environmentValue struct {
	name  string
	value string
}

func shellEnvironment(ctx context.Context, shellPath []string) []string {
	parent := parentEnvironment()
	values := map[string]environmentValue{}
	for key, entry := range parent {
		if protectedShellEnvironment(key) {
			continue
		}
		values[key] = entry
	}
	if len(shellPath) > 0 {
		current := []string{}
		if entry, ok := values["PATH"]; ok {
			current = filepath.SplitList(entry.value)
		}
		setShellEnvironment(values, "PATH", strings.Join(mergeExecutablePath(shellPath, current), string(os.PathListSeparator)))
	}
	setShellEnvironment(values, "CI", "true")
	setShellEnvironment(values, "PAGER", "cat")
	setShellEnvironment(values, "GIT_PAGER", "cat")
	setShellEnvironment(values, "NO_COLOR", "1")
	setShellEnvironment(values, "npm_config_yes", "true")
	setShellEnvironment(values, controlplane.ToolContextEnv, "1")
	setShellEnvironment(values, configformat.EnvConfigDir, configformat.RootPath())
	if granted, ok := controlguard.ApprovalFromContext(ctx); ok {
		setShellEnvironment(values, controlplane.ControlApprovalEnv, granted.Capability)
	} else {
		delete(values, strings.ToUpper(controlplane.ControlApprovalEnv))
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		entry := values[key]
		out = append(out, entry.name+"="+entry.value)
	}
	return out
}

func mergeExecutablePath(groups ...[]string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" || !filepath.IsAbs(value) {
				continue
			}
			value = filepath.Clean(value)
			key := value
			if runtime.GOOS == "windows" {
				key = strings.ToLower(key)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func parentEnvironment() map[string]environmentValue {
	values := map[string]environmentValue{}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		values[strings.ToUpper(name)] = environmentValue{name: name, value: value}
	}
	return values
}

func setShellEnvironment(values map[string]environmentValue, name, value string) {
	values[strings.ToUpper(name)] = environmentValue{name: name, value: value}
}

func protectedShellEnvironment(name string) bool {
	switch strings.ToUpper(name) {
	case strings.ToUpper(controlplane.ToolContextEnv), strings.ToUpper(controlplane.ControlApprovalEnv), strings.ToUpper(configformat.EnvConfigDir):
		return true
	default:
		return false
	}
}
