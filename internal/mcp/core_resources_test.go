package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestCoreResourcePoliciesArePrivateAndConservative(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))

	runtime := tools.NewRuntime()
	registry := FeatureRegistryForRuntime(runtime)
	if FeatureRegistryForRuntime(runtime) != registry {
		t.Fatal("core resources created a second feature registry")
	}
	snapshot := registry.Snapshot()
	if len(snapshot.Resources) != 4 || len(snapshot.ResourceTemplates) != 4 {
		t.Fatalf("core resources=%#v templates=%#v", snapshot.Resources, snapshot.ResourceTemplates)
	}
	if snapshot.Capabilities.Resources == nil || !snapshot.Capabilities.Resources.Subscribe || !snapshot.Capabilities.Resources.ListChanged {
		t.Fatalf("resource capabilities=%#v", snapshot.Capabilities.Resources)
	}

	for _, descriptor := range snapshot.Resources {
		if descriptor.Policy.Cache.Scope != ResourceCacheScopePrivate {
			t.Fatalf("global resource widened cache/subscription policy: %#v", descriptor)
		}
		switch descriptor.URI {
		case "cm://global/status", "cm://global/readiness":
			if descriptor.Policy.Cache.TTLMs != statusResourceTTLMs || descriptor.Policy.Subscription.Allowed {
				t.Fatalf("short-lived resource policy=%#v", descriptor)
			}
		default:
			if descriptor.Policy.Cache.TTLMs != 0 || descriptor.Policy.Subscription.Allowed {
				t.Fatalf("global resource advertised cache/subscription without a complete event owner: %#v", descriptor)
			}
		}
	}
	for _, descriptor := range snapshot.ResourceTemplates {
		if descriptor.Policy.Cache.Scope != ResourceCacheScopePrivate || descriptor.Policy.Cache.TTLMs != 0 {
			t.Fatalf("workspace resource can outlive canonical owner state: %#v", descriptor)
		}
		if descriptor.URITemplate == "cm://workspace/{workspace_id}/prompts/catalog" {
			if descriptor.Policy.Subscription.Allowed {
				t.Fatalf("prompt catalog advertised changes without a canonical prompt mutation owner: %#v", descriptor)
			}
		} else if !descriptor.Policy.Subscription.Allowed {
			t.Fatalf("event-backed workspace resource omitted subscription support: %#v", descriptor)
		}
	}

	descriptor := DescribeProtocolWithFeatures(nil, registry)
	base := ProjectFeatures(BaseProfile(), descriptor)
	openai := ProjectFeatures(OpenAIProfile(), descriptor)
	if !reflect.DeepEqual(base.Resources, openai.Resources) || !reflect.DeepEqual(base.ResourceTemplates, openai.ResourceTemplates) {
		t.Fatalf("profile changed core resource truth: base=%#v openai=%#v", base, openai)
	}
}

