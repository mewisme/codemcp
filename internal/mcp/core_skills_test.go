package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestCanonicalSkillProjectionMatchesNativeResolver(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, configRoot)
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".cm", "skills", "native"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cm", "skills", "native", "SKILL.md"), []byte("---\nname: native\ndescription: native skill\n---\nnative body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".newagent", "skills", "future"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".newagent", "skills", "future", "SKILL.md"), []byte("---\nname: future\ndescription: future skill\n---\nfuture body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := tools.NewRuntime()
	runtime.Workspaces = manager

	want, err := skills.DiscoverWithUser(root, "", skillsPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalSkillInventory(context.Background(), runtime, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("canonical inventory count=%d want=%d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("skill[%d]=%#v want %#v", i, got[i], want[i])
		}
	}
	if got[len(got)-3].Name != skills.BuiltinCreateRuleName || got[len(got)-2].Name != skills.BuiltinCreateSkillName || got[len(got)-1].Name != skills.BuiltinCreatePlanName {
		t.Fatalf("reserved builtins were not preserved at canonical tail: %#v", got)
	}
}

func TestCanonicalSkillLoadPreservesContentIdentity(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".cm", "skills", "sample", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: sample\ndescription: sample skill\n---\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := tools.NewRuntime()
	runtime.Workspaces = manager

	loaded, err := loadCanonicalSkill(runtime, item.ID, "sample", skillContentMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != content || loaded.Truncated {
		t.Fatalf("loaded=%#v", loaded)
	}
	manifest := skillManifest(loaded)
	digest, _ := manifest["digest"].(string)
	if !strings.HasPrefix(digest, "sha256:") || len(strings.TrimPrefix(digest, "sha256:")) != 64 {
		t.Fatalf("digest=%q", digest)
	}

	builtin, err := loadCanonicalSkill(runtime, item.ID, skills.BuiltinCreateSkillName, skillContentMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !skills.IsBuiltin(builtin.Skill) || !strings.Contains(builtin.Content, "create_skill") {
		t.Fatalf("builtin=%#v", builtin)
	}
}

func TestSkillsExtensionManifestAndResourceRead(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".cm", "skills", "sample")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "---\nname: sample\ndescription: sample skill\nlicense: MIT\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "note.txt"), []byte("support"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := tools.NewRuntime()
	runtime.Workspaces = manager
	executor := NewFeatureExecutor(FeatureRegistryForRuntime(runtime), runtime, item.ID, "test")
	executor.Profile = OpenAIProfile()

	descriptor := DescribeProtocolWithFeatures(nil, executor.Registry)
	if _, ok := descriptor.Capabilities.Extensions[SkillsExtensionID]; !ok {
		t.Fatalf("skills extension missing: %#v", descriptor.Capabilities.Extensions)
	}
	listed, err := executor.Invoke(context.Background(), SkillsListMethod, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := listed["skills"].([]map[string]any)
	if len(items) == 0 {
		if generic, ok := listed["skills"].([]any); ok {
			for _, raw := range generic {
				if entry, ok := raw.(map[string]any); ok {
					items = append(items, entry)
				}
			}
		}
	}
	var entry map[string]any
	for _, candidate := range items {
		if candidate["name"] == "sample" {
			entry = candidate
			break
		}
	}
	if entry == nil {
		t.Fatalf("sample skill missing from list: %#v", listed)
	}
	frontmatter, _ := entry["frontmatter"].(map[string]any)
	if frontmatter["license"] != "MIT" {
		t.Fatalf("frontmatter lost fields: %#v", frontmatter)
	}
	uri, _ := entry["uri"].(string)
	got, err := executor.Invoke(context.Background(), SkillsGetMethod, map[string]any{"uri": uri})
	if err != nil {
		t.Fatal(err)
	}
	if got["uri"] != uri {
		t.Fatalf("skills/get uri=%#v want %q", got["uri"], uri)
	}
	resources, _ := entry["resources"].([]map[string]any)
	if len(resources) == 0 {
		if generic, ok := entry["resources"].([]any); ok {
			for _, raw := range generic {
				if resource, ok := raw.(map[string]any); ok {
					resources = append(resources, resource)
				}
			}
		}
	}
	if len(resources) != 2 {
		t.Fatalf("resources=%#v", entry["resources"])
	}
	for _, resource := range resources {
		resourceURI, _ := resource["uri"].(string)
		digest, _ := resource["digest"].(string)
		read, err := executor.ReadResource(context.Background(), resourceURI)
		if err != nil {
			t.Fatal(err)
		}
		var data []byte
		if read.Content.Text != nil {
			data = []byte(*read.Content.Text)
		} else {
			data = read.Content.Blob
		}
		if digest != digestBytes(data) {
			t.Fatalf("digest mismatch for %s: %q != %q", resourceURI, digest, digestBytes(data))
		}
	}
}

func TestOpenAISkillsProjectionEnforcesImportLimitsWithoutChangingBase(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		name := "skill-" + string(rune('a'+i))
		dir := filepath.Join(root, ".cm", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + name + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runtime := tools.NewRuntime()
	runtime.Workspaces = manager

	base := NewFeatureExecutor(FeatureRegistryForRuntime(runtime), runtime, item.ID, "test")
	base.Profile = BaseProfile()
	baseList, err := base.ListSkills(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	baseSkills, _ := baseList["skills"].([]map[string]any)
	if len(baseSkills) < 6 {
		t.Fatalf("base skills unexpectedly restricted: %d", len(baseSkills))
	}

	openai := NewFeatureExecutor(FeatureRegistryForRuntime(runtime), runtime, item.ID, "test")
	openai.Profile = OpenAIProfile()
	openAIList, err := openai.ListSkills(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	openAISkills, _ := openAIList["skills"].([]map[string]any)
	if len(openAISkills) != openAIMaxSkills {
		t.Fatalf("openai skills=%d want=%d", len(openAISkills), openAIMaxSkills)
	}

	oversized := skills.Skill{Name: "oversized", Description: "oversized", Source: ".cm"}
	dir := filepath.Join(root, ".cm", "skills", "oversized")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oversized.Path = filepath.Join(dir, "SKILL.md")
	content := "---\nname: oversized\ndescription: oversized\n---\n" + strings.Repeat("x", openAIMaxManifestBytes)
	if err := os.WriteFile(oversized.Path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildSkillBundle(oversized, BaseProfile()); err != nil {
		t.Fatalf("base profile should remain standards-oriented: %v", err)
	}
	if _, err := buildSkillBundle(oversized, OpenAIProfile()); err == nil {
		t.Fatal("OpenAI profile accepted oversized SKILL.md")
	}
}

func TestSkillsToolFallbackMatchesNativeSkillContent(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	root := t.TempDir()
	runtime := tools.NewRuntime()
	item, err := runtime.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".cm", "skills", "fallback")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: fallback\ndescription: fallback skill\n---\nfallback body\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := NewFeatureExecutor(FeatureRegistryForRuntime(runtime), runtime, item.ID, "test")

	native, err := executor.ReadResource(context.Background(), skillURI("fallback", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if native.Content.Text == nil || *native.Content.Text != content {
		t.Fatalf("native content=%#v", native.Content)
	}
	fallback, err := runtime.Registry.Call(context.Background(), "load_skill", map[string]any{
		"workspace_id": item.ID,
		"name":         "fallback",
		"max_bytes":    skillContentMaxBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := fallback.StructuredContent.(skills.Loaded)
	if !ok {
		data, marshalErr := json.Marshal(fallback.StructuredContent)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if unmarshalErr := json.Unmarshal(data, &loaded); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
	}
	if loaded.Content != content || loaded.Skill.Name != "fallback" {
		t.Fatalf("fallback=%#v", loaded)
	}
}

func skillsPolicy(t *testing.T) instructionpolicy.Config {
	t.Helper()
	return instructionpolicy.DefaultConfig()
}
