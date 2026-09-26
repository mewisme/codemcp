package mcp

type Capabilities struct {
	Tools      ToolsCapability      `json:"tools"`
	Resources  *ResourcesCapability `json:"resources,omitempty"`
	Prompts    *PromptsCapability   `json:"prompts,omitempty"`
	Extensions map[string]any       `json:"extensions,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type ResourcesCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
	Subscribe   bool `json:"subscribe,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{
		Tools:      ToolsCapability{ListChanged: true},
		Extensions: map[string]any{TasksExtensionID: map[string]any{}},
	}
}
