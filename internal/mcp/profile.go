package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/tools"
)

type ProfileID string

const BaseProfileID ProfileID = "base"

// Profile controls wire presentation and may optionally filter which canonical
// tools are exposed. Operation identity, schemas, effects, auth requirements
// and approval authority are composed by the core projector and cannot be
// rewritten by a profile.
type Profile interface {
	ID() ProfileID
	ToolRepresentation(ToolDescriptor) ToolRepresentation
}

type ToolVisibilityProfile interface {
	IncludeTool(ToolDescriptor) bool
}

type ToolRepresentation struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type InstructionPresentation struct {
	Heading string
}

type instructionPresentationProfile interface {
	InstructionPresentation() InstructionPresentation
}

type ClientAuthenticationProfile interface {
	ClientCertificateAuthentication() bool
}

type baseProfile struct{}

func BaseProfile() Profile { return baseProfile{} }

func (baseProfile) ID() ProfileID { return BaseProfileID }

func (baseProfile) ToolRepresentation(tool ToolDescriptor) ToolRepresentation {
	return ToolRepresentation{Title: tool.Title, Description: tool.Description}
}

func (baseProfile) InstructionPresentation() InstructionPresentation {
	return InstructionPresentation{}
}

func (baseProfile) ClientCertificateAuthentication() bool { return false }

func (baseProfile) BackgroundCapabilities() BackgroundCapabilities {
	return BackgroundCapabilities{
		Execution:       true,
		TaskObservation: true,
	}
}

func RequiresClientCertificateAuthentication(profile Profile) bool {
	provider, ok := profile.(ClientAuthenticationProfile)
	return ok && provider.ClientCertificateAuthentication()
}

type ToolProjectionOptions struct {
	BoundWorkspace bool
}

type ProjectedTool struct {
	Meta            map[string]any   `json:"_meta,omitempty"`
	Annotations     map[string]any   `json:"annotations,omitempty"`
	Description     string           `json:"description,omitempty"`
	InputSchema     json.RawMessage  `json:"inputSchema"`
	Name            string           `json:"name"`
	OutputSchema    json.RawMessage  `json:"outputSchema,omitempty"`
	SecuritySchemes []map[string]any `json:"securitySchemes,omitempty"`
	Title           string           `json:"title,omitempty"`
}

type ProjectedFeatures struct {
	Resources         []ResourceDescriptor         `json:"resources,omitempty"`
	ResourceTemplates []ResourceTemplateDescriptor `json:"resource_templates,omitempty"`
	Prompts           []PromptDescriptor           `json:"prompts,omitempty"`
	Skills            []SkillDescriptor            `json:"skills,omitempty"`
}

func ProjectFeatures(_ Profile, descriptor ProtocolDescriptors) ProjectedFeatures {
	return ProjectedFeatures{
		Resources:         cloneResourceDescriptors(descriptor.Resources),
		ResourceTemplates: cloneResourceTemplateDescriptors(descriptor.ResourceTemplates),
		Prompts:           clonePromptDescriptors(descriptor.Prompts),
		Skills:            cloneSkillDescriptors(descriptor.Skills),
	}
}

