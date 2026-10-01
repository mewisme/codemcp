package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	plandoc "go.mewis.me/codemcp/internal/plan"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func newPlanAuthoringHarness(t *testing.T) (*PlanAuthoringService, *workspace.Manager, workspace.Workspace) {
	t.Helper()
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewPlanAuthoringService(manager), manager, item
}

func TestPlanAuthoringCreateUpdateDryRunAndBoundedResult(t *testing.T) {
	service, _, item := newPlanAuthoringHarness(t)
	changes := instructioncontext.NewChangeStream()
	service.Changes = changes
	subscription, _ := changes.Subscribe(0)
	defer changes.Unsubscribe(subscription)

	planBody, orderBody := planAuthoringFixture(false)
	dry, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "oauth-redesign",
		PlanContent: planBody, ImplementationOrder: orderBody, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.Status != plandoc.StatusPending || dry.PhaseCount != 1 || dry.CompletedPhaseCount != 0 ||
		dry.NextPhase == nil || dry.NextPhase.ID != "1A" {
		t.Fatalf("dry result=%#v", dry)
	}
	if _, err := os.Stat(workspacestate.New(item.Path).PlansRoot()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run mutated plans root: %v", err)
	}
	select {
	case change := <-subscription.Events:
		t.Fatalf("dry run emitted change=%#v", change)
	default:
	}

	created, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "oauth-redesign",
		PlanContent: planBody, ImplementationOrder: orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.DryRun || created.ContentID != dry.ContentID {
		t.Fatalf("created=%#v dry=%#v", created, dry)
	}
	data, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	document, err := plandoc.Parse(data)
	if err != nil || document.ContentID() != created.ContentID {
		t.Fatalf("document id=%q result=%q err=%v", document.ContentID(), created.ContentID, err)
	}
	change := <-subscription.Events
	if change.Kind != "plan" || change.Scope != "workspace" || change.WorkspaceID != item.ID ||
		change.Name != "oauth-redesign" || change.Operation != "create" {
		t.Fatalf("change=%#v", change)
	}

	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "oauth-redesign",
		PlanContent: planBody, ImplementationOrder: orderBody,
	}); err == nil || !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("duplicate create error=%v", err)
	}
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "oauth-redesign",
		PlanContent: planBody, ImplementationOrder: orderBody,
	}); err == nil || !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("missing expected id error=%v", err)
	}

	completedPlan, completedOrder := planAuthoringFixture(true)
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "oauth-redesign",
		PlanContent: completedPlan, ImplementationOrder: completedOrder,
		ExpectedContentID: "sha256:stale", DryRun: true,
	}); err == nil || !errors.Is(err, ErrPlanStale) {
		t.Fatalf("stale dry-run error=%v", err)
	}
	updated, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "oauth-redesign",
		PlanContent: completedPlan, ImplementationOrder: completedOrder,
		ExpectedContentID: created.ContentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != plandoc.StatusCompleted || updated.CompletedPhaseCount != 1 || updated.NextPhase != nil {
		t.Fatalf("updated=%#v", updated)
	}
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "oauth-redesign",
		PlanContent: planBody, ImplementationOrder: orderBody,
		ExpectedContentID: updated.ContentID, DryRun: true,
	}); err == nil || !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("completed plan regression error=%v", err)
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("Implementation order")) || bytes.Contains(encoded, []byte("Persist the state")) ||
		bytes.Contains(encoded, []byte("plan_content")) || bytes.Contains(encoded, []byte("implementation_order")) {
		t.Fatalf("result leaked plan body: %s", encoded)
	}
}

