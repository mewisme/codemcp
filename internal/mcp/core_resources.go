package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/tools"
)

const (
	coreResourcesRegistrationID = "codemcp-core-resources"

	resourcePathStatus             = "status"
	resourcePathReadiness          = "readiness"
	resourcePathInstructionSources = "instruction-sources"
	resourcePathSkillCatalog       = "skills/catalog"
	resourcePathProjectContext     = "project-context"
	resourcePathPromptCatalog      = "prompts/catalog"

	statusResourceMaxBytes         int64 = 64 << 10
	readinessResourceMaxBytes      int64 = 8 << 10
	sourceResourceMaxBytes         int64 = 512 << 10
	catalogResourceMaxBytes        int64 = 1 << 20
	projectContextResourceMaxBytes       = maxResourceMaxBytes

	statusResourceTTLMs = 1000
)

func ensureCoreResources(registry *FeatureRegistry, runtime *tools.Runtime) error {
	if registry == nil || runtime == nil {
		return nil
	}
	registry.coreResourcesOnce.Do(func() {
		registry.coreResourcesErr = registry.Register(coreResourceRegistration(runtime))
	})
	return registry.coreResourcesErr
}

func coreResourceRegistration(runtime *tools.Runtime) FeatureRegistration {
	privateCached := func(maxBytes int64, ttlMs int) ResourcePolicy {
		return ResourcePolicy{
			MaxBytes: maxBytes,
			Cache: ResourceCachePolicy{
				TTLMs: ttlMs,
				Scope: ResourceCacheScopePrivate,
			},
		}
	}
	privateLive := func(maxBytes int64, subscribable bool) ResourcePolicy {
		// Mutable resource views remain uncached. Where an authoritative owner
		// publishes changes, the subscription policy opts into that shared stream.
		policy := privateCached(maxBytes, 0)
		policy.Subscription.Allowed = subscribable
		return policy
	}
	template := func(path, name, title, description string, maxBytes int64, subscribable bool) ResourceTemplateDescriptor {
		uri, err := WorkspaceResourceTemplate(path)
		if err != nil {
			panic(err)
		}
		return ResourceTemplateDescriptor{
			URITemplate: uri,
			Name:        name,
			Title:       title,
			Description: description,
			MIMEType:    "application/json",
			Policy:      privateLive(maxBytes, subscribable),
		}
	}
	global := func(path, name, title, description string, maxBytes int64, ttlMs int) ResourceDescriptor {
		uri, err := GlobalResourceURI(path)
		if err != nil {
			panic(err)
		}
		return ResourceDescriptor{
			URI:         uri,
			Name:        name,
			Title:       title,
			Description: description,
			MIMEType:    "application/json",
			Policy:      privateCached(maxBytes, ttlMs),
		}
	}

	return FeatureRegistration{
		ID:     coreResourcesRegistrationID,
		Family: FeatureResources,
		Resources: []ResourceDescriptor{
			global(resourcePathStatus, "status", "CodeMCP Status", "Running CodeMCP version and uptime status.", statusResourceMaxBytes, statusResourceTTLMs),
			global(resourcePathReadiness, "readiness", "CodeMCP Readiness", "Bounded readiness projection for the running CodeMCP runtime.", readinessResourceMaxBytes, statusResourceTTLMs),
			global(resourcePathInstructionSources, "global-instruction-sources", "Global Instruction Sources", "Sanitized global instruction-source provenance and policy visibility.", sourceResourceMaxBytes, 0),
			global(resourcePathSkillCatalog, "global-skill-catalog", "Global Skill Catalog", "Global CodeMCP-native and product-owned skill summaries.", catalogResourceMaxBytes, 0),
		},
		ResourceTemplates: []ResourceTemplateDescriptor{
			template(resourcePathProjectContext, "project-context", "Project Context", "Canonical bounded project context for one authorized workspace.", projectContextResourceMaxBytes, true),
			template(resourcePathInstructionSources, "workspace-instruction-sources", "Workspace Instruction Sources", "Sanitized instruction-source provenance for one authorized workspace.", sourceResourceMaxBytes, true),
			template(resourcePathPromptCatalog, "workspace-prompt-catalog", "Workspace Prompt Catalog", "Discoverable workspace prompt metadata without prompt bodies.", catalogResourceMaxBytes, false),
			template(resourcePathSkillCatalog, "workspace-skill-catalog", "Workspace Skill Catalog", "Resolved skill summaries for one authorized workspace.", catalogResourceMaxBytes, true),
		},
		ReadResource: coreResourceReader(runtime),
	}
}

