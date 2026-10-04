package instructioncontext

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

func writeSkillFile(t *testing.T, root, provider, dir, name, description, body string) string {
	t.Helper()
	path := filepath.Join(root, provider, "skills", dir, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSkillSummariesPrefersAgentsAndReturnsMetadataOnly(t *testing.T) {
	root := t.TempDir()
	agentsPath := writeSkillFile(t, root, ".agents", "release", "release", "Release workflow", "SECRET BODY MUST NOT APPEAR")
	claudePath := writeSkillFile(t, root, ".claude", "review", "review", "Review workflow", "review body")
	cursorPath := writeSkillFile(t, root, ".cursor", "release", "release", "Alternative release workflow", "alternate body")

	loaded, err := LoadSkillSummaries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 6 {
		t.Fatalf("skills = %#v", loaded)
	}
	if loaded[0].Source != ".agents" || loaded[0].Path != agentsPath || loaded[0].Name != "release" || loaded[0].Description != "Release workflow" {
		t.Fatalf("agents skill = %#v", loaded[0])
	}
	if loaded[1].Source != ".claude" || loaded[1].Path != claudePath {
		t.Fatalf("claude skill = %#v", loaded[1])
	}
	if loaded[2].Source != ".cursor" || loaded[2].Path != cursorPath || loaded[2].Name != "release" {
		t.Fatalf("cursor skill = %#v", loaded[2])
	}
	if loaded[3].Source != "codemcp" || loaded[3].Name != "create-rule" ||
		loaded[4].Source != "codemcp" || loaded[4].Name != "create-skill" ||
		loaded[5].Source != "codemcp" || loaded[5].Name != "create-plan" {
		t.Fatalf("builtin skills = %#v", loaded[3:])
	}
	for _, skill := range loaded {
		if skill.Name == "SECRET BODY MUST NOT APPEAR" || skill.Description == "SECRET BODY MUST NOT APPEAR" {
			t.Fatalf("skill body leaked into summary: %#v", skill)
		}
	}
}

func TestLoadSkillSummariesSupportsAllProviders(t *testing.T) {
	root := t.TempDir()
	providers := []string{".agents", ".claude", ".claudes", ".cursor", ".codex"}
	for _, provider := range providers {
		writeSkillFile(t, root, provider, provider[1:], provider[1:], provider+" skill", "body")
	}
	loaded, err := LoadSkillSummaries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(providers)+3 {
		t.Fatalf("skills = %#v", loaded)
	}
	wantOrder := []string{".agents", ".claude", ".claudes", ".codex", ".cursor"}
	for i, provider := range wantOrder {
		if loaded[i].Source != provider {
			t.Fatalf("skill %d = %#v", i, loaded[i])
		}
	}
	if loaded[len(providers)].Source != "codemcp" || loaded[len(providers)+1].Source != "codemcp" || loaded[len(providers)+2].Source != "codemcp" {
		t.Fatalf("builtin skills = %#v", loaded[len(providers):])
	}
}

func TestLoadSkillSummariesSkipsSymlinkSkills(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeSkillFile(t, outside, ".agents", "outside", "outside", "Outside skill", "body")
	link := filepath.Join(root, ".agents", "skills", "linked")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, ".agents", "skills", "outside"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	loaded, err := LoadSkillSummaries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 || loaded[0].Source != "codemcp" || loaded[1].Source != "codemcp" || loaded[2].Source != "codemcp" {
		t.Fatalf("skills = %#v", loaded)
	}
}

func TestLoadSkillSummariesWithUserForWorkspaceExcludesUserProviders(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	workspaceRoot := t.TempDir()
	home := t.TempDir()
	writeSkillFile(t, home, ".agents", "home-release", "home-release", "Home release workflow", "SECRET HOME BODY")

	loaded, err := LoadSkillSummariesWithUserForWorkspace(workspaceRoot, workspaceRoot, home, instructionpolicy.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range loaded {
		if skill.Name == "home-release" {
			t.Fatalf("global provider leaked into project-context skill summaries: %#v", loaded)
		}
	}
}

func TestLoadSkillSummariesSeesManagedNativeFilesystemWithoutMetadataAuthority(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", configRoot)
	workspaceRoot := t.TempDir()
	home := t.TempDir()

	workspacePath := writeSkillFile(t, workspaceRoot, ".cm", "managed-context", "managed-context", "Managed context skill", "managed body")
	globalPath := writeSkillFile(t, configRoot, "", "global-context", "global-context", "Global context skill", "global body")
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".cm", "skills", ".cm-sources.json"), []byte("{not runtime truth}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "skills", ".cm-sources.json"), []byte("{not runtime truth}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSkillSummariesWithUserForWorkspace(workspaceRoot, workspaceRoot, home, instructionpolicy.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	seenWorkspace, seenGlobal := false, false
	for _, skill := range loaded {
		switch skill.Name {
		case "managed-context":
			seenWorkspace = skill.Source == ".cm" && skill.Path == workspacePath
		case "global-context":
			seenGlobal = skill.Source == ".cm" && skill.Path == globalPath
		case "metadata-only":
			t.Fatalf("metadata-only skill leaked into instruction context: %#v", loaded)
		}
	}
	if !seenWorkspace || !seenGlobal {
		t.Fatalf("managed native skills missing from instruction context: %#v", loaded)
	}
}
