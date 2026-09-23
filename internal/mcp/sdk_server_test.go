package mcp

import (
	"encoding/json"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

func TestProjectBoundWorkspaceSchemaRemovesWorkspaceArgument(t *testing.T) {
	schema := tools.Schema{Name: "probe", InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"}},"required":["workspace_id","path"],"additionalProperties":false}`)}
	projected := projectBoundWorkspaceSchema(schema)
	var input map[string]any
	if err := json.Unmarshal(projected.InputSchema, &input); err != nil {
		t.Fatal(err)
	}
	properties := input["properties"].(map[string]any)
	if _, ok := properties["workspace_id"]; ok {
		t.Fatalf("workspace_id still exposed: %s", projected.InputSchema)
	}
	required := input["required"].([]any)
	if len(required) != 1 || required[0] != "path" {
		t.Fatalf("required=%#v", required)
	}
}
