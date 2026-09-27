package mcp

import (
	"context"
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
	if got[len(got)-2].Name != skills.BuiltinCreateRuleName || got[len(got)-1].Name != skills.BuiltinCreateSkillName {
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

func skillsPolicy(t *testing.T) instructionpolicy.Config {
	t.Helper()
	policy, err := instructionpolicy.DefaultStore().Load()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}
