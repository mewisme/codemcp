package application

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func newInstructionAuthoringHarness(t *testing.T) (*InstructionAuthoringService, *workspace.Manager, workspace.Workspace) {
	t.Helper()
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewInstructionAuthoringService(manager, nil), manager, item
}

func TestInstructionAuthoringPublishesCanonicalChangesAfterMutation(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changes := instructioncontext.NewChangeStream()
	service := NewInstructionAuthoringService(manager, nil, changes)
	subscription, snapshot := changes.Subscribe(0)
	defer changes.Unsubscribe(subscription)
	if snapshot.LatestSequence != 0 {
		t.Fatalf("initial change snapshot=%#v", snapshot)
	}

	if _, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "dry-event", AlwaysApply: true, Content: "dry", DryRun: true,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-subscription.Events:
		t.Fatalf("dry run emitted change=%#v", event)
	default:
	}

	if _, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "event-skill", Description: "event", Instructions: "body",
	}); err != nil {
		t.Fatal(err)
	}
	event := <-subscription.Events
	if event.Sequence != 1 || event.Kind != "skill" || event.Scope != "workspace" ||
		event.WorkspaceID != item.ID || event.Name != "event-skill" || event.Operation != "create" {
		t.Fatalf("instruction change=%#v", event)
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(item.Path).SkillsRoot(), "event-skill", "SKILL.md")); err != nil {
		t.Fatalf("change was published before active state existed: %v", err)
	}

	if _, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "event-skill", Description: "duplicate", Instructions: "must fail",
	}); err == nil {
		t.Fatal("duplicate create unexpectedly succeeded")
	}
	select {
	case event := <-subscription.Events:
		t.Fatalf("failed mutation emitted change=%#v", event)
	default:
	}
}

