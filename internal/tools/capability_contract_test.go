package tools

import (
	"sort"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestBuiltInToolsHaveCanonicalOperationIDs(t *testing.T) {
	runtime := NewRuntime()
	actual := map[string]bool{}
	for _, schema := range runtime.List() {
		actual[schema.Name] = true
		id, ok := capability.ForMCPTool(schema.Name)
		if !ok {
			t.Errorf("built-in MCP tool %q has no canonical operation ID", schema.Name)
			continue
		}
		if _, ok := capability.Lookup(id); !ok {
			t.Errorf("built-in MCP tool %q maps to unknown operation %q", schema.Name, id)
		}
	}

	var stale []string
	for _, spec := range capability.All() {
		for _, name := range spec.MCPTools {
			if !actual[name] {
				stale = append(stale, string(spec.ID)+": "+name)
			}
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("canonical MCP tool bindings missing from built-in runtime:\n  %s", strings.Join(stale, "\n  "))
	}
}
