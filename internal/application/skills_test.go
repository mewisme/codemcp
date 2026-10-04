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
	service := NewSkillManagementService(manager)
	service.AuditSecurity = func(context.Context, skills.GitHubSource, []string) (skills.SecurityAssessment, error) {
		return skills.SecurityAssessment{}, nil
	}
	return service, item
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
	repository, revision := createSkillGitRepository(t, map[string]string{"managed": skillFixture("managed", "Managed skill")})
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
	if entry.Source != "github:owner/repo" || entry.Revision != revision || entry.Path != "managed" || entry.ContentHash == "" {
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
	writeRepositorySkill(t, repository, "alpha", "alpha")
	writeRepositorySkill(t, repository, "beta", "beta")
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
	writeRepositorySkill(t, repository, "alpha", "alpha")
	writeRepositorySkill(t, repository, "beta", "beta")
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

func TestSkillManagementExactSelectionFindsDirectRootSkillAlongsidePriorityContainer(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	archifyRoot := filepath.Join(repository, "archify")
	if err := os.MkdirAll(archifyRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archifyRoot, "SKILL.md"), []byte("---\nname: archify\ndescription: Create architecture and lifecycle diagrams for states: a leave or travel plan\nlicense: MIT\nmetadata:\n  version: \"3.0\"\n---\n# Archify\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRepositorySkill(t, repository, filepath.Join(".agents", "skills", "archify-review"), "archify-review")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("f", 40))

	result, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "tt-a1i/archify", Skill: "archify",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || result.Skills[0].Name != "archify" {
		t.Fatalf("result=%#v", result)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	if _, err := os.Stat(filepath.Join(root, "archify", "SKILL.md")); err != nil {
		t.Fatalf("selected direct-root skill was not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "archify-review")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected priority-container skill was installed: %v", err)
	}
}

func TestSkillManagementRiskReviewGatesAddBeforeMutation(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "risky", "risky")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("1", 40))
	service.AuditSecurity = func(context.Context, skills.GitHubSource, []string) (skills.SecurityAssessment, error) {
		return riskySkillAssessment("owner/repo", "risky"), nil
	}

	request := SkillAddRequest{Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo"}
	if _, err := service.Add(t.Context(), request); err == nil || !strings.Contains(err.Error(), "risk review is required") {
		t.Fatalf("unreviewed risky add err=%v", err)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	if _, err := os.Stat(filepath.Join(root, "risky")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("risky add mutated before review: %v", err)
	}

	events := make([]string, 0, 3)
	reviewed := false
	request.ReviewRisk = func(assessment skills.SecurityAssessment) error {
		events = append(events, "review")
		reviewed = assessment.RequiresConfirmation()
		return nil
	}
	request.Progress = func(event SkillMutationEvent) { events = append(events, event.Phase) }
	result, err := service.Add(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reviewed || len(result.Skills) != 1 || result.Skills[0].Name != "risky" {
		t.Fatalf("reviewed=%v result=%#v", reviewed, result)
	}
	if got := strings.Join(events, ","); got != "repository_acquired,discovered,selected,review,installing" {
		t.Fatalf("add lifecycle order=%q", got)
	}
}

func TestSkillManagementRiskReviewRejectsUpdateBeforeAnyMutation(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "managed-risk", "managed-risk")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("2", 40))
	if _, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	}); err != nil {
		t.Fatal(err)
	}

	root := workspacestate.New(item.Path).SkillsRoot()
	metadataBefore, err := os.ReadFile(filepath.Join(root, skills.ManagedSourcesFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "managed-risk", "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("3", 40))
	service.AuditSecurity = func(context.Context, skills.GitHubSource, []string) (skills.SecurityAssessment, error) {
		return riskySkillAssessment("owner/repo", "managed-risk"), nil
	}
	denied := errors.New("review denied")
	events := make([]string, 0, 3)
	_, err = service.Update(t.Context(), SkillUpdateRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID},
		Name:  "managed-risk",
		ReviewRisk: func(skills.SecurityAssessment) error {
			events = append(events, "review")
			return denied
		},
		Progress: func(event SkillMutationEvent) { events = append(events, event.Phase) },
	})
	if !errors.Is(err, denied) {
		t.Fatalf("update review err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "managed-risk", "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("update mutated tree before review: %v", err)
	}
	metadataAfter, err := os.ReadFile(filepath.Join(root, skills.ManagedSourcesFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(metadataAfter) != string(metadataBefore) {
		t.Fatalf("update mutated metadata before review\nbefore=%s\nafter=%s", metadataBefore, metadataAfter)
	}
	if got := strings.Join(events, ","); got != "acquired,review" {
		t.Fatalf("denied update lifecycle order=%q", got)
	}
}

func TestSkillManagementSecurityAuditFailureIsAdvisory(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "audit-fail-open", "audit-fail-open")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("4", 40))
	service.AuditSecurity = func(context.Context, skills.GitHubSource, []string) (skills.SecurityAssessment, error) {
		return skills.SecurityAssessment{}, errors.New("audit unavailable")
	}
	result, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || result.Skills[0].Name != "audit-fail-open" {
		t.Fatalf("result=%#v", result)
	}
}

func riskySkillAssessment(source, name string) skills.SecurityAssessment {
	return skills.SecurityAssessment{
		Source: source, DetailsURL: "https://skills.sh/" + source,
		Skills: []skills.SkillSecurityAssessment{{
			Name: name, Gen: &skills.PartnerAudit{Risk: skills.SecurityRiskHigh},
		}},
	}
}

func TestSkillManagementAddRejectsUnmanagedConflict(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "conflict", "conflict")
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
	writeRepositorySkill(t, repository, "candidate", "candidate")
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
	writeRepositorySkill(t, repository, "alpha", "alpha")
	writeRepositorySkill(t, repository, "beta", "beta")
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
	writeRepositorySkill(t, repository, "global-skill", "global-skill")
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

func TestSkillManagementUpdateUsesContentHashAndPreservesUnchangedTrees(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "managed", "managed")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("1", 40))
	if _, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	}); err != nil {
		t.Fatal(err)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	before, err := skills.ReadManagedSources(root)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := before.Skills["managed"].ContentHash

	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("2", 40))
	unchanged, err := service.Update(t.Context(), SkillUpdateRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "managed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Skills) != 1 || unchanged.Skills[0].Changed || unchanged.Skills[0].ContentHash != oldHash {
		t.Fatalf("unchanged update=%#v", unchanged)
	}
	metadata, err := skills.ReadManagedSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Skills["managed"].Revision != strings.Repeat("2", 40) || metadata.Skills["managed"].ContentHash != oldHash {
		t.Fatalf("metadata after unchanged update=%#v", metadata)
	}

	if err := os.WriteFile(filepath.Join(repository, "managed", "notes.txt"), []byte("new content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("3", 40))
	changed, err := service.Update(t.Context(), SkillUpdateRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "managed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Skills) != 1 || !changed.Skills[0].Changed || changed.Skills[0].ContentHash == oldHash {
		t.Fatalf("changed update=%#v", changed)
	}
	data, err := os.ReadFile(filepath.Join(root, "managed", "notes.txt"))
	if err != nil || string(data) != "new content\n" {
		t.Fatalf("updated resource err=%v data=%q", err, data)
	}
}

func TestSkillManagementUpdateRejectsLocalDriftAndRollsBackMetadataFailure(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "managed", "managed")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("4", 40))
	if _, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	}); err != nil {
		t.Fatal(err)
	}
	root := workspacestate.New(item.Path).SkillsRoot()
	installedManifest := filepath.Join(root, "managed", "SKILL.md")
	originalManifest, err := os.ReadFile(installedManifest)
	if err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(root, skills.ManagedSourcesFile)
	originalMetadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(installedManifest, append(originalManifest, []byte("\nlocal drift\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "managed", "remote.txt"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("5", 40))
	if _, err := service.Update(t.Context(), SkillUpdateRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "managed",
	}); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("local drift update err=%v", err)
	}
	if err := os.WriteFile(installedManifest, originalManifest, 0o600); err != nil {
		t.Fatal(err)
	}

	service.BeforeMetadataCommit = func() error { return errors.New("forced update metadata failure") }
	if _, err := service.Update(t.Context(), SkillUpdateRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "managed",
	}); err == nil || !strings.Contains(err.Error(), "forced update metadata failure") {
		t.Fatalf("metadata rollback update err=%v", err)
	}
	afterManifest, err := os.ReadFile(installedManifest)
	if err != nil || string(afterManifest) != string(originalManifest) {
		t.Fatalf("installed manifest changed after rollback: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "managed", "remote.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement resource leaked after rollback: %v", err)
	}
	afterMetadata, err := os.ReadFile(metadataPath)
	if err != nil || string(afterMetadata) != string(originalMetadata) {
		t.Fatalf("metadata changed after rollback: err=%v\nbefore=%s\nafter=%s", err, originalMetadata, afterMetadata)
	}
	assertNoSkillStages(t, root)
}