func coreResourceReader(runtime *tools.Runtime) ResourceReadHandler {
	return func(ctx context.Context, request ResourceReadRequest) (ResourceContent, error) {
		switch request.Scope {
		case FeatureScopeGlobal:
			return readGlobalCoreResource(ctx, runtime, request.Path)
		case FeatureScopeWorkspace:
			return readWorkspaceCoreResource(ctx, runtime, request.WorkspaceID, request.Path)
		default:
			return ResourceContent{}, errors.New("unsupported resource scope")
		}
	}
}

func readGlobalCoreResource(ctx context.Context, runtime *tools.Runtime, path string) (ResourceContent, error) {
	switch path {
	case resourcePathStatus:
		value, err := readToolModel(ctx, runtime, "get_version", nil)
		if err != nil {
			return ResourceContent{}, err
		}
		return jsonResourceContent(value)
	case resourcePathReadiness:
		value, err := readToolModel(ctx, runtime, "get_version", nil)
		if err != nil {
			return ResourceContent{}, err
		}
		status, ok := value.(tools.VersionResult)
		if !ok {
			var decoded tools.VersionResult
			if err := decodeResourceModel(value, &decoded); err != nil {
				return ResourceContent{}, err
			}
			status = decoded
		}
		return jsonResourceContent(map[string]any{
			"ready":   true,
			"status":  "ready",
			"version": status.Version,
		})
	case resourcePathInstructionSources:
		value, err := globalInstructionSources()
		if err != nil {
			return ResourceContent{}, err
		}
		return jsonResourceContent(value)
	case resourcePathSkillCatalog:
		value, err := globalSkillCatalog()
		if err != nil {
			return ResourceContent{}, err
		}
		return jsonResourceContent(value)
	default:
		return ResourceContent{}, fmt.Errorf("unsupported global resource path %q", path)
	}
}

func readWorkspaceCoreResource(ctx context.Context, runtime *tools.Runtime, workspaceID, path string) (ResourceContent, error) {
	switch path {
	case resourcePathProjectContext:
		value, err := readToolModel(ctx, runtime, "project_context", map[string]any{"workspace_id": workspaceID})
		if err != nil {
			return ResourceContent{}, err
		}
		return jsonResourceContent(value)
	case resourcePathInstructionSources:
		project, err := readProjectContextModel(ctx, runtime, workspaceID, true)
		if err != nil {
			return ResourceContent{}, err
		}
		sources := sanitizeInstructionSources(project.InstructionContext.Sources)
		return jsonResourceContent(map[string]any{"sources": sources, "count": len(sources)})
	case resourcePathPromptCatalog:
		store, err := promptStoreFor(runtime, workspaceID)
		if err != nil {
			return ResourceContent{}, err
		}
		values, err := store.List()
		if err != nil {
			return ResourceContent{}, err
		}
		prompts := make([]map[string]any, 0, len(values))
		for _, prompt := range values {
			prompts = append(prompts, map[string]any{
				"name": prompt.Definition.Name, "description": prompt.Definition.Description,
				"source": ".cm", "scope": string(prompt.Scope) + "-native", "read_only": false,
			})
		}
		return jsonResourceContent(map[string]any{"prompts": prompts, "count": len(prompts)})
	case resourcePathSkillCatalog:
		value, err := readToolModel(ctx, runtime, "list_skills", map[string]any{"workspace_id": workspaceID})
		if err != nil {
			return ResourceContent{}, err
		}
		var catalog tools.SkillsListResult
		if typed, ok := value.(tools.SkillsListResult); ok {
			catalog = typed
		} else if err := decodeResourceModel(value, &catalog); err != nil {
			return ResourceContent{}, err
		}
		entries := sanitizeSkillCatalog(catalog.Skills)
		return jsonResourceContent(map[string]any{"skills": entries, "count": len(entries)})
	default:
		return ResourceContent{}, fmt.Errorf("unsupported workspace resource path %q", path)
	}
}

func readProjectContextModel(ctx context.Context, runtime *tools.Runtime, workspaceID string, includeSkills bool) (tools.ProjectContextResult, error) {
	value, err := readToolModel(ctx, runtime, "project_context", map[string]any{
		"workspace_id":   workspaceID,
		"include_git":    false,
		"include_skills": includeSkills,
	})
	if err != nil {
		return tools.ProjectContextResult{}, err
	}
	if typed, ok := value.(tools.ProjectContextResult); ok {
		return typed, nil
	}
	var decoded tools.ProjectContextResult
	if err := decodeResourceModel(value, &decoded); err != nil {
		return tools.ProjectContextResult{}, err
	}
	return decoded, nil
}