func TestAgentInstructionAuthoringUsesCanonicalResultAndErrorSemantics(t *testing.T) {
	service, manager, item := newInstructionAuthoringHarness(t)
	provider := NewAgentInstructionAuthoringProvider(manager)
	provider.service = service

	base := map[string]any{
		"scope":        "workspace",
		"workspace_id": item.ID,
		"mode":         "create",
		"name":         "provider-dry",
		"content":      "provider body",
		"always_apply": true,
		"dry_run":      true,
	}
	value, err := provider.AuthorRule(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	dry, ok := value.(InstructionAuthoringResult)
	if !ok || !dry.DryRun || dry.Scope != InstructionScopeWorkspace || dry.Mode != InstructionCreate || dry.ContentID == "" {
		t.Fatalf("dry-run semantic result=%#v", value)
	}
	if _, err := os.Stat(dry.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run mutated authored rule path: %v", err)
	}

	invalid := cloneAuthoringArgs(base)
	invalid["scope"] = "global"
	if _, err := provider.AuthorRule(t.Context(), invalid); ErrorSemanticsOf(err) != (ErrorSemantics{Code: ErrorInvalidArgument}) {
		t.Fatalf("invalid scope semantics=%#v err=%v", ErrorSemanticsOf(err), err)
	}

	missing := cloneAuthoringArgs(base)
	missing["mode"] = "update"
	missing["name"] = "provider-missing"
	missing["dry_run"] = false
	if _, err := provider.AuthorRule(t.Context(), missing); ErrorSemanticsOf(err) != (ErrorSemantics{Code: ErrorNotFound}) {
		t.Fatalf("missing update semantics=%#v err=%v", ErrorSemanticsOf(err), err)
	}

	create := cloneAuthoringArgs(base)
	create["name"] = "provider-live"
	create["dry_run"] = false
	if _, err := provider.AuthorRule(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.AuthorRule(t.Context(), create); ErrorSemanticsOf(err) != (ErrorSemantics{Code: ErrorConflict}) {
		t.Fatalf("duplicate create semantics=%#v err=%v", ErrorSemanticsOf(err), err)
	}

	staleCreate := cloneAuthoringArgs(base)
	staleCreate["name"] = "provider-stale"
	staleCreate["dry_run"] = false
	if _, err := provider.AuthorRule(t.Context(), staleCreate); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(workspacestate.New(item.Path).RulesRoot(), "provider-stale.md")
	hookCalled := false
	provider.service.activationHook = func(point string) error {
		if point != "before-activate" || hookCalled {
			return nil
		}
		hookCalled = true
		return os.WriteFile(stalePath, []byte("---\nalways_apply: true\n---\nexternal provider change\n"), 0o600)
	}
	staleUpdate := cloneAuthoringArgs(staleCreate)
	staleUpdate["mode"] = "update"
	staleUpdate["content"] = "replacement"
	_, err = provider.AuthorRule(t.Context(), staleUpdate)
	if got := ErrorSemanticsOf(err); got != (ErrorSemantics{Code: ErrorConflict, Retryable: true, Stale: true}) {
		t.Fatalf("stale update semantics=%#v err=%v", got, err)
	}
}

func cloneAuthoringArgs(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func TestInstructionAuthoringWorkspaceCreateUpdateDryRunAndResolverVisibility(t *testing.T) {
	service, _, item := newInstructionAuthoringHarness(t)
	store := workspacestate.New(item.Path)

	providerBefore := map[string][]byte{}
	for _, provider := range []string{".agents", ".claude", ".cursor", ".codex", ".newagent"} {
		rulePath := filepath.Join(item.Path, provider, "rules", "provider.md")
		if err := os.MkdirAll(filepath.Dir(rulePath), 0o755); err != nil {
			t.Fatal(err)
		}
		ruleBody := []byte("provider rule " + provider)
		if err := os.WriteFile(rulePath, ruleBody, 0o644); err != nil {
			t.Fatal(err)
		}
		providerBefore[rulePath] = append([]byte(nil), ruleBody...)

		skillPath := filepath.Join(item.Path, provider, "skills", strings.TrimPrefix(provider, "."), "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
			t.Fatal(err)
		}
		skillBody := []byte("---\nname: " + strings.TrimPrefix(provider, ".") + "\ndescription: provider\n---\nprovider body\n")
		if err := os.WriteFile(skillPath, skillBody, 0o644); err != nil {
			t.Fatal(err)
		}
		providerBefore[skillPath] = append([]byte(nil), skillBody...)
	}

	dryRule, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "dry-rule", AlwaysApply: true, Content: "dry content", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dryRule.DryRun || dryRule.ContentID == "" {
		t.Fatalf("dry rule result=%#v", dryRule)
	}
	if _, err := os.Stat(filepath.Join(store.RulesRoot(), "dry-rule.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run mutated rule root: %v", err)
	}

	createdRule, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "workspace-rule", AlwaysApply: true, Content: "workspace rule v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if createdRule.Path != filepath.Join(store.RulesRoot(), "workspace-rule.md") || createdRule.ContentID == "" || createdRule.DryRun {
		t.Fatalf("created rule=%#v", createdRule)
	}
	if _, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "workspace-rule", AlwaysApply: true, Content: "must not overwrite",
	}); err == nil {
		t.Fatal("duplicate create overwrote existing rule")
	}
	if _, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionUpdate, WorkspaceID: item.ID,
		Name: "workspace-rule", Globs: []string{"src/**/*.go"}, Content: "workspace rule v2",
	}); err != nil {
		t.Fatal(err)
	}

	discoveredRules, err := rules.Discover(item.Path)
	if err != nil {
		t.Fatal(err)
	}
	var authoredRule rules.Rule
	for _, value := range discoveredRules {
		if value.Path == createdRule.Path {
			authoredRule = value
			break
		}
	}
	if authoredRule.Source != ".cm" || authoredRule.Content != "workspace rule v2" || authoredRule.AlwaysApply || len(authoredRule.Patterns) != 1 || authoredRule.Patterns[0] != "src/**/*.go" {
		t.Fatalf("authored rule not visible through canonical resolver: %#v", authoredRule)
	}

	drySkill, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "dry-skill", Description: "Dry skill", Instructions: "dry body", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !drySkill.DryRun || drySkill.ContentID == "" {
		t.Fatalf("dry skill result=%#v", drySkill)
	}
	if _, err := os.Stat(filepath.Join(store.SkillsRoot(), "dry-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run mutated skill root: %v", err)
	}

	createdSkill, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "workspace-skill", Description: "Workspace skill", Instructions: "skill body v1",
		SupportingFiles: []SkillSupportingFile{
			{Path: "scripts/check.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Executable: true},
			{Path: "notes.md", Content: []byte("notes\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if createdSkill.Path != filepath.Join(store.SkillsRoot(), "workspace-skill") || createdSkill.ContentID == "" {
		t.Fatalf("created skill=%#v", createdSkill)
	}
	executableInfo, err := os.Stat(filepath.Join(createdSkill.Path, "scripts", "check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	plainInfo, err := os.Stat(filepath.Join(createdSkill.Path, "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && (executableInfo.Mode().Perm() != 0o700 || plainInfo.Mode().Perm() != 0o600) {
		t.Fatalf("supporting modes executable=%o plain=%o", executableInfo.Mode().Perm(), plainInfo.Mode().Perm())
	}

	if _, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionUpdate, WorkspaceID: item.ID,
		Name: "workspace-skill", Description: "Workspace skill updated", Instructions: "skill body v2",
		SupportingFiles: []SkillSupportingFile{{Path: "new.txt", Content: []byte("new")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(createdSkill.Path, "notes.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale supporting file survived tree replacement: %v", err)
	}
	loadedSkill, err := skills.Load(item.Path, "workspace-skill", 200_000)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSkill.Skill.Source != ".cm" || loadedSkill.Skill.Description != "Workspace skill updated" || !strings.Contains(loadedSkill.Content, "skill body v2") {
		t.Fatalf("authored skill not visible through canonical resolver: %#v", loadedSkill)
	}

	for providerPath, before := range providerBefore {
		after, err := os.ReadFile(providerPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("native authoring mutated provider-owned instruction path %s", providerPath)
		}
	}
}

func TestInstructionAuthoringGlobalRequiresOperatorAndPreservesManagedPolicy(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, filepath.Join(t.TempDir(), "config"))
	policy := instructionpolicy.DefaultConfig()
	policy.Context = "managed context"
	policy.Rules = []instructionpolicy.GlobalRule{{ID: "managed", Enabled: true, Content: "managed rule"}}
	if err := instructionpolicy.DefaultStore().Save(policy); err != nil {
		t.Fatal(err)
	}
	policyBefore, err := os.ReadFile(instructionpolicy.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}

	denied := NewInstructionAuthoringService(nil, nil)
	if _, err := denied.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeGlobal, Mode: InstructionCreate, Name: "global-rule", AlwaysApply: true, Content: "global native",
	}); err == nil {
		t.Fatal("global authoring succeeded without operator authorization")
	}

	allowed := NewInstructionAuthoringService(nil, func(context.Context) bool { return true })
	ruleResult, err := allowed.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeGlobal, Mode: InstructionCreate, Name: "global-rule", AlwaysApply: true, Content: "global native",
	})
	if err != nil {
		t.Fatal(err)
	}
	skillResult, err := allowed.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeGlobal, Mode: InstructionCreate, Name: "global-skill",
		Description: "Global skill", Instructions: "global skill body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ruleResult.Path, filepath.Join(configformat.RootPath(), "rules")+string(filepath.Separator)) ||
		!strings.HasPrefix(skillResult.Path, filepath.Join(configformat.RootPath(), "skills")+string(filepath.Separator)) {
		t.Fatalf("global results escaped native roots: rule=%#v skill=%#v", ruleResult, skillResult)
	}

	loadedRules, err := rules.DiscoverUser(t.TempDir(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedRules) != 1 || loadedRules[0].Source != ".cm" || loadedRules[0].Content != "global native" {
		t.Fatalf("global native rule=%#v", loadedRules)
	}
	loadedSkills, err := skills.DiscoverUser(t.TempDir(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedSkills) != 4 || loadedSkills[0].Source != ".cm" || loadedSkills[0].Name != "global-skill" ||
		!skills.IsBuiltin(loadedSkills[1]) || !skills.IsBuiltin(loadedSkills[2]) || !skills.IsBuiltin(loadedSkills[3]) {
		t.Fatalf("global native skill=%#v", loadedSkills)
	}
	policyAfter, err := os.ReadFile(instructionpolicy.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(policyBefore, policyAfter) {
		t.Fatal("native authoring rewrote managed instruction policy")
	}
}

func TestInstructionAuthoringValidationRejectsUnsafeOrOversizeRequests(t *testing.T) {
	service, _, item := newInstructionAuthoringHarness(t)
	for _, request := range []RuleAuthoringRequest{
		{Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID, Name: "../escape", AlwaysApply: true, Content: "body"},
		{Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID, Name: "mixed", AlwaysApply: true, Globs: []string{"*.go"}, Content: "body"},
		{Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID, Name: "missing-glob", Content: "body"},
		{Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID, Name: "bad-glob", Globs: []string{"src\\*.go"}, Content: "body"},
		{Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID, Name: "oversize", AlwaysApply: true, Content: strings.Repeat("x", maxAuthoredRuleBytes+1)},
	} {
		if _, err := service.WriteRule(t.Context(), request); err == nil {
			t.Fatalf("unsafe rule request accepted: %#v", request)
		}
	}

	badPaths := []string{"../escape", "/absolute", "C:/volume", "dir\\file", "SKILL.md", "a/../../escape"}
	for _, badPath := range badPaths {
		request := SkillAuthoringRequest{
			Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
			Name:        "skill-" + strings.Trim(strings.ReplaceAll(strings.ReplaceAll(badPath, "/", "-"), "\\", "-"), "-."),
			Description: "Skill", Instructions: "body",
			SupportingFiles: []SkillSupportingFile{{Path: badPath, Content: []byte("x")}},
		}
		request.Name = "unsafe-skill"
		if _, err := service.WriteSkill(t.Context(), request); err == nil {
			t.Fatalf("unsafe supporting path accepted: %q", badPath)
		}
	}

	if _, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "oversize-skill", Description: "Skill", Instructions: "body",
		SupportingFiles: []SkillSupportingFile{{Path: "large.bin", Content: bytes.Repeat([]byte("x"), maxAuthoredSkillFileBytes+1)}},
	}); err == nil {
		t.Fatal("oversize supporting file accepted")
	}

	manyFiles := make([]SkillSupportingFile, maxAuthoredSkillFiles+1)
	for i := range manyFiles {
		manyFiles[i] = SkillSupportingFile{Path: "f-" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + ".txt", Content: []byte("x")}
	}
	if _, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "many-files", Description: "Skill", Instructions: "body", SupportingFiles: manyFiles,
	}); err == nil {
		t.Fatal("too many supporting files accepted")
	}
}

