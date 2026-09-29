package released024

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

func TestRetireRequiresCanonicalReadinessBeforeHistoricalCleanup(t *testing.T) {
	h, activation := activatedRetirementHarness(t)
	launcherCalls := 0
	badProbe := func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
		return runtimecontrol.RuntimeStatus{
			Managed: true, ServiceID: h.manager.spec.ID, ServiceScope: string(h.manager.spec.Scope),
			ConfigRoot: h.fixture.sourceRoot,
		}, true, nil
	}
	_, err := Retire(t.Context(), RetireOptions{
		JournalPath: h.stage.JournalPath, ServiceManager: h.manager, RuntimeProbe: badProbe,
		ServiceRetirer: h.fixture.controller,
		RemoveLauncher: func(Launcher) (bool, error) { launcherCalls++; return true, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "canonical runtime ownership") {
		t.Fatalf("retirement error=%v", err)
	}
	if len(h.fixture.controller.retired) != 0 || launcherCalls != 0 {
		t.Fatalf("cleanup ran before canonical readiness: retired=%v launcherCalls=%d", h.fixture.controller.retired, launcherCalls)
	}
	if _, err := os.Stat(filepath.Join(h.fixture.sourceRoot, ".runtime-control.json")); err != nil {
		t.Fatalf("released runtime metadata changed before retirement: %v", err)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || journal.Phase != stagePhaseCommitted || journal.RetiredAt != nil {
		t.Fatalf("preflight failure mutated committed journal: %#v ok=%t", journal, ok)
	}
	if activation.RuntimeProbe == nil {
		t.Fatal("activation runtime probe unexpectedly missing")
	}
}

func TestRetireRemovesOwnedIdentitiesAndKeepsRollbackSnapshot(t *testing.T) {
	h, activation := activatedRetirementHarness(t)
	fixed := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	result, err := Retire(t.Context(), RetireOptions{
		JournalPath: h.stage.JournalPath, ServiceManager: h.manager, RuntimeProbe: activation.RuntimeProbe,
		ServiceRetirer: h.fixture.controller, Retention: 48 * time.Hour, Now: func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Retired || result.RetainUntil == nil || !result.RetainUntil.Equal(fixed.Add(48*time.Hour)) || result.RetainedSHA256 == "" {
		t.Fatalf("retirement result=%#v", result)
	}
	if len(h.fixture.controller.retired) != 1 || len(h.fixture.controller.restored) != 0 || h.fixture.controller.running {
		t.Fatalf("historical lifecycle retired=%v restored=%v running=%t", h.fixture.controller.retired, h.fixture.controller.restored, h.fixture.controller.running)
	}
	for _, relative := range []string{".runtime-control.json", "runtime/environment.json"} {
		if _, err := os.Stat(filepath.Join(h.fixture.sourceRoot, relative)); !os.IsNotExist(err) {
			t.Fatalf("historical runtime metadata %s remained: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(h.fixture.sourceRoot, "config.json")); err != nil {
		t.Fatalf("durable released rollback state was removed: %v", err)
	}
	if _, err := os.Stat(h.fixture.sourceRoot); err != nil {
		t.Fatalf("released rollback root was removed: %v", err)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || journal.Phase != stagePhaseRetired || journal.RetiredAt == nil || journal.RetainUntil == nil || journal.RetainedSHA256 == "" || journal.FailureStage != "" {
		t.Fatalf("retired journal=%#v ok=%t", journal, ok)
	}
	if journal.Canonical.ServiceID == "" || !sameComparablePath(journal.Canonical.ConfigRoot, h.fixture.targetRoot) || sameComparablePath(journal.Canonical.ConfigRoot, h.fixture.sourceRoot) {
		t.Fatalf("canonical retirement evidence=%#v", journal.Canonical)
	}
	if !hasRetirementOutcome(journal.Retirement, "retention", "success") || !hasRetirementOutcome(journal.Retirement, "readiness", "success") {
		t.Fatalf("retirement outcomes=%#v", journal.Retirement)
	}
}

func TestRetireFailureAfterHistoricalDeletionNeverRestoresDeletedIdentity(t *testing.T) {
	h, activation := activatedRetirementHarness(t)
	_, err := Retire(t.Context(), RetireOptions{
		JournalPath: h.stage.JournalPath, ServiceManager: h.manager, RuntimeProbe: activation.RuntimeProbe,
		ServiceRetirer: h.fixture.controller,
		InjectFailure: func(point string) error {
			if point == "services-retired" {
				return errors.New("injected retirement interruption")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected retirement interruption") {
		t.Fatalf("retirement error=%v", err)
	}
	if len(h.fixture.controller.retired) != 1 || len(h.fixture.controller.restored) != 0 {
		t.Fatalf("deleted historical service was restored: retired=%v restored=%v", h.fixture.controller.retired, h.fixture.controller.restored)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || journal.Phase != stagePhaseRetirementFailed || journal.FailureStage != "services" {
		t.Fatalf("partial retirement journal=%#v ok=%t", journal, ok)
	}

	result, err := Retire(t.Context(), RetireOptions{
		JournalPath: h.stage.JournalPath, ServiceManager: h.manager, RuntimeProbe: activation.RuntimeProbe,
		ServiceRetirer: h.fixture.controller,
	})
	if err != nil || !result.Retired {
		t.Fatalf("retirement retry result=%#v err=%v", result, err)
	}
	if len(h.fixture.controller.restored) != 0 {
		t.Fatalf("retry restored historical service: %v", h.fixture.controller.restored)
	}
}

func TestRetirePreservesChangedRuntimeMetadataAndUnownedLaunchers(t *testing.T) {
	h, activation := activatedRetirementHarness(t)
	runtimePath := filepath.Join(h.fixture.sourceRoot, ".runtime-control.json")
	if err := os.WriteFile(runtimePath, []byte(`{"pid":9999}`), 0600); err != nil {
		t.Fatal(err)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok {
		t.Fatal("committed migration journal missing")
	}
	journal.Launchers = []Launcher{
		{Path: filepath.Join(h.fixture.home, "package", "chatgpt-mcp"), Kind: "executable", Ownership: OwnershipPackageManager, PackageManaged: true},
		{Path: filepath.Join(h.fixture.home, "ambiguous", "cgm"), Kind: "alias", Ownership: OwnershipAmbiguous},
		{Path: filepath.Join(h.fixture.home, "owned", "cgm"), Target: filepath.Join(h.fixture.home, "owned", "chatgpt-mcp"), Kind: "alias", Ownership: OwnershipVerified, Removable: true},
	}
	journal.Services = append(journal.Services, ServiceState{ID: "unrelated-service", Installed: true, Running: true, Ownership: OwnershipAmbiguous})
	if err := writeStageJournal(h.stage.JournalPath, journal); err != nil {
		t.Fatal(err)
	}
	launcherCalls := 0
	result, err := Retire(t.Context(), RetireOptions{
		JournalPath: h.stage.JournalPath, ServiceManager: h.manager, RuntimeProbe: activation.RuntimeProbe,
		ServiceRetirer: h.fixture.controller,
		RemoveLauncher: func(launcher Launcher) (bool, error) {
			launcherCalls++
			if launcher.Ownership != OwnershipVerified || !launcher.Removable || launcher.PackageManaged {
				t.Fatalf("unsafe launcher reached remover: %#v", launcher)
			}
			return true, nil
		},
	})
	if err != nil || !result.Retired {
		t.Fatalf("retirement result=%#v err=%v", result, err)
	}
	if launcherCalls != 1 {
		t.Fatalf("launcher remover calls=%d", launcherCalls)
	}
	if len(h.fixture.controller.retired) != 1 {
		t.Fatalf("unverified historical service reached retirer: %v", h.fixture.controller.retired)
	}
	if data, err := os.ReadFile(runtimePath); err != nil || string(data) != `{"pid":9999}` {
		t.Fatalf("changed runtime metadata=%q err=%v", data, err)
	}
	journal, _ = loadStageJournal(h.stage.JournalPath)
	if !hasRetirementDetail(journal.Retirement, "warning", "changed after quiescence") {
		t.Fatalf("runtime drift warning missing: %#v", journal.Retirement)
	}
	if countRetirementState(journal.Retirement, "launcher", "warning") != 2 {
		t.Fatalf("unowned/package launchers were not reported: %#v", journal.Retirement)
	}
}

func TestStageJournalCapturesOnlyRetirementOwnedRuntimeArtifacts(t *testing.T) {
	fixture := newStageFixture(t, false)
	stage, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	journal, ok := loadStageJournal(stage.JournalPath)
	if !ok || len(journal.Artifacts) == 0 {
		t.Fatalf("retirement artifacts missing: %#v ok=%t", journal.Artifacts, ok)
	}
	kinds := map[string]bool{}
	for _, artifact := range journal.Artifacts {
		if !retirementRuntimeArtifact(artifact) || artifact.SHA256 == "" {
			t.Fatalf("unsafe retirement artifact=%#v", artifact)
		}
		kinds[artifact.Kind] = true
	}
	if !kinds["runtime-state"] || !kinds["service-environment"] {
		t.Fatalf("retirement artifact kinds=%#v", kinds)
	}
}

func activatedRetirementHarness(t *testing.T) (activationHarness, ActivateOptions) {
	t.Helper()
	h := newActivationHarness(t)
	activation := h.options(t)
	if _, err := Activate(t.Context(), activation); err != nil {
		t.Fatal(err)
	}
	return h, activation
}

func hasRetirementOutcome(outcomes []RetirementOutcome, kind, state string) bool {
	for _, outcome := range outcomes {
		if outcome.Kind == kind && outcome.State == state {
			return true
		}
	}
	return false
}

func hasRetirementDetail(outcomes []RetirementOutcome, state, detail string) bool {
	for _, outcome := range outcomes {
		if outcome.State == state && strings.Contains(outcome.Detail, detail) {
			return true
		}
	}
	return false
}

func countRetirementState(outcomes []RetirementOutcome, kind, state string) int {
	count := 0
	for _, outcome := range outcomes {
		if outcome.Kind == kind && outcome.State == state {
			count++
		}
	}
	return count
}
