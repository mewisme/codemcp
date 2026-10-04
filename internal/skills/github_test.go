package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseGitHubSourceAcceptedFormsNormalizeIdentically(t *testing.T) {
	want := GitHubSource{
		Owner: "owner", Repository: "repo",
		Identity: "github:owner/repo",
		CloneURL: "https://github.com/owner/repo.git",
	}
	for _, raw := range []string{
		"owner/repo",
		"https://github.com/owner/repo",
		"https://github.com/owner/repo.git",
		"git@github.com:owner/repo.git",
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := ParseGitHubSource(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("source=%#v want=%#v", got, want)
			}
		})
	}
}

func TestParseGitHubSourceRejectsUnsupportedAndUnsafeForms(t *testing.T) {
	for _, raw := range []string{
		"",
		" owner/repo",
		"owner/repo ",
		"owner",
		"owner/repo/extra",
		"owner/repo.git",
		"../repo",
		"owner/..",
		"owner/re%70o",
		"http://github.com/owner/repo",
		"https://www.github.com/owner/repo",
		"https://github.com/owner/repo/",
		"https://github.com/owner/repo/extra",
		"https://github.com/owner/repo?ref=main",
		"https://github.com/owner/repo#main",
		"git@github.com:owner/repo",
		"git@gitlab.com:owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
	} {
		t.Run(strings.ReplaceAll(raw, "/", "_"), func(t *testing.T) {
			if _, err := ParseGitHubSource(raw); err == nil {
				t.Fatalf("source %q unexpectedly accepted", raw)
			}
		})
	}
}

func TestParseGitHubIdentityAcceptsOnlyCanonicalIdentity(t *testing.T) {
	got, err := ParseGitHubIdentity("github:owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity != "github:owner/repo" || got.CloneURL != "https://github.com/owner/repo.git" {
		t.Fatalf("identity=%#v", got)
	}
	for _, raw := range []string{
		"owner/repo",
		"github:Owner/Repo",
		"github:owner/repo/extra",
		"https://github.com/owner/repo",
	} {
		if _, err := ParseGitHubIdentity(raw); err == nil {
			t.Fatalf("identity %q unexpectedly accepted", raw)
		}
	}
}

func TestValidateNativeSkillRootAndRepositoryDiscovery(t *testing.T) {
	repository := t.TempDir()
	writeSkillFixture(t, filepath.Join(repository, "skills", "alpha"), "alpha", "Alpha skill")
	writeSkillFixture(t, filepath.Join(repository, "nested", "beta"), "beta", "Beta skill")
	lowercaseOnly := filepath.Join(repository, "legacy", "lowercase")
	if err := os.MkdirAll(lowercaseOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lowercaseOnly, "skill.md"), []byte(validSkillManifest("lowercase")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../objects", filepath.Join(repository, ".git", "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	values, err := DiscoverRepositorySkills(repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("candidates=%#v", values)
	}
	if values[0].Skill.Name != "alpha" || values[0].RelativePath != "skills/alpha" {
		t.Fatalf("first candidate=%#v", values[0])
	}
	for _, value := range values {
		if !reflect.DeepEqual(value.Files, []string{"SKILL.md", "notes.txt"}) {
			t.Fatalf("candidate files=%#v", value.Files)
		}
	}
	fullDepth, err := DiscoverRepositorySkillsWithOptions(repository, RepositoryDiscoveryOptions{FullDepth: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fullDepth) != 2 || fullDepth[0].Skill.Name != "alpha" || fullDepth[1].Skill.Name != "beta" {
		t.Fatalf("full-depth candidates=%#v", fullDepth)
	}
}

func TestRepositoryDiscoveryRootSkillShadowsNestedUnlessFullDepth(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "SKILL.md"), []byte(validSkillManifest("root-skill")), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkillFixture(t, filepath.Join(repository, "skills", "nested"), "nested", "Nested skill")

	values, err := DiscoverRepositorySkills(repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Skill.Name != "root-skill" || values[0].RelativePath != "" {
		t.Fatalf("default root discovery=%#v", values)
	}

	fullDepth, err := DiscoverRepositorySkillsWithOptions(repository, RepositoryDiscoveryOptions{FullDepth: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fullDepth) != 2 || fullDepth[0].Skill.Name != "nested" || fullDepth[1].Skill.Name != "root-skill" {
		t.Fatalf("full-depth root discovery=%#v", fullDepth)
	}
}

func TestRepositoryDiscoveryIncludesDirectRootSkillAlongsidePriorityContainer(t *testing.T) {
	repository := t.TempDir()
	archifyRoot := filepath.Join(repository, "archify")
	if err := os.MkdirAll(archifyRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archifyRoot, "SKILL.md"), []byte("---\nname: archify\ndescription: Create architecture and lifecycle diagrams for states: a leave or travel plan\nlicense: MIT\nmetadata:\n  version: \"3.0\"\n---\n# Archify\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkillFixture(t, filepath.Join(repository, ".agents", "skills", "archify-review"), "archify-review", "Archify review skill")

	values, err := DiscoverRepositorySkills(repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 {
		t.Fatalf("candidates=%#v", values)
	}
	byName := map[string]RepositorySkillCandidate{}
	for _, value := range values {
		byName[value.Skill.Name] = value
	}
	if got := byName["archify"]; got.RelativePath != "archify" {
		t.Fatalf("direct root skill=%#v", got)
	}
	if got := byName["archify-review"]; got.RelativePath != ".agents/skills/archify-review" {
		t.Fatalf("priority-container skill=%#v", got)
	}
}

func TestValidateNativeSkillRootFailsClosedForUnsafeContent(t *testing.T) {
	t.Run("manifest symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "manifest.md")
		if err := os.WriteFile(target, []byte(validSkillManifest("linked")), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "SKILL.md")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := ValidateNativeSkillRoot(root); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("resource symlink", func(t *testing.T) {
		root := t.TempDir()
		writeSkillFixture(t, root, "linked-resource", "Linked resource")
		if err := os.Symlink("notes.txt", filepath.Join(root, "linked.txt")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := ValidateNativeSkillRoot(root); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("unsafe name", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(validSkillManifest("../escape")), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateNativeSkillRoot(root); err == nil || !strings.Contains(err.Error(), "skill name") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("fifo resource", func(t *testing.T) {
		if testing.Short() {
			t.Skip("requires mkfifo")
		}
		root := t.TempDir()
		writeSkillFixture(t, root, "fifo", "FIFO resource")
		fifo := filepath.Join(root, "pipe")
		if err := syscallMkfifo(fifo); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		if _, err := ValidateNativeSkillRoot(root); err == nil || !strings.Contains(err.Error(), "non-regular") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestNativeSkillDiscoveryRequiresCanonicalManifestName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".cm", "skills", "legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte("---\nname: legacy\ndescription: Existing lowercase manifest\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	values, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || !IsBuiltin(values[0]) || !IsBuiltin(values[1]) || !IsBuiltin(values[2]) {
		t.Fatalf("inventory=%#v", values)
	}
}

func writeSkillFixture(t *testing.T, root, name, description string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+description+"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validSkillManifest(name string) string {
	return "---\nname: " + name + "\ndescription: valid\n---\nbody\n"
}