func TestInstructionAuthoringUpdateDetectsSourceChangeAndPreservesConcurrentContent(t *testing.T) {
	service, _, item := newInstructionAuthoringHarness(t)
	created, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "race-rule", AlwaysApply: true, Content: "original",
	})
	if err != nil {
		t.Fatal(err)
	}
	hookCalled := false
	service.activationHook = func(point string) error {
		if point != "before-activate" || hookCalled {
			return nil
		}
		hookCalled = true
		return os.WriteFile(created.Path, []byte("---\nalways_apply: true\n---\nexternal change\n"), 0o600)
	}
	if _, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionUpdate, WorkspaceID: item.ID,
		Name: "race-rule", AlwaysApply: true, Content: "replacement",
	}); err == nil || !errors.Is(err, ErrInstructionStale) || !strings.Contains(err.Error(), "changed before update activation") {
		t.Fatalf("source change was not rejected as stale: %v", err)
	}
	data, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "external change") || strings.Contains(string(data), "replacement") {
		t.Fatalf("concurrent content was overwritten: %q", data)
	}
}

func TestInstructionAuthoringSkillUpdateFailureRestoresPreviousTree(t *testing.T) {
	service, _, item := newInstructionAuthoringHarness(t)
	created, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "rollback-skill", Description: "Old description", Instructions: "old body",
		SupportingFiles: []SkillSupportingFile{{Path: "old.txt", Content: []byte("old")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeMain, _ := os.ReadFile(filepath.Join(created.Path, "SKILL.md"))
	beforeSupport, _ := os.ReadFile(filepath.Join(created.Path, "old.txt"))

	service.activationHook = func(point string) error {
		if point == "after-backup" {
			return errors.New("injected activation failure")
		}
		return nil
	}
	if _, err := service.WriteSkill(t.Context(), SkillAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionUpdate, WorkspaceID: item.ID,
		Name: "rollback-skill", Description: "New description", Instructions: "new body",
		SupportingFiles: []SkillSupportingFile{{Path: "new.txt", Content: []byte("new")}},
	}); err == nil || !strings.Contains(err.Error(), "injected activation failure") {
		t.Fatalf("expected injected failure, got %v", err)
	}
	afterMain, err := os.ReadFile(filepath.Join(created.Path, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	afterSupport, err := os.ReadFile(filepath.Join(created.Path, "old.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeMain, afterMain) || !bytes.Equal(beforeSupport, afterSupport) {
		t.Fatal("failed update did not restore previous skill tree")
	}
	if _, err := os.Stat(filepath.Join(created.Path, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed update leaked new tree: %v", err)
	}
}

func TestInstructionAuthoringRejectsSymlinkRootsAndPathSwap(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	service, _, item := newInstructionAuthoringHarness(t)
	store := workspacestate.New(item.Path)

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(store.RulesRoot()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, store.RulesRoot()); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "linked-root", AlwaysApply: true, Content: "body",
	}); err == nil {
		t.Fatal("symlink rule root accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "linked-root.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("authoring escaped through symlink root: %v", err)
	}
	if err := os.Remove(store.RulesRoot()); err != nil {
		t.Fatal(err)
	}

	created, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionCreate, WorkspaceID: item.ID,
		Name: "swap-rule", AlwaysApply: true, Content: "old",
	})
	if err != nil {
		t.Fatal(err)
	}
	realRules := store.RulesRoot() + ".real"
	service.activationHook = func(point string) error {
		if point != "before-activate" {
			return nil
		}
		if err := os.Rename(store.RulesRoot(), realRules); err != nil {
			return err
		}
		return os.Symlink(outside, store.RulesRoot())
	}
	if _, err := service.WriteRule(t.Context(), RuleAuthoringRequest{
		Scope: InstructionScopeWorkspace, Mode: InstructionUpdate, WorkspaceID: item.ID,
		Name: "swap-rule", AlwaysApply: true, Content: "new",
	}); err == nil {
		t.Fatal("resource-root path swap was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "swap-rule.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path swap wrote outside native root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(realRules, filepath.Base(created.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "old") || strings.Contains(string(data), "new") {
		t.Fatalf("path swap changed original artifact: %q", data)
	}
}