func TestCoreResourcesUseCanonicalReadModelsAndFenceEveryWorkspaceTemplate(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry()
	status := tools.VersionResult{Version: "test-version", Commit: "test-commit", BuildTime: "test-build"}
	registry.MustRegister("get_version", tools.Schema{Name: "get_version"}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.JSONResult(status), nil
	})

	var projectCalls atomic.Int32
	project := tools.ProjectContextResult{
		Root:        first.Path,
		WorkspaceID: first.ID,
		InstructionContext: instructioncontext.InstructionContext{
			Sources: []instructioncontext.SourceSnapshot{
				{
					Provider: ".agents", Kind: "skills", Scope: "workspace-provider",
					Paths: []string{filepath.Join(first.Path, ".agents", "skills", "provider", "SKILL.md")},
					Count: 1, Enabled: true, Loaded: true,
				},
				{
					Provider: ".cm", Kind: "prompts", Scope: "workspace-native",
					Paths: []string{filepath.Join(first.Path, ".cm", "prompts", "review.json")},
					Count: 1, Enabled: true, Loaded: false,
				},
			},
		},
	}
	registry.MustRegister("project_context", tools.Schema{Name: "project_context"}, func(context.Context, map[string]any) (tools.Result, error) {
		projectCalls.Add(1)
		return tools.JSONResult(project), nil
	})

	var skillCalls atomic.Int32
	skillInventory := tools.SkillsListResult{
		Skills: []skills.Skill{{Name: "provider-skill", Description: "provider", Source: ".agents", Path: filepath.Join(first.Path, ".agents", "skills", "provider", "SKILL.md")}},
		Count:  1,
	}
	registry.MustRegister("list_skills", tools.Schema{Name: "list_skills"}, func(context.Context, map[string]any) (tools.Result, error) {
		skillCalls.Add(1)
		return tools.JSONResult(skillInventory), nil
	})

	runtime := &tools.Runtime{
		Registry:      registry,
		Workspaces:    manager,
		SessionAccess: tools.NewSessionWorkspaceAccessManager(),
		LoopGuard:     tools.NewToolLoopGuard(),
	}
	features := FeatureRegistryForRuntime(runtime)
	executor := NewFeatureExecutor(features, runtime, first.ID, "test")

	statusRead, err := executor.ReadResource(context.Background(), "cm://global/status")
	if err != nil {
		t.Fatal(err)
	}
	var statusValue tools.VersionResult
	if err := json.Unmarshal([]byte(*statusRead.Content.Text), &statusValue); err != nil {
		t.Fatal(err)
	}
	if statusValue != status {
		t.Fatalf("status resource=%#v canonical=%#v", statusValue, status)
	}
	readiness, err := executor.ReadResource(context.Background(), "cm://global/readiness")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*readiness.Content.Text, "\"ready\":true") || !strings.Contains(*readiness.Content.Text, "\"version\":\"test-version\"") {
		t.Fatalf("readiness=%s", *readiness.Content.Text)
	}

	projectURI, _ := WorkspaceResourceURI(first.ID, resourcePathProjectContext)
	projectRead, err := executor.ReadResource(context.Background(), projectURI)
	if err != nil {
		t.Fatal(err)
	}
	var resourceProject, canonicalProject any
	if err := json.Unmarshal([]byte(*projectRead.Content.Text), &resourceProject); err != nil {
		t.Fatal(err)
	}
	canonicalBytes, _ := json.Marshal(project)
	if err := json.Unmarshal(canonicalBytes, &canonicalProject); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resourceProject, canonicalProject) {
		t.Fatalf("project resource diverged from canonical read model: resource=%#v canonical=%#v", resourceProject, canonicalProject)
	}

	beforeProject, beforeSkills := projectCalls.Load(), skillCalls.Load()
	for _, path := range []string{resourcePathProjectContext, resourcePathInstructionSources, resourcePathPromptCatalog, resourcePathSkillCatalog} {
		uri, err := WorkspaceResourceURI(second.ID, path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.ReadResource(context.Background(), uri); err == nil {
			t.Fatalf("workspace resource %q escaped bound workspace", path)
		}
		if projectCalls.Load() != beforeProject || skillCalls.Load() != beforeSkills {
			t.Fatalf("denied workspace resource %q reached canonical owner", path)
		}
	}

	for _, path := range []string{resourcePathInstructionSources, resourcePathPromptCatalog, resourcePathSkillCatalog} {
		uri, _ := WorkspaceResourceURI(first.ID, path)
		if _, err := executor.ReadResource(context.Background(), uri); err != nil {
			t.Fatalf("authorized workspace resource %q: %v", path, err)
		}
	}
}

