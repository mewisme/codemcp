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
		dir := filepath.Join(root, provider, "skills", provider[1:]+"-skill")
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
	if len(values) != 3 {
		t.Fatalf("skills = %#v", values)
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
	if len(values) != 1 || values[0].Path != path || values[0].Source != ".cm" {
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
	if len(values) != 2 || values[0].Source != ".cm" || values[1].Source != ".agents" {
		t.Fatalf("skills=%#v", values)
	}
	for _, value := range values {
		if value.Name == "parent" {
			t.Fatalf("provider discovery walked above selected project: %#v", values)
		}
	}
}

func TestDiscoverWithUserForWorkspaceAppliesProviderPolicyAndSourcePrecedence(t *testing.T) {
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
	write(home, ".agents", "home-provider")

	disabled := false
	policy := instructionpolicy.DefaultConfig()
	policy.Sources["newagent"] = instructionpolicy.SourcePolicy{Skills: &disabled}
	values, err := DiscoverWithUserForWorkspace(workspaceRoot, workspaceRoot, home, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 {
		t.Fatalf("skills=%#v", values)
	}
	want := []string{".cm", ".cm", ".agents"}
	for i, source := range want {
		if values[i].Source != source {
			t.Fatalf("skill %d=%#v want source=%q", i, values[i], source)
		}
	}
	for _, value := range values {
		if value.Name == "future-provider" || value.Name == "home-provider" {
			t.Fatalf("disabled or home provider loaded: %#v", values)
		}
	}
}

func TestSkillTraversalAndLoadBoundsRemainBounded(t *testing.T) {
	root := t.TempDir()
	providerRoot := filepath.Join(root, ".newagent", "skills")
	atLimit := filepath.Join(providerRoot, "one", "two", "three", "at-limit", "SKILL.md")
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
	if len(values) != 1 || values[0].Name != "bounded" || values[0].Path != atLimit {
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