func readToolModel(ctx context.Context, runtime *tools.Runtime, name string, args map[string]any) (any, error) {
	if runtime == nil || runtime.Registry == nil {
		return nil, errors.New("tool runtime is unavailable")
	}
	result, err := runtime.Registry.Call(ctx, name, args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		message := "tool read model failed"
		if len(result.Content) > 0 && strings.TrimSpace(result.Content[0].Text) != "" {
			message = result.Content[0].Text
		}
		return nil, errors.New(message)
	}
	if result.StructuredContent != nil {
		return result.StructuredContent, nil
	}
	if len(result.Content) == 0 || strings.TrimSpace(result.Content[0].Text) == "" {
		return nil, errors.New("tool read model returned no content")
	}
	var value any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &value); err != nil {
		return nil, fmt.Errorf("decode tool read model: %w", err)
	}
	return value, nil
}

func jsonResourceContent(value any) (ResourceContent, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return ResourceContent{}, fmt.Errorf("encode resource content: %w", err)
	}
	text := string(data)
	return ResourceContent{MIMEType: "application/json", Text: &text}, nil
}

func decodeResourceModel(value any, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode canonical read model: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode canonical read model: %w", err)
	}
	return nil
}

func globalInstructionSources() (map[string]any, error) {
	policy := instructionpolicy.DefaultConfig()
	home, _ := os.UserHomeDir()
	allRules, err := rules.DiscoverUser(home, policy)
	if err != nil {
		return nil, err
	}
	allSkills, err := skills.DiscoverUser(home, policy)
	if err != nil {
		return nil, err
	}

	sources := make([]map[string]any, 0, 4)
	nativeRuleCount := 0
	for _, rule := range allRules {
		if rule.Source == ".cm" {
			nativeRuleCount++
		}
	}
	if nativeRuleCount > 0 {
		sources = append(sources, sourceResourceMap(".cm", string(instructionpolicy.ResourceRules), "global-native", nativeRuleCount, true, true))
	}
	nativeSkillCount, builtinSkillCount := 0, 0
	for _, skill := range allSkills {
		if skills.IsBuiltin(skill) {
			builtinSkillCount++
		} else if skill.Source == ".cm" {
			nativeSkillCount++
		}
	}
	if nativeSkillCount > 0 {
		sources = append(sources, sourceResourceMap(".cm", string(instructionpolicy.ResourceSkills), "global-native", nativeSkillCount, true, true))
	}
	if builtinSkillCount > 0 {
		sources = append(sources, sourceResourceMap(skills.BuiltinSource, string(instructionpolicy.ResourceSkills), "builtin", builtinSkillCount, true, true))
	}
	return map[string]any{"sources": sources, "count": len(sources)}, nil
}

func globalSkillCatalog() (map[string]any, error) {
	values, err := globalResolvedSkills()
	if err != nil {
		return nil, err
	}
	entries := sanitizeSkillCatalog(values)
	return map[string]any{"skills": entries, "count": len(entries)}, nil
}

func sanitizeInstructionSources(values []instructioncontext.SourceSnapshot) []map[string]any {
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		result = append(result, sanitizeInstructionSource(value))
	}
	return result
}

func sanitizeInstructionSource(value instructioncontext.SourceSnapshot) map[string]any {
	return sourceResourceMap(value.Provider, value.Kind, value.Scope, value.Count, value.Enabled, value.Loaded)
}

func sourceResourceMap(provider, kind, scope string, count int, enabled, loaded bool) map[string]any {
	return map[string]any{
		"provider":  provider,
		"kind":      kind,
		"scope":     scope,
		"count":     count,
		"enabled":   enabled,
		"loaded":    loaded,
		"read_only": instructionSourceReadOnly(scope),
	}
}

func instructionSourceReadOnly(scope string) bool {
	switch strings.TrimSpace(scope) {
	case "workspace-native", "global-native", "global-managed":
		return false
	default:
		return true
	}
}

func sanitizeSkillCatalog(values []skills.Skill) []map[string]any {
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		builtin := skills.IsBuiltin(value)
		result = append(result, map[string]any{
			"name":        value.Name,
			"description": value.Description,
			"source":      value.Source,
			"read_only":   builtin || value.Source != ".cm",
			"builtin":     builtin,
		})
	}
	return result
}
