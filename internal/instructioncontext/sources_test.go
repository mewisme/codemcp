package instructioncontext

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

func TestDiscoverUserSourcesDetectsProviderResourcesWithoutLoadingThem(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("claude context"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "rules", "global.md"), []byte("---\nalwaysApply: true\n---\nProvider rule"), 0600); err != nil {
		t.Fatal(err)
	}
	writeSkillFile(t, home, ".claude", "review", "review", "Review workflow", "body")
	values, err := DiscoverUserSources(home, instructionpolicy.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 {
		t.Fatalf("sources = %#v", values)
	}
	for _, value := range values {
		if value.Provider != "claude" || value.Scope != "user-provider" || !value.Enabled || value.Loaded {
			t.Fatalf("provider source = %#v", value)
		}
	}
}

func TestDiscoverUserSourcesKeepsDisabledProviderVisible(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.WriteFile(path, []byte("claude context"), 0644); err != nil {
		t.Fatal(err)
	}
	disabled := false
	policy := instructionpolicy.DefaultConfig()
	policy.Sources["claude"] = instructionpolicy.SourcePolicy{Context: &disabled}
	values, err := DiscoverUserSources(home, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Provider != "claude" || values[0].Enabled || values[0].Loaded || values[0].Scope != "user-provider" || len(values[0].Paths) != 1 || values[0].Paths[0] != path {
		t.Fatalf("sources = %#v", values)
	}
}

func TestLoadedProjectSourcesIncludesWorkspacePromptMetadataWithoutLoadingBody(t *testing.T) {
	workspaceRoot := t.TempDir()
	promptRoot := filepath.Join(workspaceRoot, ".cm", "prompts")
	if err := os.MkdirAll(promptRoot, 0700); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(promptRoot, "review.json")
	if err := os.WriteFile(promptPath, []byte("{\"name\":\"review\",\"description\":\"Review changes\"}"), 0600); err != nil {
		t.Fatal(err)
	}
	values := LoadedProjectSources(ProjectMemoryBundle{}, nil, nil, workspaceRoot)
	if len(values) != 1 {
		t.Fatalf("sources=%#v", values)
	}
	value := values[0]
	if value.Provider != ".cm" || value.Kind != "prompts" || value.Scope != "workspace-native" || value.Loaded || !value.Enabled || value.Count != 1 || len(value.Paths) != 1 || value.Paths[0] != promptPath {
		t.Fatalf("prompt source=%#v", value)
	}
}