func TestCoreResourceCatalogsSanitizePathsAndReadFreshOwnerState(t *testing.T) {
	configRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	runtime := tools.NewRuntime()
	root := t.TempDir()
	item, err := runtime.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}

	writeSkillFixture(t, filepath.Join(root, ".agents", "skills", "provider-skill"), "provider-skill", "provider skill")
	writeSkillFixture(t, filepath.Join(configRoot, "skills", "global-skill"), "global-skill", "global skill")
	writeSkillFixture(t, filepath.Join(home, ".claude", "skills", "home-only"), "home-only", "home visibility only")
	promptRoot := filepath.Join(root, ".cm", "prompts")
	if err := os.MkdirAll(promptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptRoot, "review.json"), []byte("{\"version\":1,\"name\":\"review\",\"messages\":[{\"role\":\"user\",\"content\":{\"type\":\"text\",\"text\":\"Review changes\"}}]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor := NewFeatureExecutor(FeatureRegistryForRuntime(runtime), runtime, item.ID, "test")

	workspaceSources := mustReadResourceText(t, executor, item.ID, resourcePathInstructionSources)
	if !strings.Contains(workspaceSources, "\"provider\":\".agents\"") || !strings.Contains(workspaceSources, "\"scope\":\"workspace-provider\"") || !strings.Contains(workspaceSources, "\"read_only\":true") {
		t.Fatalf("workspace instruction sources=%s", workspaceSources)
	}
	assertNoResourcePathLeak(t, workspaceSources, root, home, configRoot)

	globalSources := mustReadGlobalResourceText(t, executor, resourcePathInstructionSources)
	if strings.Contains(globalSources, "\"provider\":\"claude\"") ||
		!strings.Contains(globalSources, "\"provider\":\".cm\"") ||
		!strings.Contains(globalSources, "\"scope\":\"global-native\"") ||
		!strings.Contains(globalSources, "\"provider\":\"codemcp\"") ||
		!strings.Contains(globalSources, "\"scope\":\"builtin\"") {
		t.Fatalf("global instruction sources=%s", globalSources)
	}
	assertNoResourcePathLeak(t, globalSources, root, home, configRoot)

	prompts := mustReadResourceText(t, executor, item.ID, resourcePathPromptCatalog)
	if !strings.Contains(prompts, "\"name\":\"review\"") || !strings.Contains(prompts, "\"source\":\".cm\"") || strings.Contains(prompts, "review.json") {
		t.Fatalf("prompt catalog=%s", prompts)
	}
	assertNoResourcePathLeak(t, prompts, root, home, configRoot)

	workspaceSkills := mustReadResourceText(t, executor, item.ID, resourcePathSkillCatalog)
	for _, expected := range []string{"\"name\":\"provider-skill\"", "\"name\":\"global-skill\"", "\"name\":\"create-rule\"", "\"name\":\"create-skill\""} {
		if !strings.Contains(workspaceSkills, expected) {
			t.Fatalf("workspace skill catalog missing %s: %s", expected, workspaceSkills)
		}
	}
	assertNoResourcePathLeak(t, workspaceSkills, root, home, configRoot)

	globalSkills := mustReadGlobalResourceText(t, executor, resourcePathSkillCatalog)
	if !strings.Contains(globalSkills, "\"name\":\"global-skill\"") || strings.Contains(globalSkills, "\"name\":\"home-only\"") {
		t.Fatalf("global skill catalog=%s", globalSkills)
	}
	assertNoResourcePathLeak(t, globalSkills, root, home, configRoot)

	writeSkillFixture(t, filepath.Join(root, ".cm", "skills", "fresh-skill"), "fresh-skill", "fresh owner state")
	workspaceSkills = mustReadResourceText(t, executor, item.ID, resourcePathSkillCatalog)
	if !strings.Contains(workspaceSkills, "\"name\":\"fresh-skill\"") {
		t.Fatalf("zero-TTL resource did not reflect fresh canonical state: %s", workspaceSkills)
	}
}

func mustReadResourceText(t *testing.T, executor *FeatureExecutor, workspaceID, path string) string {
	t.Helper()
	uri, err := WorkspaceResourceURI(workspaceID, path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ReadResource(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content.Text == nil {
		t.Fatalf("resource %s returned non-text content", uri)
	}
	return *result.Content.Text
}

func mustReadGlobalResourceText(t *testing.T, executor *FeatureExecutor, path string) string {
	t.Helper()
	uri, err := GlobalResourceURI(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ReadResource(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content.Text == nil {
		t.Fatalf("resource %s returned non-text content", uri)
	}
	return *result.Content.Text
}

func assertNoResourcePathLeak(t *testing.T, content string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if path != "" && strings.Contains(content, path) {
			t.Fatalf("resource leaked filesystem path %q: %s", path, content)
		}
	}
}

func writeSkillFixture(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCoreResourceOwnerErrorsRemainProtocolBounded(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := &tools.Runtime{
		Registry:      tools.NewRegistry(),
		Workspaces:    workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json")),
		SessionAccess: tools.NewSessionWorkspaceAccessManager(),
		LoopGuard:     tools.NewToolLoopGuard(),
	}
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := NewFeatureExecutor(FeatureRegistryForRuntime(runtime), runtime, item.ID, "test")
	uri, _ := WorkspaceResourceURI(item.ID, resourcePathProjectContext)
	_, err = executor.ReadResource(context.Background(), uri)
	var protocol *Error
	if !errors.As(err, &protocol) || protocol.Code != ErrInternal || protocol.Message != "Resource resolution failed" {
		t.Fatalf("missing canonical owner error=%#v", err)
	}
}
