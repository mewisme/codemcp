package rules

import (
	"os"
	"path/filepath"
	"testing"
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
