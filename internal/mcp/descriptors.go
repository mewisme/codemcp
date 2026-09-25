package mcp

import (
	"encoding/json"
	"strings"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/version"
)

// ProtocolDescriptors is the canonical MCP-facing description of CodeMCP.
// Runtime authorization and approval decisions remain owned by the tools
// runtime; descriptors only describe protocol-visible operation truth.
type ProtocolDescriptors struct {
	Server           ServerDescriptor     `json:"server"`
	Capabilities     Capabilities         `json:"capabilities"`
	Tools            []ToolDescriptor     `json:"tools,omitempty"`
	Resources        []ResourceDescriptor `json:"resources,omitempty"`
	Prompts          []PromptDescriptor   `json:"prompts,omitempty"`
	Skills           []SkillDescriptor    `json:"skills,omitempty"`
	Results          ResultDescriptor     `json:"results"`
	Errors           ErrorDescriptor      `json:"errors"`
	AuthRequirements []AuthRequirement    `json:"auth_requirements,omitempty"`
}

type ServerDescriptor struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ToolDescriptor struct {
	Name         string                 `json:"name"`
	Title        string                 `json:"title,omitempty"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	OutputSchema json.RawMessage        `json:"output_schema,omitempty"`
	Effects      ToolEffects            `json:"effects"`
	Security     ToolSecurityDescriptor `json:"security"`
}

// ToolEffects contains semantic MCP hints only. It must not be interpreted as
// a CodeMCP authorization or approval policy.
type ToolEffects struct {
	ReadOnly    bool `json:"read_only"`
	Destructive bool `json:"destructive"`
	Idempotent  bool `json:"idempotent"`
	OpenWorld   bool `json:"open_world"`
}

// ToolSecurityDescriptor identifies the authority that decides security for a
// tool without attempting to predict whether a particular invocation needs an
// approval. That decision remains input-sensitive runtime truth.
type ToolSecurityDescriptor struct {
	ApprovalAuthority string            `json:"approval_authority"`
	AuthRequirements  []AuthRequirement `json:"auth_requirements,omitempty"`
}

type AuthRequirement struct {
	Scheme string   `json:"scheme"`
	Scopes []string `json:"scopes,omitempty"`
}

type ResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
}

type PromptDescriptor struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type SkillDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Extensions  map[string]any `json:"extensions,omitempty"`
}

type ResultDescriptor struct {
	SupportsStructuredContent bool `json:"supports_structured_content"`
	SupportsInputRequired     bool `json:"supports_input_required"`
}

type ErrorDescriptor struct {
	JSONRPC bool `json:"json_rpc"`
	Tool    bool `json:"tool"`
}

const runtimeApprovalAuthority = "codemcp-runtime"
const bearerAuthScheme = "oauth2"

func DescribeProtocol(schemas []tools.Schema, authRequirements ...AuthRequirement) ProtocolDescriptors {
	descriptor := ProtocolDescriptors{
		Server:           ServerDescriptor{Name: "codemcp", Version: version.Version},
		Capabilities:     DefaultCapabilities(),
		Results:          ResultDescriptor{SupportsStructuredContent: true, SupportsInputRequired: true},
		Errors:           ErrorDescriptor{JSONRPC: true, Tool: true},
		AuthRequirements: cloneAuthRequirements(authRequirements),
	}
	for _, schema := range schemas {
		tool := DescribeTool(schema)
		tool.Security.AuthRequirements = cloneAuthRequirements(authRequirements)
		descriptor.Tools = append(descriptor.Tools, tool)
	}
	return descriptor
}

func BearerAuthRequirement(scopes ...string) AuthRequirement {
	return AuthRequirement{Scheme: bearerAuthScheme, Scopes: append([]string(nil), scopes...)}
}

func cloneAuthRequirements(requirements []AuthRequirement) []AuthRequirement {
	if len(requirements) == 0 {
		return nil
	}
	out := make([]AuthRequirement, len(requirements))
	for index, requirement := range requirements {
		out[index] = AuthRequirement{Scheme: strings.TrimSpace(requirement.Scheme), Scopes: append([]string(nil), requirement.Scopes...)}
	}
	return out
}

func DescribeTool(schema tools.Schema) ToolDescriptor {
	return ToolDescriptor{
		Name:         strings.TrimSpace(schema.Name),
		Title:        strings.TrimSpace(schema.Title),
		Description:  strings.TrimSpace(schema.Description),
		InputSchema:  cloneRawMessage(schema.InputSchema),
		OutputSchema: cloneRawMessage(schema.OutputSchema),
		Effects:      effectsFromAnnotations(schema.Annotations),
		Security:     ToolSecurityDescriptor{ApprovalAuthority: runtimeApprovalAuthority},
	}
}

func effectsFromAnnotations(annotations map[string]any) ToolEffects {
	boolValue := func(key string, fallback bool) bool {
		value, ok := annotations[key].(bool)
		if !ok {
			return fallback
		}
		return value
	}
	return ToolEffects{
		ReadOnly:    boolValue("readOnlyHint", false),
		Destructive: boolValue("destructiveHint", true),
		Idempotent:  boolValue("idempotentHint", false),
		OpenWorld:   boolValue("openWorldHint", true),
	}
}

func (e ToolEffects) annotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    e.ReadOnly,
		"destructiveHint": e.Destructive,
		"idempotentHint":  e.Idempotent,
		"openWorldHint":   e.OpenWorld,
	}
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