func ProjectTool(profile Profile, descriptor ToolDescriptor, options ToolProjectionOptions) (ProjectedTool, error) {
	if profile == nil {
		profile = BaseProfile()
	}
	if strings.TrimSpace(descriptor.Name) == "" {
		return ProjectedTool{}, errors.New("tool descriptor name is required")
	}
	input, err := projectInputSchema(descriptor.InputSchema, options.BoundWorkspace, descriptor.Approval)
	if err != nil {
		return ProjectedTool{}, fmt.Errorf("project tool %q input schema: %w", descriptor.Name, err)
	}
	if err := tools.ValidateSchemaDocument(input, true); err != nil {
		return ProjectedTool{}, fmt.Errorf("project tool %q input schema: %w", descriptor.Name, err)
	}
	if _, err := toolHeaderSpecs(input); err != nil {
		return ProjectedTool{}, fmt.Errorf("project tool %q header schema: %w", descriptor.Name, err)
	}
	if len(descriptor.OutputSchema) > 0 {
		if err := tools.ValidateSchemaDocument(descriptor.OutputSchema, false); err != nil {
			return ProjectedTool{}, fmt.Errorf("project tool %q output schema: %w", descriptor.Name, err)
		}
	}
	representation := profile.ToolRepresentation(descriptor)
	title := strings.TrimSpace(representation.Title)
	if title == "" {
		title = descriptor.Name
	}
	description := strings.TrimSpace(representation.Description)
	if description == "" {
		description = title
	}
	return ProjectedTool{
		Meta:            cloneAnyMap(representation.Meta),
		Annotations:     descriptor.Effects.annotations(),
		Description:     description,
		InputSchema:     input,
		Name:            descriptor.Name,
		OutputSchema:    cloneRawMessage(descriptor.OutputSchema),
		SecuritySchemes: projectSecuritySchemes(descriptor.Security.AuthRequirements),
		Title:           title,
	}, nil
}

func projectSecuritySchemes(requirements []AuthRequirement) []map[string]any {
	if len(requirements) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(requirements))
	for _, requirement := range requirements {
		switch strings.ToLower(strings.TrimSpace(requirement.Scheme)) {
		case bearerAuthScheme:
			result = append(result, map[string]any{"type": "oauth2", "scopes": append([]string(nil), requirement.Scopes...)})
		}
	}
	return result
}

func ProjectTools(profile Profile, descriptors []ToolDescriptor, options ToolProjectionOptions) ([]ProjectedTool, error) {
	result := make([]ProjectedTool, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if !ProfileIncludesTool(profile, descriptor) {
			continue
		}
		projected, err := ProjectTool(profile, descriptor, options)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	return result, nil
}

func ProfileIncludesTool(profile Profile, descriptor ToolDescriptor) bool {
	if profile == nil {
		profile = BaseProfile()
	}
	filter, ok := profile.(ToolVisibilityProfile)
	return !ok || filter.IncludeTool(descriptor)
}

func EffectiveToolSchemas(profile Profile, schemas []tools.Schema) []tools.Schema {
	result := make([]tools.Schema, 0, len(schemas))
	for _, schema := range schemas {
		if ProfileIncludesTool(profile, DescribeTool(schema)) {
			result = append(result, schema)
		}
	}
	return result
}

func ToolProfileName(profile Profile) string {
	if profile == nil || profile.ID() == BaseProfileID {
		return "full"
	}
	if value := strings.TrimSpace(string(profile.ID())); value != "" {
		return value
	}
	return "full"
}

func ProjectSDKTool(profile Profile, descriptor ToolDescriptor, options ToolProjectionOptions) (*sdkmcp.Tool, error) {
	projected, err := ProjectTool(profile, descriptor, options)
	if err != nil {
		return nil, err
	}
	input, err := decodeSchemaObject(projected.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("decode input schema: %w", err)
	}
	var output any
	if len(projected.OutputSchema) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(projected.OutputSchema))
		decoder.UseNumber()
		if err := decoder.Decode(&output); err != nil {
			return nil, fmt.Errorf("decode output schema: %w", err)
		}
	}
	annotations := &sdkmcp.ToolAnnotations{}
	data, err := json.Marshal(projected.Annotations)
	if err != nil {
		return nil, fmt.Errorf("encode annotations: %w", err)
	}
	if err := json.Unmarshal(data, annotations); err != nil {
		return nil, fmt.Errorf("decode annotations: %w", err)
	}
	meta := cloneAnyMap(projected.Meta)
	if len(projected.SecuritySchemes) > 0 {
		meta["securitySchemes"] = projected.SecuritySchemes
	}
	return &sdkmcp.Tool{
		Meta:         sdkmcp.Meta(meta),
		Annotations:  annotations,
		Description:  projected.Description,
		InputSchema:  input,
		Name:         projected.Name,
		OutputSchema: output,
		Title:        projected.Title,
	}, nil
}

