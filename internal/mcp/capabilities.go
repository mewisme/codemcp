package mcp

type Capabilities struct {
	Tools      ToolsCapability `json:"tools"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{
		Tools:      ToolsCapability{ListChanged: true},
		Extensions: map[string]any{TasksExtensionID: map[string]any{}},
	}
}