func TestPlanAuthoringUpdateFailureRestoresPreviousPlan(t *testing.T) {
	service, _, item := newPlanAuthoringHarness(t)
	planBody, orderBody := planAuthoringFixture(false)
	created, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "rollback-plan",
		PlanContent: planBody, ImplementationOrder: orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	service.activationHook = func(point string) error {
		if point == "after-backup" {
			return errors.New("injected activation failure")
		}
		return nil
	}
	completedPlan, completedOrder := planAuthoringFixture(true)
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "rollback-plan",
		PlanContent: completedPlan, ImplementationOrder: completedOrder,
		ExpectedContentID: created.ContentID,
	}); err == nil || !strings.Contains(err.Error(), "injected activation failure") {
		t.Fatalf("update error=%v", err)
	}
	after, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed update did not restore previous plan")
	}
	entries, err := os.ReadDir(filepath.Dir(created.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "rollback-plan.md" {
		t.Fatalf("failed update left staging residue: %#v", entries)
	}
}

func TestPlanAuthoringRejectsSymlinkRootsAndPathSwap(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	service, _, item := newPlanAuthoringHarness(t)
	store := workspacestate.New(item.Path)
	outside := t.TempDir()
	if err := os.Symlink(outside, store.PlansRoot()); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	planBody, orderBody := planAuthoringFixture(false)
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "linked-plan",
		PlanContent: planBody, ImplementationOrder: orderBody,
	}); err == nil {
		t.Fatal("symlinked plans root was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "linked-plan.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("authoring escaped through symlink root: %v", err)
	}
	if err := os.Remove(store.PlansRoot()); err != nil {
		t.Fatal(err)
	}
	created, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "swap-plan",
		PlanContent: planBody, ImplementationOrder: orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	realPlans := store.PlansRoot() + ".real"
	service.activationHook = func(point string) error {
		if point != "before-activate" {
			return nil
		}
		if err := os.Rename(store.PlansRoot(), realPlans); err != nil {
			return err
		}
		return os.Symlink(outside, store.PlansRoot())
	}
	completedPlan, completedOrder := planAuthoringFixture(true)
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "swap-plan",
		PlanContent: completedPlan, ImplementationOrder: completedOrder,
		ExpectedContentID: created.ContentID,
	}); err == nil {
		t.Fatal("plans-root path swap was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "swap-plan.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path swap wrote outside plans root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(realPlans, "swap-plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := plandoc.Parse(data)
	if err != nil || document.ContentID() != created.ContentID {
		t.Fatalf("original plan changed: id=%q want=%q err=%v", document.ContentID(), created.ContentID, err)
	}
}

func TestPlanAuthoringDetectsMalformedOutOfBandState(t *testing.T) {
	service, _, item := newPlanAuthoringHarness(t)
	planBody, orderBody := planAuthoringFixture(false)
	created, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "manual-edit",
		PlanContent: planBody, ImplementationOrder: orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created.Path, []byte("# malformed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "manual-edit",
		PlanContent: planBody, ImplementationOrder: orderBody,
		ExpectedContentID: created.ContentID,
	}); err == nil || !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("malformed manual state error=%v", err)
	}
	data, err := os.ReadFile(created.Path)
	if err != nil || string(data) != "# malformed\n" {
		t.Fatalf("malformed state was mutated: err=%v data=%q", err, data)
	}
}

func TestPlanAuthoringRejectsNonWorkspaceAndPathLikeTargets(t *testing.T) {
	service, _, item := newPlanAuthoringHarness(t)
	planBody, orderBody := planAuthoringFixture(false)
	for _, request := range []PlanAuthoringRequest{
		{Mode: PlanCreate, Name: "missing-workspace", PlanContent: planBody, ImplementationOrder: orderBody},
		{WorkspaceID: item.ID, Mode: PlanCreate, Name: "../rules/escape", PlanContent: planBody, ImplementationOrder: orderBody},
		{WorkspaceID: item.ID, Mode: PlanCreate, Name: ".cm-rules", PlanContent: planBody, ImplementationOrder: orderBody},
	} {
		if _, err := service.Write(t.Context(), request); err == nil || !errors.Is(err, ErrPlanInvalid) {
			t.Fatalf("unsafe target accepted: request=%#v err=%v", request, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(item.Path).RulesRoot(), "escape.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path-like plan target escaped into rules: %v", err)
	}
}

func TestPlanAuthoringUpdateDetectsConcurrentCanonicalChange(t *testing.T) {
	service, _, item := newPlanAuthoringHarness(t)
	planBody, orderBody := planAuthoringFixture(false)
	created, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanCreate, Name: "concurrent-plan",
		PlanContent: planBody, ImplementationOrder: orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}

	externalPlan := "# External plan\n\n## Goal\nPreserve external state.\n\n## Phase 1A - Persist state\n\n- [ ] Persist the state.\n\n## Acceptance\nState is durable."
	externalOrder := "## Execution rules\nComplete the phase.\n\n## Why this order\nPersistence comes first.\n\n## Ordered phases\n\n- [ ] Phase 1A - Persist state\n\n## Terminal acceptance\n\n- [ ] Durable-state validation passes."
	externalDocument, err := plandoc.ParseParts(externalPlan, externalOrder)
	if err != nil {
		t.Fatal(err)
	}
	externalBytes := externalDocument.Render()
	service.activationHook = func(point string) error {
		if point != "before-activate" {
			return nil
		}
		return os.WriteFile(created.Path, externalBytes, 0o600)
	}
	completedPlan, completedOrder := planAuthoringFixture(true)
	if _, err := service.Write(t.Context(), PlanAuthoringRequest{
		WorkspaceID: item.ID, Mode: PlanUpdate, Name: "concurrent-plan",
		PlanContent: completedPlan, ImplementationOrder: completedOrder,
		ExpectedContentID: created.ContentID,
	}); err == nil || !errors.Is(err, ErrPlanStale) {
		t.Fatalf("concurrent plan change error=%v", err)
	}
	after, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, externalBytes) {
		t.Fatalf("concurrent plan was overwritten: %q", after)
	}
}

func planAuthoringFixture(completed bool) (string, string) {
	mark := " "
	if completed {
		mark = "x"
	}
	return "# Persisted plan\n\n## Goal\nPersist safely.\n\n## Phase 1A - Persist state\n\n- [" + mark + "] Persist the state.\n\n## Acceptance\nState is durable.",
		"## Execution rules\nComplete the phase.\n\n## Why this order\nPersistence comes first.\n\n## Ordered phases\n\n- [" + mark + "] Phase 1A - Persist state\n\n## Terminal acceptance\n\n- [" + mark + "] Durable-state validation passes."
}
