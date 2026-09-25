package mcp

import "go.mewis.me/codemcp/internal/tools"

type ToolCatalog struct{ Registry *tools.Registry }

func NewToolCatalog(registry *tools.Registry) *ToolCatalog { return &ToolCatalog{Registry: registry} }

func (c *ToolCatalog) List() []tools.Schema {
	return c.Registry.ListSchemas()
}

func (c *ToolCatalog) Descriptors() []ToolDescriptor {
	if c == nil || c.Registry == nil {
		return nil
	}
	return DescribeProtocol(filterHeaderSafeTools(c.Registry.ListSchemas())).Tools
}
