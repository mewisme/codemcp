package commandalias

import "strings"

type Definition struct {
	Command string
	Aliases []string
}

var registry = map[string][]Definition{
	"": {
		{Command: "config", Aliases: []string{"cfg"}},
		{Command: "install", Aliases: []string{"ins"}},
		{Command: "logs", Aliases: []string{"log"}},
		{Command: "request", Aliases: []string{"req"}},
		{Command: "status", Aliases: []string{"st"}},
		{Command: "telegram", Aliases: []string{"tg"}},
		{Command: "telemetry", Aliases: []string{"tel"}},
		{Command: "upgrade", Aliases: []string{"update", "upg"}},
		{Command: "upstream", Aliases: []string{"ups"}},
		{Command: "workspace", Aliases: []string{"ws"}},
	},
	"agent completion": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"show", "info"}},
	},
	"agent": {
		{Command: "list", Aliases: []string{"ls"}},
	},
	"auth": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"config": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "verify", Aliases: []string{"validate"}},
	},
	"execution": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"info"}},
	},
	"integration": {
		{Command: "cf", Aliases: []string{"cf-tunnel"}},
	},
	"integration cf": {
		{Command: "remove", Aliases: []string{"rm"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"integration codegraph": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"integration rtk": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"integration typesafe": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"process": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"info"}},
	},
	"prompt": {
		{Command: "delete", Aliases: []string{"rm"}},
		{Command: "list", Aliases: []string{"ls"}},
	},
	"request": {
		{Command: "approve", Aliases: []string{"accept", "allow"}},
		{Command: "deny", Aliases: []string{"reject"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "view", Aliases: []string{"show", "info"}},
	},
	"request grant": {
		{Command: "list", Aliases: []string{"ls"}},
	},
	"telegram token": {
		{Command: "remove", Aliases: []string{"clear", "rm"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"telemetry": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"tools": {
		{Command: "list", Aliases: []string{"ls"}},
	},
	"tunnel": {
		{Command: "get", Aliases: []string{"info"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "status", Aliases: []string{"st"}},
		{Command: "use", Aliases: []string{"select", "switch"}},
	},
	"tunnel admin key": {
		{Command: "remove", Aliases: []string{"rm"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"tunnel key": {
		{Command: "remove", Aliases: []string{"rm"}},
	},
	"upstream server": {
		{Command: "configure", Aliases: []string{"set"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "remove", Aliases: []string{"rm"}},
		{Command: "show", Aliases: []string{"info"}},
		{Command: "status", Aliases: []string{"st"}},
	},
	"upstream server auth": {
		{Command: "status", Aliases: []string{"st"}},
	},
	"workspace": {
		{Command: "container", Aliases: []string{"ctr"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "relocate", Aliases: []string{"move"}},
		{Command: "show", Aliases: []string{"info"}},
	},
	"workspace access": {
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "remove", Aliases: []string{"rm"}},
	},
	"workspace container": {
		{Command: "delete", Aliases: []string{"rm"}},
		{Command: "list", Aliases: []string{"ls"}},
		{Command: "show", Aliases: []string{"info"}},
	},
}

func Registry() map[string][]Definition {
	out := make(map[string][]Definition, len(registry))
	for parent, definitions := range registry {
		copied := make([]Definition, len(definitions))
		for index, definition := range definitions {
			copied[index] = Definition{
				Command: definition.Command,
				Aliases: append([]string(nil), definition.Aliases...),
			}
		}
		out[parent] = copied
	}
	return out
}

func Aliases(parentPath, command string) []string {
	parentPath = normalizePath(parentPath)
	command = strings.TrimSpace(command)
	for _, definition := range registry[parentPath] {
		if definition.Command == command {
			return append([]string(nil), definition.Aliases...)
		}
	}
	return nil
}

func Canonicalize(args []string) []string {
	result := append([]string(nil), args...)
	parent := ""
	for index, token := range result {
		if strings.HasPrefix(token, "-") {
			break
		}
		command, ok := resolveToken(parent, token)
		if !ok {
			break
		}
		result[index] = command
		parent = normalizePath(strings.TrimSpace(parent + " " + command))
	}
	return result
}

func resolveToken(parentPath, token string) (string, bool) {
	parentPath = normalizePath(parentPath)
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	for _, definition := range registry[parentPath] {
		if definition.Command == token {
			return definition.Command, true
		}
		for _, alias := range definition.Aliases {
			if alias == token {
				return definition.Command, true
			}
		}
	}
	if canonicalChildKnown(parentPath, token) {
		return token, true
	}
	return "", false
}

func canonicalChildKnown(parentPath, token string) bool {
	candidate := normalizePath(strings.TrimSpace(parentPath + " " + token))
	if candidate == "" {
		return false
	}
	for path := range registry {
		path = normalizePath(path)
		if path == candidate || strings.HasPrefix(path, candidate+" ") {
			return true
		}
	}
	return false
}

func normalizePath(path string) string {
	return strings.Join(strings.Fields(path), " ")
}
