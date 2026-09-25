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

// Profile controls presentation-only wire representation. Operation identity,
// schemas, effects, auth requirements and approval authority are composed by
// the core projector and cannot be overridden by a profile.
type Profile interface {
	ID() ProfileID
	ToolRepresentation(ToolDescriptor) ToolRepresentation
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

func ProjectTool(profile Profile, descriptor ToolDescriptor, options ToolProjectionOptions) (ProjectedTool, error) {
	if profile == nil {
		profile = BaseProfile()
	}
	if strings.TrimSpace(descriptor.Name) == "" {
		return ProjectedTool{}, errors.New("tool descriptor name is required")
	}
	input, err := projectInputSchema(descriptor.InputSchema, options.BoundWorkspace)
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
		projected, err := ProjectTool(profile, descriptor, options)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	return result, nil
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
	if descriptor.Capabilities.Tools.ListChanged {
		capabilities.Tools = &sdkmcp.ToolCapabilities{ListChanged: true}
	}
	return implementation, &sdkmcp.ServerOptions{
		Capabilities: capabilities,
		Instructions: ProjectServerInstructions(profile),
	}
}

func projectInputSchema(input json.RawMessage, boundWorkspace bool) (json.RawMessage, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{"type":"object"}`)
	}
	object, err := decodeSchemaObject(input)
	if err != nil {
		return nil, err
	}
	if boundWorkspace {
		if properties, ok := object["properties"].(map[string]any); ok {
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