func ProjectServerInstructions(profile Profile) string {
	if profile == nil {
		profile = BaseProfile()
	}
	presentation := InstructionPresentation{}
	if provider, ok := profile.(instructionPresentationProfile); ok {
		presentation = provider.InstructionPresentation()
	}
	return instructioncontext.RenderServerInstructions(
		instructioncontext.CanonicalServerInstructionModel(),
		instructioncontext.ServerInstructionRenderOptions{Heading: presentation.Heading},
	)
}

func ProjectSDKServer(profile Profile, descriptor ProtocolDescriptors) (*sdkmcp.Implementation, *sdkmcp.ServerOptions) {
	implementation := &sdkmcp.Implementation{Name: descriptor.Server.Name, Version: descriptor.Server.Version}
	capabilities := &sdkmcp.ServerCapabilities{}
	projected := ProjectCapabilities(profile, descriptor.Capabilities, true)
	if len(projected.Extensions) > 0 {
		capabilities.Extensions = projected.Extensions
	}
	if projected.Tools.ListChanged {
		capabilities.Tools = &sdkmcp.ToolCapabilities{ListChanged: true}
	}
	if projected.Resources != nil {
		capabilities.Resources = &sdkmcp.ResourceCapabilities{
			ListChanged: projected.Resources.ListChanged,
			Subscribe:   projected.Resources.Subscribe,
		}
	}
	if projected.Completions != nil {
		capabilities.Completions = &sdkmcp.CompletionCapabilities{}
	}
	if projected.Prompts != nil {
		capabilities.Prompts = &sdkmcp.PromptCapabilities{ListChanged: projected.Prompts.ListChanged}
	}
	return implementation, &sdkmcp.ServerOptions{
		Capabilities: capabilities,
		Instructions: ProjectServerInstructions(profile),
	}
}

func projectInputSchema(input json.RawMessage, boundWorkspace bool, approval *tools.ApprovalMetadata) (json.RawMessage, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{"type":"object"}`)
	}
	object, err := decodeSchemaObject(input)
	if err != nil {
		return nil, err
	}
	properties, _ := object["properties"].(map[string]any)
	if properties != nil {
		if _, reserved := properties[tools.InlineApprovalArgumentKey]; reserved {
			return nil, fmt.Errorf("input schema property %q is reserved for runtime approval metadata", tools.InlineApprovalArgumentKey)
		}
	}
	if boundWorkspace {
		if properties != nil {
			delete(properties, "workspace_id")
		}
		if required, ok := object["required"].([]any); ok {
			filtered := make([]any, 0, len(required))
			for _, item := range required {
				if value, ok := item.(string); !ok || value != "workspace_id" {
					filtered = append(filtered, item)
				}
			}
			if len(filtered) == 0 {
				delete(object, "required")
			} else {
				object["required"] = filtered
			}
		}
	}
	if approval != nil && approval.Inline {
		if properties == nil {
			properties = map[string]any{}
			object["properties"] = properties
		}
		properties[tools.InlineApprovalArgumentKey] = map[string]any{
			"type":        "object",
			"description": "Request local human approval for the exact previously challenged invocation. This is runtime metadata, not a business argument.",
			"properties": map[string]any{
				tools.InlineApprovalChallengeID: map[string]any{
					"type":      "string",
					"minLength": 1,
				},
				tools.InlineApprovalTitle: map[string]any{
					"type":        "string",
					"minLength":   1,
					"maxLength":   tools.InlineApprovalTitleMaxLen,
					"description": "Concise human-readable summary of what the guarded action will do. Do not copy raw commands, flags, arguments, tokens, secrets, or IDs.",
				},
			},
			"required":             []any{tools.InlineApprovalChallengeID, tools.InlineApprovalTitle},
			"additionalProperties": false,
		}
	}
	return json.Marshal(object)
}

func decodeSchemaObject(raw json.RawMessage) (map[string]any, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if kind, _ := object["type"].(string); kind != "object" {
		return nil, errors.New("input schema must have type object")
	}
	return object, nil
}
