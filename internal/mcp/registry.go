package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/tools"
)

const (
	defaultFeatureResultBytes = 1 << 20
	maxFeatureResultBytes     = 4 << 20
)

type FeatureFamily string

const (
	FeatureResources FeatureFamily = "resources"
	FeaturePrompts   FeatureFamily = "prompts"
	FeatureSkills    FeatureFamily = "skills"
)

type FeatureScope string

const (
	FeatureScopeGlobal    FeatureScope = "global"
	FeatureScopeWorkspace FeatureScope = "workspace"
)

type WorkspaceResolver func(map[string]any) (string, error)

type FeatureHandler func(context.Context, FeatureRequest) (map[string]any, error)

type FeatureRequest struct {
	Method      string
	Params      map[string]any
	WorkspaceID string
}

type FeatureMethod struct {
	Name             string
	Scope            FeatureScope
	MaxResultBytes   int
	Custom           bool
	ResolveWorkspace WorkspaceResolver
	Handler          FeatureHandler
}

type FeatureMethodDescriptor struct {
	Name           string       `json:"name"`
	Scope          FeatureScope `json:"scope"`
	MaxResultBytes int          `json:"max_result_bytes"`
	Custom         bool         `json:"custom"`
}

type FeatureCapabilities struct {
	Resources  *ResourcesCapability `json:"resources,omitempty"`
	Prompts    *PromptsCapability   `json:"prompts,omitempty"`
	Extensions map[string]any       `json:"extensions,omitempty"`
}

type FeatureRegistration struct {
	ID                string
	Family            FeatureFamily
	Resources         []ResourceDescriptor
	ResourceTemplates []ResourceTemplateDescriptor
	ReadResource      ResourceReadHandler
	Prompts           []PromptDescriptor
	Skills            []SkillDescriptor
	Capabilities      FeatureCapabilities
	Methods           []FeatureMethod
}

type FeatureSnapshot struct {
	Resources         []ResourceDescriptor
	ResourceTemplates []ResourceTemplateDescriptor
	Prompts           []PromptDescriptor
	Skills            []SkillDescriptor
	Capabilities      FeatureCapabilities
	Methods           []FeatureMethodDescriptor
}

type FeatureRegistry struct {
	mu            sync.RWMutex
	registrations map[string]FeatureRegistration
	methods       map[string]FeatureMethod
}

func NewFeatureRegistry() *FeatureRegistry {
	return &FeatureRegistry{
		registrations: map[string]FeatureRegistration{},
		methods:       map[string]FeatureMethod{},
	}
}

func (*FeatureRegistry) ProtocolFeatureProvider() {}

func FeatureRegistryForRuntime(runtime *tools.Runtime) *FeatureRegistry {
	if runtime == nil {
		return NewFeatureRegistry()
	}
	candidate := NewFeatureRegistry()
	provider := runtime.EnsureProtocolFeatures(candidate)
	registry, ok := provider.(*FeatureRegistry)
	if !ok {
		panic(fmt.Sprintf("unexpected protocol feature provider %T", provider))
	}
	return registry
}

func (r *FeatureRegistry) Register(registration FeatureRegistration) error {
	if r == nil {
		return errors.New("feature registry is unavailable")
	}
	prepared, err := normalizeFeatureRegistration(registration)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.registrations[prepared.ID]; exists {
		return fmt.Errorf("feature %q is already registered", prepared.ID)
	}
	if err := validateFeatureCollisions(r.registrations, prepared); err != nil {
		return err
	}
	r.registrations[prepared.ID] = prepared
	for _, method := range prepared.Methods {
		r.methods[method.Name] = method
	}
	return nil
}

func (r *FeatureRegistry) Snapshot() FeatureSnapshot {
	if r == nil {
		return FeatureSnapshot{}
	}
	r.mu.RLock()
	registrations := make([]FeatureRegistration, 0, len(r.registrations))
	for _, registration := range r.registrations {
		registrations = append(registrations, cloneFeatureRegistration(registration))
	}
	r.mu.RUnlock()
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].ID < registrations[j].ID })

	snapshot := FeatureSnapshot{Capabilities: FeatureCapabilities{Extensions: map[string]any{}}}
	for _, registration := range registrations {
		snapshot.Resources = append(snapshot.Resources, registration.Resources...)
		snapshot.ResourceTemplates = append(snapshot.ResourceTemplates, registration.ResourceTemplates...)
		snapshot.Prompts = append(snapshot.Prompts, registration.Prompts...)
		snapshot.Skills = append(snapshot.Skills, registration.Skills...)
		snapshot.Capabilities = mergeFeatureCapabilities(snapshot.Capabilities, registration.Capabilities)
		for _, method := range registration.Methods {
			snapshot.Methods = append(snapshot.Methods, FeatureMethodDescriptor{
				Name: method.Name, Scope: method.Scope, MaxResultBytes: method.MaxResultBytes, Custom: method.Custom,
			})
		}
	}
	sort.Slice(snapshot.Resources, func(i, j int) bool { return snapshot.Resources[i].URI < snapshot.Resources[j].URI })
	sort.Slice(snapshot.ResourceTemplates, func(i, j int) bool {
		return snapshot.ResourceTemplates[i].URITemplate < snapshot.ResourceTemplates[j].URITemplate
	})
	sort.Slice(snapshot.Prompts, func(i, j int) bool { return snapshot.Prompts[i].Name < snapshot.Prompts[j].Name })
	sort.Slice(snapshot.Skills, func(i, j int) bool { return snapshot.Skills[i].Name < snapshot.Skills[j].Name })
	sort.Slice(snapshot.Methods, func(i, j int) bool { return snapshot.Methods[i].Name < snapshot.Methods[j].Name })
	if len(snapshot.Capabilities.Extensions) == 0 {
		snapshot.Capabilities.Extensions = nil
	}
	return snapshot
}

