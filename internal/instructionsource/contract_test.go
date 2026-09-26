package instructionsource

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSourceClassPrecedenceAndDynamicProviderOrder(t *testing.T) {
	values := []Source{
		{Class: ClassBuiltin, Provider: "builtin", Path: "builtin"},
		{Class: ClassDynamicProvider, Provider: ".zedagent", Path: "/project/.zedagent"},
		{Class: ClassProjectRoot, Provider: "agents", Path: "/project/AGENTS.md"},
		{Class: ClassGlobalNative, Provider: NativeSource, Path: "/global/rules/base.md"},
		{Class: ClassDynamicProvider, Provider: PreferredAgentSource, Path: "/project/.agents"},
		{Class: ClassWorkspaceNative, Provider: NativeSource, Path: "/project/.cm/rules/base.md"},
		{Class: ClassDynamicProvider, Provider: ".newagent", Path: "/project/.newagent"},
	}
	slices.SortFunc(values, Compare)

	want := []struct {
		class    Class
		provider string
	}{
		{ClassWorkspaceNative, NativeSource},
		{ClassGlobalNative, NativeSource},
		{ClassProjectRoot, "agents"},
		{ClassDynamicProvider, PreferredAgentSource},
		{ClassDynamicProvider, ".newagent"},
		{ClassDynamicProvider, ".zedagent"},
		{ClassBuiltin, "builtin"},
	}
	if len(values) != len(want) {
		t.Fatalf("sources=%#v", values)
	}
	for i, expected := range want {
		if values[i].Class != expected.class || values[i].Provider != expected.provider {
			t.Fatalf("source %d=%#v want class=%d provider=%q", i, values[i], expected.class, expected.provider)
		}
	}
}

func TestDynamicProviderIdentityAcceptsFutureHiddenProvidersOnly(t *testing.T) {
	for _, name := range []string{".agents", ".claude", ".newagent", ".future-provider"} {
		if got, ok := DynamicProviderIdentity(name); !ok || got != name {
			t.Fatalf("identity %q=%q ok=%v", name, got, ok)
		}
	}
	for _, name := range []string{"", ".", "..", "agents", ".cm", "../.agents", ".agents/child", ".agents\\child"} {
		if got, ok := DynamicProviderIdentity(name); ok || got != "" {
			t.Fatalf("invalid identity %q=%q ok=%v", name, got, ok)
		}
	}
}

func TestDiscoverDynamicProvidersIsImmediateBoundedAndFutureCompatible(t *testing.T) {
	workspaceRoot := t.TempDir()
	projectRoot := filepath.Join(workspaceRoot, "packages", "app")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeDirectContext := func(root, provider, name, content string) string {
		t.Helper()
		path := filepath.Join(root, provider, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	writeDirectContext(workspaceRoot, ".parentagent", "AGENTS.md", "parent")
	agents := writeDirectContext(projectRoot, ".agents", "AGENTS.md", "agents")
	claude := writeDirectContext(projectRoot, ".claude", "CLAUDE.md", "claude")
	future := writeDirectContext(projectRoot, ".newagent", "AGENTS.md", "future")
	writeDirectContext(projectRoot, ".zedagent", "CLAUDE.md", "zed")
	writeDirectContext(projectRoot, ".cm", "AGENTS.md", "reserved")
	writeDirectContext(projectRoot, "visibleagent", "AGENTS.md", "not hidden")
	writeDirectContext(projectRoot, ".readme-only", "README.md", "unsupported")
	writeDirectContext(projectRoot, ".recursive-only", filepath.Join("nested", "AGENTS.md"), "nested")
	writeDirectContext(filepath.Join(projectRoot, "nested-project"), ".childagent", "AGENTS.md", "child")

	providers, err := DiscoverDynamicProviders(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 4 {
		t.Fatalf("providers=%#v", providers)
	}
	wantNames := []string{".agents", ".claude", ".newagent", ".zedagent"}
	for i, name := range wantNames {
		if providers[i].Name != name {
			t.Fatalf("provider %d=%#v want=%q", i, providers[i], name)
		}
	}
	if len(providers[0].ContextFiles) != 1 || providers[0].ContextFiles[0] != agents {
		t.Fatalf("agents provider=%#v", providers[0])
	}
	if len(providers[1].ContextFiles) != 1 || providers[1].ContextFiles[0] != claude {
		t.Fatalf("claude provider=%#v", providers[1])
	}
	if len(providers[2].ContextFiles) != 1 || providers[2].ContextFiles[0] != future {
		t.Fatalf("future provider=%#v", providers[2])
	}
}

func TestDiscoverDynamicProvidersRejectsSymlinkBoundaries(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	writeFile := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	outsideProvider := filepath.Join(outside, "provider")
	writeFile(filepath.Join(outsideProvider, "AGENTS.md"))
	if err := os.Symlink(outsideProvider, filepath.Join(root, ".linked-provider")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	contextProvider := filepath.Join(root, ".linked-context")
	if err := os.MkdirAll(contextProvider, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideContext := filepath.Join(outside, "AGENTS.md")
	writeFile(outsideContext)
	if err := os.Symlink(outsideContext, filepath.Join(contextProvider, "AGENTS.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	rulesProvider := filepath.Join(root, ".linked-rules")
	if err := os.MkdirAll(rulesProvider, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideRules := filepath.Join(outside, "rules")
	if err := os.MkdirAll(outsideRules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideRules, filepath.Join(rulesProvider, "rules")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	skillsProvider := filepath.Join(root, ".linked-skills")
	if err := os.MkdirAll(skillsProvider, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideSkills := filepath.Join(outside, "skills")
	if err := os.MkdirAll(outsideSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideSkills, filepath.Join(skillsProvider, "skills")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	providers, err := DiscoverDynamicProviders(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 {
		t.Fatalf("symlink-backed providers discovered=%#v", providers)
	}
}

func TestProviderContextFilesAreDirectAndNamed(t *testing.T) {
	root := t.TempDir()
	provider := filepath.Join(root, ".newagent")
	files := map[string]string{
		filepath.Join(provider, "AGENTS.md"):           "agents",
		filepath.Join(provider, "CLAUDE.md"):           "claude",
		filepath.Join(provider, "CLAUDE.local.md"):     "local",
		filepath.Join(provider, "README.md"):           "readme",
		filepath.Join(provider, "nested", "AGENTS.md"): "nested",
		filepath.Join(provider, "nested", "CLAUDE.md"): "nested claude",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := directContextFiles(provider)
	want := []string{filepath.Join(provider, "AGENTS.md"), filepath.Join(provider, "CLAUDE.md")}
	if !slices.Equal(got, want) {
		t.Fatalf("context files=%#v want=%#v", got, want)
	}
}
