package mcp

type Capabilities struct {
	Tools       ToolsCapability         `json:"tools"`
	Resources   *ResourcesCapability    `json:"resources,omitempty"`
	Prompts     *PromptsCapability      `json:"prompts,omitempty"`
	Completions *CompletionCapabilities `json:"completions,omitempty"`
	Extensions  map[string]any          `json:"extensions,omitempty"`
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

type CompletionCapabilities struct{}

func DefaultCapabilities() Capabilities {
	return Capabilities{
		Tools:       ToolsCapability{ListChanged: true},
		Completions: &CompletionCapabilities{},
		Extensions:  map[string]any{TasksExtensionID: map[string]any{}},
	}
}