func TestSkillManagementUpdateRelocatesUnambiguousSkillAndGroupsAcquisition(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, filepath.Join("skills", "alpha"), "alpha")
	writeRepositorySkill(t, repository, filepath.Join("skills", "beta"), "beta")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("6", 40))
	if _, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo", All: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repository, "skills", "alpha"), filepath.Join(repository, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "alpha", "moved.txt"), []byte("moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "skills", "beta", "changed.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	acquisitions := 0
	service.AcquireRepository = func(context.Context, skills.GitHubSource) (acquiredSkillRepository, error) {
		acquisitions++
		return acquiredSkillRepository{Root: repository, Revision: strings.Repeat("7", 40)}, nil
	}
	result, err := service.Update(t.Context(), SkillUpdateRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if acquisitions != 1 || len(result.Skills) != 2 {
		t.Fatalf("acquisitions=%d result=%#v", acquisitions, result)
	}
	var alpha SkillUpdateItem
	for _, value := range result.Skills {
		if value.Name == "alpha" {
			alpha = value
		}
	}
	if !alpha.Relocated || alpha.PreviousPath != "skills/alpha" || alpha.Path != "alpha" || !alpha.Changed {
		t.Fatalf("relocated alpha=%#v", alpha)
	}
}

func TestSkillManagementUpdateRejectsAmbiguousRelocation(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, filepath.Join("skills", "alpha"), "alpha")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("8", 40))
	if _, err := service.Add(t.Context(), SkillAddRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Source: "owner/repo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repository, "skills")); err != nil {
		t.Fatal(err)
	}
	writeRepositorySkill(t, repository, filepath.Join("one", "alpha"), "alpha")
	writeRepositorySkill(t, repository, filepath.Join("two", "alpha"), "alpha")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("a", 40))
	if _, err := service.Update(t.Context(), SkillUpdateRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "alpha",
	}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous relocation err=%v", err)
	}
}