func (r *FeatureRegistry) method(name string) (FeatureMethod, bool) {
	if r == nil {
		return FeatureMethod{}, false
	}
	r.mu.RLock()
	method, ok := r.methods[strings.TrimSpace(name)]
	r.mu.RUnlock()
	return method, ok
}

func (r *FeatureRegistry) SupportsMethod(name string) bool {
	switch strings.TrimSpace(name) {
	case ResourcesListMethod, ResourcesReadMethod, ResourceTemplatesListMethod:
		return r.supportsResourceMethods()
	}
	_, ok := r.method(name)
	return ok
}

func normalizeFeatureRegistration(registration FeatureRegistration) (FeatureRegistration, error) {
	registration.ID = strings.TrimSpace(registration.ID)
	if registration.ID == "" {
		return FeatureRegistration{}, errors.New("feature id is required")
	}
	switch registration.Family {
	case FeatureResources, FeaturePrompts, FeatureSkills:
	default:
		return FeatureRegistration{}, fmt.Errorf("feature %q has unsupported family %q", registration.ID, registration.Family)
	}
	if err := normalizeResourceRegistration(&registration); err != nil {
		return FeatureRegistration{}, fmt.Errorf("feature %q resources: %w", registration.ID, err)
	}
	if _, err := json.Marshal(registration.Capabilities.Extensions); err != nil {
		return FeatureRegistration{}, fmt.Errorf("feature %q capability settings are not JSON serializable: %w", registration.ID, err)
	}
	for _, skill := range registration.Skills {
		if _, err := json.Marshal(skill.Extensions); err != nil {
			return FeatureRegistration{}, fmt.Errorf("feature %q skill %q extensions are not JSON serializable: %w", registration.ID, skill.Name, err)
		}
	}
	registration.Resources = cloneResourceDescriptors(registration.Resources)
	registration.ResourceTemplates = cloneResourceTemplateDescriptors(registration.ResourceTemplates)
	registration.Prompts = clonePromptDescriptors(registration.Prompts)
	registration.Skills = cloneSkillDescriptors(registration.Skills)
	registration.Capabilities = cloneFeatureCapabilities(registration.Capabilities)

	seenMethods := map[string]bool{}
	for index := range registration.Methods {
		method := registration.Methods[index]
		method.Name = strings.TrimSpace(method.Name)
		if method.Name == "" {
			return FeatureRegistration{}, fmt.Errorf("feature %q method name is required", registration.ID)
		}
		if seenMethods[method.Name] {
			return FeatureRegistration{}, fmt.Errorf("feature %q repeats method %q", registration.ID, method.Name)
		}
		seenMethods[method.Name] = true
		switch method.Scope {
		case FeatureScopeGlobal, FeatureScopeWorkspace:
		default:
			return FeatureRegistration{}, fmt.Errorf("feature %q method %q has unsupported scope %q", registration.ID, method.Name, method.Scope)
		}
		if method.Handler == nil {
			return FeatureRegistration{}, fmt.Errorf("feature %q method %q handler is required", registration.ID, method.Name)
		}
		if method.MaxResultBytes == 0 {
			method.MaxResultBytes = defaultFeatureResultBytes
		}
		if method.MaxResultBytes < 1 || method.MaxResultBytes > maxFeatureResultBytes {
			return FeatureRegistration{}, fmt.Errorf("feature %q method %q result limit must be between 1 and %d bytes", registration.ID, method.Name, maxFeatureResultBytes)
		}
		registration.Methods[index] = method
	}
	return registration, nil
}

