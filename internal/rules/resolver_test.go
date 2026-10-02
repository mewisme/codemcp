package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

func TestLoadRulesAcrossProviders(t *testing.T) {
	root := t.TempDir()
	for _, provider := range []string{".claude", ".agents", ".cursor"} {
		dir := filepath.Join(root, provider, "rules")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\npaths:\n  - \"src/**/*.ts\"\n---\nUse TypeScript rule from " + provider
		if provider == ".cursor" {
			content = "---\nglobs: [\"src/**/*.ts\", \"*.tsx\"]\n---\nCursor rule"
		}
		if err := os.WriteFile(filepath.Join(dir, "rule.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(root, "src", "app", "page.ts")
	values, err := LoadForFile(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 {
		t.Fatalf("rules = %#v", values)
	}
}

func TestAlwaysApplyRule(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "always.mdc"), []byte("---\nalwaysApply: true\n---\nAlways"), 0644); err != nil {
		t.Fatal(err)
	}
	values, err := LoadForFile(root, filepath.Join(root, "anything.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || !values[0].AlwaysApply {
		t.Fatalf("rules = %#v", values)
	}
}

func TestDiscoverIncludesNativeWorkspaceRules(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".cm", "rules")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "native.md")
	if err := os.WriteFile(path, []byte("---\nalwaysApply: true\n---\nNative CodeMCP rule"), 0600); err != nil {
		t.Fatal(err)
	}
	values, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Path != path || values[0].Source != ".cm" {
		t.Fatalf("native rules=%#v", values)
	}
}

func TestDiscoverForWorkspaceUsesSelectedProjectProvidersAndWorkspaceNativeFirst(t *testing.T) {
	workspaceRoot := t.TempDir()
	projectRoot := filepath.Join(workspaceRoot, "packages", "app")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agents", "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agents", "rules", "project.md"), []byte("---\nalwaysApply: true\n---\nproject provider"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".agents", "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".agents", "rules", "parent.md"), []byte("---\nalwaysApply: true\n---\nparent provider"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".cm", "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".cm", "rules", "native.md"), []byte("---\nalwaysApply: true\n---\nworkspace native"), 0600); err != nil {
		t.Fatal(err)
	}
	values, err := DiscoverForWorkspace(projectRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].Source != ".cm" || values[1].Source != ".agents" {
		t.Fatalf("rules=%#v", values)
	}
	for _, value := range values {
		if value.Content == "parent provider" {
			t.Fatalf("provider discovery walked above selected project: %#v", values)
		}
	}
}

func TestDiscoverWithUserForWorkspaceIgnoresLegacyProviderPolicyAndKeepsSourcePrecedence(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	workspaceRoot := t.TempDir()
	home := t.TempDir()

	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(workspaceRoot, ".cm", "rules", "workspace.md"), "workspace native")
	write(filepath.Join(configRoot, "rules", "global.md"), "global native")
	write(filepath.Join(workspaceRoot, ".agents", "rules", "agents.md"), "agents provider")
	write(filepath.Join(workspaceRoot, ".newagent", "rules", "future.md"), "future provider")
	write(filepath.Join(home, ".agents", "rules", "home.md"), "home provider")

	disabled := false
	policy := instructionpolicy.DefaultConfig()
	policy.Sources["newagent"] = instructionpolicy.SourcePolicy{Rules: &disabled}
	values, err := DiscoverWithUserForWorkspace(workspaceRoot, workspaceRoot, home, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 4 {
		t.Fatalf("rules=%#v", values)
	}
	want := []string{".cm", ".cm", ".agents", ".newagent"}
	for i, source := range want {
		if values[i].Source != source {
			t.Fatalf("rule %d=%#v want source=%q", i, values[i], source)
		}
	}
	for _, value := range values {
		if value.Content == "home provider" {
			t.Fatalf("home provider leaked into workspace-scoped discovery: %#v", values)
		}
	}
}

func TestRuleTraversalAndContentBoundsRemainBounded(t *testing.T) {
	root := t.TempDir()
	atLimit := filepath.Join(root, ".newagent", "rules", "one", "two", "three", "limit.md")
	beyond := filepath.Join(root, ".newagent", "rules", "one", "two", "three", "four", "beyond.md")
	if err := os.MkdirAll(filepath.Dir(atLimit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(atLimit, []byte(strings.Repeat("a", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(beyond), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beyond, []byte("must not load"), 0o644); err != nil {
		t.Fatal(err)
	}

	values, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Path != atLimit || len(values[0].Content) != 4000 {
		t.Fatalf("bounded rules=%#v", values)
	}
}
