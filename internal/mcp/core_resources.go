package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	privateLive := func(maxBytes int64) ResourcePolicy {
		// Mutable owners do not currently expose a canonical change stream. Keep
		// their resource views uncached instead of inventing cache-local invalidation.
		return privateCached(maxBytes, 0)
	}
	template := func(path, name, title, description string, maxBytes int64) ResourceTemplateDescriptor {
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
			Policy:      privateLive(maxBytes),
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
			template(resourcePathProjectContext, "project-context", "Project Context", "Canonical bounded project context for one authorized workspace.", projectContextResourceMaxBytes),
			template(resourcePathInstructionSources, "workspace-instruction-sources", "Workspace Instruction Sources", "Sanitized instruction-source provenance for one authorized workspace.", sourceResourceMaxBytes),
			template(resourcePathPromptCatalog, "workspace-prompt-catalog", "Workspace Prompt Catalog", "Discoverable workspace prompt metadata without prompt bodies.", catalogResourceMaxBytes),
			template(resourcePathSkillCatalog, "workspace-skill-catalog", "Workspace Skill Catalog", "Resolved skill summaries for one authorized workspace.", catalogResourceMaxBytes),
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
		project, err := readProjectContextModel(ctx, runtime, workspaceID, false)
		if err != nil {
			return ResourceContent{}, err
		}
		prompts := promptCatalogFromSources(project.InstructionContext.Sources)
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
	policy, err := instructionpolicy.DefaultStore().Load()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	nativeRules, err := rules.DiscoverUser(home, policy)
	if err != nil {
		return nil, err
	}
	nativeSkills, err := skills.DiscoverUser(home, policy)
	if err != nil {
		return nil, err
	}

	sources := make([]map[string]any, 0, 5)
	if strings.TrimSpace(policy.Context) != "" {
		sources = append(sources, sourceResourceMap("codemcp", string(instructionpolicy.ResourceContext), "global-managed", 1, true, true))
	}
	managedRules := 0
	for _, rule := range policy.Rules {
		if rule.Enabled && strings.TrimSpace(rule.Content) != "" {
			managedRules++
		}
	}
	if managedRules > 0 {
		sources = append(sources, sourceResourceMap("codemcp", string(instructionpolicy.ResourceRules), "global-managed", managedRules, true, true))
	}
	if len(nativeRules) > 0 {
		sources = append(sources, sourceResourceMap(".cm", string(instructionpolicy.ResourceRules), "global-native", len(nativeRules), true, true))
	}
	nativeSkillCount, builtinSkillCount := 0, 0
	for _, skill := range nativeSkills {
		if skills.IsBuiltin(skill) {
			builtinSkillCount++
		} else {
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
	policy, err := instructionpolicy.DefaultStore().Load()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	values, err := skills.DiscoverUser(home, policy)
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

func promptCatalogFromSources(values []instructioncontext.SourceSnapshot) []map[string]any {
	seen := map[string]bool{}
	result := make([]map[string]any, 0)
	for _, source := range values {
		if source.Kind != "prompts" {
			continue
		}
		for _, path := range source.Paths {
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			key := source.Provider + "\x00" + source.Scope + "\x00" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, map[string]any{
				"name":      name,
				"source":    source.Provider,
				"scope":     source.Scope,
				"read_only": instructionSourceReadOnly(source.Scope),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		leftSource, _ := result[i]["source"].(string)
		rightSource, _ := result[j]["source"].(string)
		if leftSource != rightSource {
			return leftSource < rightSource
		}
		leftScope, _ := result[i]["scope"].(string)
		rightScope, _ := result[j]["scope"].(string)
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		leftName, _ := result[i]["name"].(string)
		rightName, _ := result[j]["name"].(string)
		return leftName < rightName
	})
	return result
}