func validateFeatureCollisions(existing map[string]FeatureRegistration, candidate FeatureRegistration) error {
	resources := map[string]string{}
	resourceTemplates := map[string]string{}
	prompts := map[string]string{}
	skills := map[string]string{}
	extensions := map[string]string{}
	methods := map[string]string{}
	collect := func(registration FeatureRegistration) error {
		for _, resource := range registration.Resources {
			key := strings.TrimSpace(resource.URI)
			if key == "" {
				return fmt.Errorf("feature %q resource URI is required", registration.ID)
			}
			if owner := resources[key]; owner != "" {
				return fmt.Errorf("resource %q is owned by both %q and %q", key, owner, registration.ID)
			}
			resources[key] = registration.ID
		}
		for _, resourceTemplate := range registration.ResourceTemplates {
			key := strings.TrimSpace(resourceTemplate.URITemplate)
			if key == "" {
				return fmt.Errorf("feature %q resource template URI is required", registration.ID)
			}
			if owner := resourceTemplates[key]; owner != "" {
				return fmt.Errorf("resource template %q is owned by both %q and %q", key, owner, registration.ID)
			}
			resourceTemplates[key] = registration.ID
		}
		for _, prompt := range registration.Prompts {
			key := strings.TrimSpace(prompt.Name)
			if key == "" {
				return fmt.Errorf("feature %q prompt name is required", registration.ID)
			}
			if owner := prompts[key]; owner != "" {
				return fmt.Errorf("prompt %q is owned by both %q and %q", key, owner, registration.ID)
			}
			prompts[key] = registration.ID
		}
		for _, skill := range registration.Skills {
			key := strings.TrimSpace(skill.Name)
			if key == "" {
				return fmt.Errorf("feature %q skill name is required", registration.ID)
			}
			if owner := skills[key]; owner != "" {
				return fmt.Errorf("skill %q is owned by both %q and %q", key, owner, registration.ID)
			}
			skills[key] = registration.ID
		}
		for extension := range registration.Capabilities.Extensions {
			key := strings.TrimSpace(extension)
			if key == "" {
				return fmt.Errorf("feature %q extension id is required", registration.ID)
			}
			if owner := extensions[key]; owner != "" {
				return fmt.Errorf("extension %q is owned by both %q and %q", key, owner, registration.ID)
			}
			extensions[key] = registration.ID
		}
		for _, method := range registration.Methods {
			if owner := methods[method.Name]; owner != "" {
				return fmt.Errorf("method %q is owned by both %q and %q", method.Name, owner, registration.ID)
			}
			methods[method.Name] = registration.ID
		}
		return nil
	}
	ids := make([]string, 0, len(existing))
	for id := range existing {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := collect(existing[id]); err != nil {
			return err
		}
	}
	return collect(candidate)
}

func mergeFeatureCapabilities(left, right FeatureCapabilities) FeatureCapabilities {
	out := cloneFeatureCapabilities(left)
	if right.Resources != nil {
		if out.Resources == nil {
			out.Resources = &ResourcesCapability{}
		}
		out.Resources.ListChanged = out.Resources.ListChanged || right.Resources.ListChanged
		out.Resources.Subscribe = out.Resources.Subscribe || right.Resources.Subscribe
	}
	if right.Prompts != nil {
		if out.Prompts == nil {
			out.Prompts = &PromptsCapability{}
		}
		out.Prompts.ListChanged = out.Prompts.ListChanged || right.Prompts.ListChanged
	}
	if len(right.Extensions) > 0 {
		if out.Extensions == nil {
			out.Extensions = map[string]any{}
		}
		for key, value := range right.Extensions {
			out.Extensions[key] = cloneAnyValue(value)
		}
	}
	return out
}

func cloneFeatureRegistration(value FeatureRegistration) FeatureRegistration {
	value.Resources = cloneResourceDescriptors(value.Resources)
	value.ResourceTemplates = cloneResourceTemplateDescriptors(value.ResourceTemplates)
	value.Prompts = clonePromptDescriptors(value.Prompts)
	value.Skills = cloneSkillDescriptors(value.Skills)
	value.Capabilities = cloneFeatureCapabilities(value.Capabilities)
	value.Methods = append([]FeatureMethod(nil), value.Methods...)
	return value
}

func cloneFeatureCapabilities(value FeatureCapabilities) FeatureCapabilities {
	out := FeatureCapabilities{Extensions: cloneAnyMap(value.Extensions)}
	if value.Resources != nil {
		copy := *value.Resources
		out.Resources = &copy
	}
	if value.Prompts != nil {
		copy := *value.Prompts
		out.Prompts = &copy
	}
	return out
}

func cloneResourceDescriptors(values []ResourceDescriptor) []ResourceDescriptor {
	if len(values) == 0 {
		return nil
	}
	return append([]ResourceDescriptor(nil), values...)
}

func clonePromptDescriptors(values []PromptDescriptor) []PromptDescriptor {
	if len(values) == 0 {
		return nil
	}
	return append([]PromptDescriptor(nil), values...)
}

func cloneSkillDescriptors(values []SkillDescriptor) []SkillDescriptor {
	if len(values) == 0 {
		return nil
	}
	out := make([]SkillDescriptor, len(values))
	for index, value := range values {
		out[index] = value
		out[index].Extensions = cloneAnyMap(value.Extensions)
	}
	return out
}

func cloneAnyValue(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil {
		return nil
	}
	return out
}
