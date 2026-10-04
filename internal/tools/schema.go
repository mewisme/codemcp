package tools

import (
	"encoding/json"
	"fmt"
)

type Schema struct {
	Name         string              `json:"name"`
	Title        string              `json:"title,omitempty"`
	Description  string              `json:"description,omitempty"`
	InputSchema  json.RawMessage     `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage     `json:"outputSchema,omitempty"`
	Annotations  map[string]any      `json:"annotations,omitempty"`
	Capability   *CapabilityMetadata `json:"capability,omitempty"`
	Approval     *ApprovalMetadata   `json:"approval,omitempty"`
}

func schemaHasWorkspaceID(schema Schema) (bool, error) {
	return schemaHasInputProperty(schema, "workspace_id")
}

func schemaHasInputProperty(schema Schema, key string) (bool, error) {
	if len(schema.InputSchema) == 0 {
		return false, nil
	}
	var input struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema.InputSchema, &input); err != nil {
		return false, fmt.Errorf("decode tool %q input schema: %w", schema.Name, err)
	}
	_, ok := input.Properties[key]
	return ok, nil
}
