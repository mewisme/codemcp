package instructioncontext

import (
	"os"
	"path/filepath"
	"testing"
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
	cursorPath := writeSkillFile(t, root, ".cursor", "release-alt", "release", "Alternative release workflow", "alternate body")

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
