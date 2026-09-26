package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogHashIsOrderIndependentAndSchemaSensitive(t *testing.T) {
	first := Schema{Name: "a", Description: "one", InputSchema: json.RawMessage(`{"type":"object"}`)}
	second := Schema{Name: "b", Description: "two", InputSchema: json.RawMessage(`{"type":"object"}`)}
	left, err := CatalogHash([]Schema{first, second})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CatalogHash([]Schema{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if left != right || left == "" {
		t.Fatalf("hashes = %q / %q", left, right)
	}
	second.Description = "changed"
	changed, err := CatalogHash([]Schema{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if changed == left {
		t.Fatal("catalog hash did not change with schema")
	}
}

func TestRuntimeDoesNotExposeWorkspaceRelocateTool(t *testing.T) {
	runtime := NewRuntime()
	for _, schema := range runtime.ListTools() {
		name := strings.ToLower(strings.TrimSpace(schema.Name))
		if name == "workspace_relocate" || name == "workspace.relocate" || name == "workspace_move" {
			t.Fatalf("control-plane workspace relocate leaked into MCP tool catalog as %q", schema.Name)
		}
	}
}

func TestToolAnnotationsReflectEffects(t *testing.T) {
	for _, test := range []struct {
		risk                                         Risk
		readOnly, destructive, openWorld, idempotent bool
	}{
		{risk: RiskRead, readOnly: true, idempotent: true},
		{risk: RiskEdit},
		{risk: RiskDestructive, destructive: true},
		{risk: RiskCommand, destructive: true, openWorld: true},
	} {
		annotations := ToolAnnotations(test.risk)
		for key, want := range map[string]bool{"readOnlyHint": test.readOnly, "destructiveHint": test.destructive, "openWorldHint": test.openWorld, "idempotentHint": test.idempotent} {
			if got, ok := annotations[key].(bool); !ok || got != want {
				t.Fatalf("risk=%s %s=%#v want=%t", test.risk, key, annotations[key], want)
			}
		}
	}
	open := ToolAnnotationsOpenWorld(RiskEdit)
	if readOnly, _ := open["readOnlyHint"].(bool); readOnly {
		t.Fatal("open-world edit became read-only")
	}
	if openWorld, _ := open["openWorldHint"].(bool); !openWorld {
		t.Fatal("open-world edit did not set openWorldHint")
	}
}

func TestCoreToolSchemasMatchHandlerContracts(t *testing.T) {
	runtime, _, _ := newToolTestRuntime(t)
	schemas := map[string]Schema{}
	for _, schema := range runtime.List() {
		if !json.Valid(schema.InputSchema) {
			t.Fatalf("%s has invalid input schema", schema.Name)
		}
		if len(schema.OutputSchema) > 0 && !json.Valid(schema.OutputSchema) {
			t.Fatalf("%s has invalid output schema", schema.Name)
		}
		schemas[schema.Name] = schema
	}
	property := func(tool, name string, output bool) map[string]any {
		schema := schemas[tool]
		raw := schema.InputSchema
		if output {
			raw = schema.OutputSchema
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		properties, _ := value["properties"].(map[string]any)
		item, _ := properties[name].(map[string]any)
		return item
	}
	required := func(tool, name string) bool {
		var value map[string]any
		if err := json.Unmarshal(schemas[tool].InputSchema, &value); err != nil {
			t.Fatal(err)
		}
		items, _ := value["required"].([]any)
		for _, item := range items {
			if item == name {
				return true
			}
		}
		return false
	}
	if !required("load_path_rules", "path") {
		t.Fatal("load_path_rules.path is not required")
	}
	if property("git_log", "count", false)["maximum"] != float64(10000) {
		t.Fatalf("git_log.count schema=%#v", property("git_log", "count", false))
	}
	if property("directory_tree", "max_depth", false)["maximum"] != float64(128) {
		t.Fatalf("directory_tree.max_depth schema=%#v", property("directory_tree", "max_depth", false))
	}
	var processOutput map[string]any
	if err := json.Unmarshal(schemas["process_status"].OutputSchema, &processOutput); err != nil {
		t.Fatal(err)
	}
	processes := processOutput["properties"].(map[string]any)["processes"].(map[string]any)
	items := processes["items"].(map[string]any)
	if _, ok := items["properties"].(map[string]any)["execution_id"]; !ok {
		t.Fatal("process_status output schema missing execution_id")
	}
	expectedDescriptions := map[string][]string{
		"start_process":  {"lifecycle-driven", "do not poll process_status/process_output"},
		"process_status": {"diagnosis or recovery", "waiting loop"},
		"process_output": {"diagnosis or recovery", "waiting loop"},
		"stop_process":   {"cancel or intervene", "completion-wait mechanism"},
	}
	for name, expected := range expectedDescriptions {
		description := schemas[name].Description
		if len(description) > 320 {
			t.Fatalf("%s description is too verbose (%d bytes): %s", name, len(description), description)
		}
		for _, fragment := range expected {
			if !strings.Contains(description, fragment) {
				t.Fatalf("%s description missing %q: %s", name, fragment, description)
			}
		}
	}
}
