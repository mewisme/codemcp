package config

import (
	"fmt"
	"sort"
	"strings"
)

type Explanation struct {
	Key         string           `json:"key"`
	Label       string           `json:"label"`
	Description string           `json:"description,omitempty"`
	Details     string           `json:"details,omitempty"`
	Branch      bool             `json:"branch"`
	Kind        FieldKind        `json:"type,omitempty"`
	Default     string           `json:"default,omitempty"`
	Values      []FieldValueSpec `json:"values,omitempty"`
	Editable    bool             `json:"editable,omitempty"`
	Sensitive   bool             `json:"sensitive,omitempty"`
	Accepts     string           `json:"accepts,omitempty"`
	Guidance    string           `json:"guidance,omitempty"`
	Related     []string         `json:"related,omitempty"`
	Children    []Explanation    `json:"children,omitempty"`
}

func Explain(key string) (Explanation, error) {
	key = canonicalFieldKey(key)
	if spec, ok := FieldByKey(key); ok {
		return explainField(spec)
	}
	if key != "" && !hasFieldPrefix(key) {
		return Explanation{}, fmt.Errorf("unsupported config key: %s", key)
	}
	return explainBranch(key)
}

func SchemaKeys() []string {
	seen := map[string]bool{"": true}
	for _, spec := range fieldSpecs {
		parts := strings.Split(spec.Key, ".")
		for index := 1; index <= len(parts); index++ {
			seen[strings.Join(parts[:index], ".")] = true
		}
	}
	keys := make([]string, 0, len(seen)-1)
	for key := range seen {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func explainField(spec FieldSpec) (Explanation, error) {
	defaultValue, err := RawValue(Default(), spec.Key)
	if err != nil {
		return Explanation{}, err
	}
	values := append([]FieldValueSpec(nil), spec.Values...)
	if len(values) == 0 {
		for _, option := range spec.Options {
			values = append(values, FieldValueSpec{Value: option})
		}
	}
	return Explanation{Key: spec.Key, Label: spec.Label, Description: spec.Description, Details: spec.Details, Kind: spec.Kind, Default: defaultValue, Values: values, Editable: spec.Editable, Sensitive: spec.Sensitive, Accepts: AcceptedValueHint(spec), Guidance: spec.Guidance, Related: append([]string(nil), spec.Related...)}, nil
}

func explainBranch(key string) (Explanation, error) {
	children := directChildKeys(key)
	result := Explanation{Key: key, Label: branchLabel(key), Description: branchDescription(key), Branch: true, Children: make([]Explanation, 0, len(children))}
	for _, child := range children {
		if spec, ok := FieldByKey(child); ok {
			explanation, err := explainField(spec)
			if err != nil {
				return Explanation{}, err
			}
			result.Children = append(result.Children, explanation)
			continue
		}
		explanation, err := explainBranch(child)
		if err != nil {
			return Explanation{}, err
		}
		result.Children = append(result.Children, explanation)
	}
	return result, nil
}

func directChildKeys(parent string) []string {
	prefix := ""
	if parent != "" {
		prefix = parent + "."
	}
	seen := map[string]bool{}
	for _, spec := range fieldSpecs {
		if !strings.HasPrefix(spec.Key, prefix) || spec.Key == parent {
			continue
		}
		remainder := strings.TrimPrefix(spec.Key, prefix)
		part, _, _ := strings.Cut(remainder, ".")
		child := prefix + part
		seen[child] = true
	}
	children := make([]string, 0, len(seen))
	for child := range seen {
		children = append(children, child)
	}
	sort.Strings(children)
	return children
}

func hasFieldPrefix(key string) bool {
	prefix := key + "."
	for _, spec := range fieldSpecs {
		if strings.HasPrefix(spec.Key, prefix) {
			return true
		}
	}
	return false
}

func branchLabel(key string) string {
	if key == "" {
		return "Configuration"
	}
	part := key
	if index := strings.LastIndexByte(key, '.'); index >= 0 {
		part = key[index+1:]
	}
	return strings.ReplaceAll(part, "_", " ")
}

func branchDescription(key string) string {
	switch key {
	case "":
		return "CodeMCP configuration schema."
	case "server":
		return "MCP HTTP server configuration."
	case "server.expose":
		return "Controls how the MCP HTTP server is exposed."
	case "admin":
		return "Admin HTTP server configuration."
	case "auth":
		return "Authentication configuration."
	case "permissions":
		return "Filesystem access configuration."
	case "shell":
		return "Shell execution, event retention, approval, environment, sandbox, and network configuration."
	case "integrations":
		return "First-party integration configuration."
	case "integrations.ponytail":
		return "Ponytail integration defaults."
	case "integrations.caveman":
		return "Caveman integration defaults."
	case "tunnel":
		return "OpenAI Secure MCP Tunnel configuration."
	default:
		return "Configuration subtree."
	}
}