func TestSkillManagementRemoveManagedAndUnmanagedWithRollback(t *testing.T) {
	service, item := newSkillManagementHarness(t)
	repository := t.TempDir()
	writeRepositorySkill(t, repository, "managed", "managed")
	service.AcquireRepository = staticRepositoryAcquirer(repository, strings.Repeat("b", 40))
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
	service.BeforeMetadataCommit = func() error { return errors.New("forced remove metadata failure") }
	if _, err := service.Remove(SkillRemoveRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "managed",
	}); err == nil || !strings.Contains(err.Error(), "forced remove metadata failure") {
		t.Fatalf("managed removal rollback err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "managed", "SKILL.md")); err != nil {
		t.Fatalf("managed skill was lost after rollback: %v", err)
	}
	metadataAfter, err := os.ReadFile(metadataPath)
	if err != nil || string(metadataAfter) != string(metadataBefore) {
		t.Fatalf("managed metadata changed after rollback: err=%v", err)
	}
	service.BeforeMetadataCommit = nil
	removed, err := service.Remove(SkillRemoveRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "managed",
	})
	if err != nil || !removed.Managed {
		t.Fatalf("managed removal=%#v err=%v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "managed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed skill still exists: %v", err)
	}

	unmanaged := filepath.Join(root, "authored")
	if err := os.MkdirAll(unmanaged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmanaged, "SKILL.md"), []byte(skillFixture("authored", "authored")), 0o600); err != nil {
		t.Fatal(err)
	}
	unmanagedResult, err := service.Remove(SkillRemoveRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: "authored",
	})
	if err != nil || unmanagedResult.Managed {
		t.Fatalf("unmanaged removal=%#v err=%v", unmanagedResult, err)
	}
	if _, err := os.Stat(unmanaged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unmanaged skill still exists: %v", err)
	}
	if _, err := service.Remove(SkillRemoveRequest{
		Scope: SkillScopeRequest{WorkspaceID: item.ID}, Name: skills.BuiltinCreatePlanName,
	}); err == nil || !strings.Contains(err.Error(), "not removable") {
		t.Fatalf("builtin removal err=%v", err)
	}
}

func TestManagedSourceLegacyMetadataRemainsReadable(t *testing.T) {
	root := t.TempDir()
	legacy := []byte("{\n  \"schema\": 1,\n  \"skills\": {\n    \"legacy\": {\n      \"source\": \"github:owner/repo\",\n      \"revision\": \"" + strings.Repeat("c", 40) + "\",\n      \"path\": \"skills/legacy\"\n    }\n  }\n}\n")
	if err := os.WriteFile(filepath.Join(root, skills.ManagedSourcesFile), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := skills.ReadManagedSources(root)
	if err != nil {
		t.Fatal(err)
	}
	entry := metadata.Skills["legacy"]
	if entry.Ref != "" || entry.ContentHash != "" || entry.Path != "skills/legacy" {
		t.Fatalf("legacy metadata=%#v", metadata)
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
