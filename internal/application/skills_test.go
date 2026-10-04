package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	gitpkg "go.mewis.me/codemcp/internal/git"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func newSkillManagementHarness(t *testing.T) (*SkillManagementService, workspace.Workspace) {
	t.Helper()
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewSkillManagementService(manager), item
}

func TestSkillManagementScopeResolutionUsesCanonicalStores(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	workspaceRoot := workspacestate.New(item.Path).SkillsRoot()
	for _, request := range []SkillScopeRequest{
		{WorkspaceID: item.ID},
		{WorkspaceID: item.ID, Workspace: true},
	} {
		resolved, err := service.resolveScope(request)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Scope != SkillScopeWorkspace || resolved.Root != workspaceRoot || resolved.WorkspaceID != item.ID {
			t.Fatalf("workspace resolution=%#v", resolved)
		}
	}
	global, err := service.resolveScope(SkillScopeRequest{Global: true})
	if err != nil {
		t.Fatal(err)
	}
	if global.Scope != SkillScopeGlobal || global.Root != filepath.Join(configformat.RootPath(), "skills") {
		t.Fatalf("global resolution=%#v", global)
	}
	if _, err := service.resolveScope(SkillScopeRequest{WorkspaceID: item.ID, Workspace: true, Global: true}); err == nil {
		t.Fatal("workspace/global conflict unexpectedly accepted")
	}
}

func TestSkillManagementScopeConflictFailsBeforeAcquisitionOrMutation(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	acquired := false
	service.AcquireRepository = func(context.Context, skills.GitHubSource) (acquiredSkillRepository, error) {
		acquired = true
		return acquiredSkillRepository{}, errors.New("should not acquire")
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	_, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{
			WorkspaceID: item.ID,
			Workspace:   true,
			Global:      true,
		},
		Source: "owner/repo",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("scope conflict error=%v", err)
	}
	if acquired {
		t.Fatal("repository acquisition ran for invalid scope")
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("skills root mutated for invalid scope: %v", statErr)
	}
}

func TestSkillManagementAddInstallsManagedNativeSkill(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository, revision := createSkillGitRepository(t, map[string]string{"skill": skillFixture("managed", "Managed skill")})
	configureGitHubRewrite(t, repository, "owner", "repo")

	result, err := service.Add(t.Context(), SkillAddRequest{
		Scope:  SkillScopeRequest{WorkspaceID: item.ID},
		Source: "owner/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != revision || len(result.Skills) != 1 || result.Skills[0].Name != "managed" {
		t.Fatalf("result=%#v revision=%q", result, revision)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	metadata, err := skills.ReadManagedSources(root)
	if err != nil {
		t.Fatal(err)
	}
	entry := metadata.Skills["managed"]
	if entry.Source != "github:owner/repo" || entry.Revision != revision || entry.Path != "skill" {
		t.Fatalf("metadata=%#v", metadata)
	}
	values, err := skills.DiscoverWithUser(item.Path, t.TempDir(), instructionpolicy.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, value := range values {
		if value.Name == "managed" {
			found = value.Source == ".cm" && value.Path == filepath.Join(root, "managed", "SKILL.md")
		}
	}
	if !found {
		t.Fatalf("installed skill not resolver-visible: %#v", values)
	}
}

func TestSkillManagementSelectionAndAtomicMultiInstall(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "a", "alpha")
	writeRepositorySkill(t, repository, "b", "beta")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("a", 40))

	request := SkillAddRequest{Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo"}
	if _, err := service.Add(t.Context(), request); err == nil || !strings.Contains(err.Error(), "--skill") {
		t.Fatalf("multi-skill request error=%v", err)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	if _, err := os.Stat(filepath.Join(root, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alpha mutated before selection: %v", err)
	}

	request.All = true
	result, err := service.Add(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 2 || result.Skills[0].Name != "alpha" || result.Skills[1].Name != "beta" {
		t.Fatalf("result=%#v", result)
	}
	metadata, err := skills.ReadManagedSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Skills) != 2 {
		t.Fatalf("metadata=%#v", metadata)
	}
}

func TestSkillManagementExactSelectionInstallsOnlyRequestedSkill(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "a", "alpha")
	writeRepositorySkill(t, repository, "b", "beta")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("e", 40))

	result, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo", Skill: "beta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || result.Skills[0].Name != "beta" {
		t.Fatalf("result=%#v", result)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	if _, err := os.Stat(filepath.Join(root, "beta", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected alpha was installed: %v", err)
	}
}

func TestSkillManagementAddRejectsUnmanagedConflict(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "skill", "conflict")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("b", 40))
	root := workspacestate.New(item.Path).SkillsRoot()
	existing := filepath.Join(root, "conflict")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "SKILL.md"), []byte(skillFixture("conflict", "authored")), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	})
	if err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("conflict error=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(existing, "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "authored") {
		t.Fatalf("existing skill changed: err=%v data=%q", err, data)
	}
	metadata, err := skills.ReadManagedSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Skills) != 0 {
		t.Fatalf("metadata changed on conflict: %#v", metadata)
	}
}

