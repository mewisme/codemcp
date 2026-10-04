package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

func TestDiscoverAcrossProviders(t *testing.T) {
	root := t.TempDir()
	for _, provider := range []string{".agents", ".claude", ".cursor"} {
		dir := filepath.Join(root, provider, "skills", provider[1:])
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + provider[1:] + "\ndescription: provider skill\n---\n# Skill\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	values, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 6 {
		t.Fatalf("skills = %#v", values)
	}
	if values[3].Name != BuiltinCreateRuleName || values[4].Name != BuiltinCreateSkillName || values[5].Name != BuiltinCreatePlanName {
		t.Fatalf("builtin skills = %#v", values[3:])
	}
	loaded, err := Load(root, "cursor", 200000)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Skill.Source != ".cursor" || loaded.Truncated {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestDiscoverIncludesNativeWorkspaceSkills(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".cm", "skills", "native")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: native\ndescription: Native CodeMCP skill\n---\nBody\n"), 0600); err != nil {
		t.Fatal(err)
	}
	values, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 4 || values[0].Path != path || values[0].Source != ".cm" || !IsBuiltin(values[1]) || !IsBuiltin(values[2]) || !IsBuiltin(values[3]) {
		t.Fatalf("native skills=%#v", values)
	}
}

func TestDiscoverForWorkspaceUsesSelectedProjectProvidersAndWorkspaceNativeFirst(t *testing.T) {
	workspaceRoot := t.TempDir()
	projectRoot := filepath.Join(workspaceRoot, "packages", "app")
	writeSkill := func(root, provider, name, description string) {
		t.Helper()
		dir := filepath.Join(root, provider, "skills", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(projectRoot, ".agents", "project", "project provider")
	writeSkill(workspaceRoot, ".agents", "parent", "parent provider")
	writeSkill(workspaceRoot, ".cm", "native", "workspace native")
	values, err := DiscoverForWorkspace(projectRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 5 || values[0].Source != ".cm" || values[1].Source != ".agents" || !IsBuiltin(values[2]) || !IsBuiltin(values[3]) || !IsBuiltin(values[4]) {
		t.Fatalf("skills=%#v", values)
	}
	for _, value := range values {
		if value.Name == "parent" {
			t.Fatalf("provider discovery walked above selected project: %#v", values)
		}
	}
}

func TestDiscoverWithUserForWorkspaceLoadsOnlyNativeGlobalSkills(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	workspaceRoot := t.TempDir()
	home := t.TempDir()

	write := func(root, provider, name string) {
		t.Helper()
		dir := filepath.Join(root, provider, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + name + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(workspaceRoot, ".cm", "workspace-native")
	write(configRoot, "", "global-native")
	write(workspaceRoot, ".agents", "agents-provider")
	write(workspaceRoot, ".newagent", "future-provider")
	write(home, ".codex", "home-provider")

	disabled := false
	policy := instructionpolicy.DefaultConfig()
	policy.Sources["newagent"] = instructionpolicy.SourcePolicy{Skills: &disabled}
	values, err := DiscoverWithUserForWorkspace(workspaceRoot, workspaceRoot, home, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 7 {
		t.Fatalf("skills=%#v", values)
	}
	want := []string{".cm", ".cm", ".agents", ".newagent", BuiltinSource, BuiltinSource, BuiltinSource}
	for i, source := range want {
		if values[i].Source != source {
			t.Fatalf("skill %d=%#v want source=%q", i, values[i], source)
		}
	}
	for _, value := range values {
		if value.Name == "home-provider" {
			t.Fatalf("global provider skill leaked into effective inventory: %#v", values)
		}
	}
}

func TestDiscoverWithUserForWorkspaceDeduplicatesByCanonicalPrecedence(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	workspaceRoot := t.TempDir()
	home := t.TempDir()

	write := func(root, provider, description string) string {
		t.Helper()
		dir := filepath.Join(root, provider, "skills", "shared")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "SKILL.md")
		content := "---\nname: shared\ndescription: " + description + "\n---\n" + description + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	workspacePath := write(workspaceRoot, ".cm", "workspace native")
	write(configRoot, "", "user native")
	write(workspaceRoot, ".agents", "workspace provider")
	write(home, ".agents", "user provider")

	values, err := DiscoverWithUserForWorkspace(workspaceRoot, workspaceRoot, home, instructionpolicy.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, value := range values {
		if value.Name != "shared" {
			continue
		}
		count++
		if value.Path != workspacePath || value.Source != ".cm" || value.Description != "workspace native" {
			t.Fatalf("shared winner=%#v", value)
		}
	}
	if count != 1 {
		t.Fatalf("shared count=%d inventory=%#v", count, values)
	}
}

func TestLoadFromInventoryPreservesSafeExactLoading(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "SKILL.md")
	content := "---\nname: exact\ndescription: exact\n---\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	values := []Skill{{Name: "exact", Description: "exact", Path: path, Source: ".cm"}}

	loaded, err := LoadFromInventory(values, "exact", 500_000)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != content || loaded.Skill != values[0] || loaded.Truncated {
		t.Fatalf("loaded=%#v", loaded)
	}
	if _, err := LoadFromInventory(values, "exa", 500_000); err == nil {
		t.Fatal("non-exact skill name unexpectedly loaded")
	}

	target := filepath.Join(root, "target.md")
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := LoadFromInventory([]Skill{{Name: "linked", Path: link, Source: ".cm"}}, "linked", 500_000); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("symlink load error=%v", err)
	}
}

func TestSkillTraversalAndLoadBoundsRemainBounded(t *testing.T) {
	root := t.TempDir()
	providerRoot := filepath.Join(root, ".newagent", "skills")
	atLimit := filepath.Join(providerRoot, "one", "two", "three", "bounded", "SKILL.md")
	beyond := filepath.Join(providerRoot, "one", "two", "three", "four", "beyond", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(atLimit), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: bounded\ndescription: bounded\n---\n" + strings.Repeat("x", 256)
	if err := os.WriteFile(atLimit, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(beyond), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beyond, []byte("---\nname: beyond\ndescription: beyond\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	values, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 4 || values[0].Name != "bounded" || values[0].Path != atLimit || !IsBuiltin(values[1]) || !IsBuiltin(values[2]) || !IsBuiltin(values[3]) {
		t.Fatalf("bounded skills=%#v", values)
	}
	loaded, err := Load(root, "bounded", 64)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Truncated || len(loaded.Content) != 64 {
		t.Fatalf("loaded=%#v bytes=%d", loaded, len(loaded.Content))
	}
}

func TestReservedBuiltinSkillsCannotBeShadowedByNativeOrProviderFiles(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	workspaceRoot := t.TempDir()
	home := t.TempDir()
	const malicious = "MALICIOUS_SHADOW_BODY"

	write := func(root, provider, name string) {
		t.Helper()
		dir := filepath.Join(root, provider, "skills", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: shadow\n---\n" + malicious + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{BuiltinCreateRuleName, BuiltinCreateSkillName, BuiltinCreatePlanName} {
		write(workspaceRoot, ".cm", name)
		write(workspaceRoot, ".agents", name)
		write(configRoot, "", name)
	}

	policy := instructionpolicy.DefaultConfig()
	values, err := DiscoverWithUserForWorkspace(workspaceRoot, workspaceRoot, home, policy)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, skill := range values {
		if IsReservedName(skill.Name) {
			counts[skill.Name]++
			if !IsBuiltin(skill) {
				t.Fatalf("reserved skill was shadowed: %#v", skill)
			}
		}
	}
	if counts[BuiltinCreateRuleName] != 1 || counts[BuiltinCreateSkillName] != 1 || counts[BuiltinCreatePlanName] != 1 {
		t.Fatalf("reserved builtin counts=%v inventory=%#v", counts, values)
	}

	for _, name := range []string{BuiltinCreateRuleName, BuiltinCreateSkillName, BuiltinCreatePlanName} {
		loaded, err := LoadWithUser(workspaceRoot, home, name, 500_000, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !IsBuiltin(loaded.Skill) || strings.Contains(loaded.Content, malicious) {
			t.Fatalf("reserved builtin load=%#v", loaded)
		}
		projectOnly, err := Load(workspaceRoot, name, 500_000)
		if err != nil {
			t.Fatal(err)
		}
		if !IsBuiltin(projectOnly.Skill) || strings.Contains(projectOnly.Content, malicious) {
			t.Fatalf("project-only reserved builtin load=%#v", projectOnly)
		}
	}
}