func TestSkillManagementMalformedMetadataFailsBeforeDestinationMutation(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "skill", "candidate")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("f", 40))
	root := workspacestate.New(item.Path).SkillsRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(root, skills.ManagedSourcesFile)
	malformed := []byte("{\"schema\":1,\"skills\":{\"broken\":{\"source\":\"github:owner/repo\",\"revision\":\"bad\",\"path\":\"\"}}}\n")
	if err := os.WriteFile(metadataPath, malformed, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	})
	if err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("malformed metadata error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "candidate")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("candidate destination mutated: %v", statErr)
	}
	after, readErr := os.ReadFile(metadataPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(malformed) {
		t.Fatalf("malformed metadata was changed: %q", after)
	}
}

func TestSkillManagementRollbackPreservesStoreWhenMetadataCommitFails(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	priorRepository := t.TempDir()
	writeRepositorySkill(t, priorRepository, "prior", "prior")
	service.AcquireRepository = staticRepositoryAcquirer(priorRepository, strings.Repeat("9", 40))
	if _, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	}); err != nil {
		t.Fatal(err)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	metadataPath := filepath.Join(root, skills.ManagedSourcesFile)
	metadataBefore, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}

	repository := t.TempDir()
	writeRepositorySkill(t, repository, "a", "alpha")
	writeRepositorySkill(t, repository, "b", "beta")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("c", 40))
	service.BeforeMetadataCommit = func() error { return errors.New("forced metadata failure") }

	_, err = service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo", All: true,
	})
	if err == nil || !strings.Contains(err.Error(), "forced metadata failure") {
		t.Fatalf("rollback error=%v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, statErr := os.Stat(filepath.Join(root, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s remained after rollback: %v", name, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "prior", "SKILL.md")); statErr != nil {
		t.Fatalf("prior skill changed: %v", statErr)
	}
	metadataAfter, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(metadataAfter) != string(metadataBefore) {
		t.Fatalf("metadata changed after rollback:\nbefore=%s\nafter=%s", metadataBefore, metadataAfter)
	}
	assertNoSkillStages(t, root)
}

func TestSkillManagementGlobalScopeHonorsConfigDir(t *testing.T) {
	service, _ := newSkillManagementHarness(t)
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	configRoot := filepath.Join(t.TempDir(), "global-config")
	if err := configformat.SetRootPath(configRoot); err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "skill", "global-skill")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("d", 40))
	result, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{Global: true}, Source: "owner/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configformat.RootPath(), "skills")
	if result.Root != want {
		t.Fatalf("global root=%q want=%q", result.Root, want)
	}
	if _, err := os.Stat(filepath.Join(want, "global-skill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func staticRepositoryAcquirer(root, revision string) func(context.Context, skills.GitHubSource) (acquiredSkillRepository, error) {
	return func(context.Context, skills.GitHubSource) (acquiredSkillRepository, error) {
		return acquiredSkillRepository{Root: root, Revision: revision}, nil
	}
}

func writeRepositorySkill(t *testing.T, root, relative, name string) {
	t.Helper()
	dir := filepath.Join(root, relative)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillFixture(name, name+" description")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillFixture(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nDo the work.\n"
}

func createSkillGitRepository(t *testing.T, skillsByPath map[string]string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	if _, err := gitpkg.OrThrow(t.Context(), repo, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitpkg.OrThrow(t.Context(), repo, "config", "user.email", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitpkg.OrThrow(t.Context(), repo, "config", "user.name", "CodeMCP Test"); err != nil {
		t.Fatal(err)
	}
	for relative, content := range skillsByPath {
		dir := filepath.Join(repo, relative)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitpkg.OrThrow(t.Context(), repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitpkg.OrThrow(t.Context(), repo, "commit", "--quiet", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	result, err := gitpkg.OrThrow(t.Context(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return repo, strings.TrimSpace(result.Stdout)
}

func configureGitHubRewrite(t *testing.T, repository, owner, name string) {
	t.Helper()
	fileURL := "file://" + filepath.ToSlash(repository)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+fileURL+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/"+owner+"/"+name+".git")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
}

func assertNoSkillStages(t *testing.T, skillsRoot string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(skillsRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cm-skill-stage-") {
			t.Fatalf("skill staging directory leaked: %s", entry.Name())
		}
	}
}
