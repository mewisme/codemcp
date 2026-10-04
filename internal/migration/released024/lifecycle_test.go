package released024

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
)

func TestCleanupForFreshInstallRemovesOwnedStageAndPredecessor(t *testing.T) {
	fixture := newStageFixture(t, false)
	stage, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := configformat.MarkRoot(fixture.targetRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.targetRoot, "migration-owned.txt"), []byte("remove"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := &fakeActivationServiceManager{}

	result, err := CleanupForFreshInstall(t.Context(), FreshInstallCleanupOptions{
		TargetRoot:     fixture.targetRoot,
		ServiceManager: manager,
		SourceOptions: Options{
			HomeDir:    fixture.home,
			SourceRoot: fixture.sourceRoot,
			LookupEnv:  func(string) string { return "" },
			FindInstallations: func(install.Layout, string) ([]install.LegacyInstallation, error) {
				return nil, nil
			},
			FindAliases: func() ([]install.LegacyAlias, error) { return nil, nil },
			InspectServices: func(context.Context, SourceDescriptor) ([]ServiceState, error) {
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TransactionsRemoved != 1 || result.StagesRemoved != 1 || result.TargetsRemoved != 1 || result.SourcesRemoved != 1 {
		t.Fatalf("cleanup result=%#v", result)
	}
	for _, path := range []string{stage.StageRoot, stage.JournalPath, fixture.targetRoot, fixture.sourceRoot} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup left %s: %v", path, err)
		}
	}
}

func TestCleanupRetainedBackupHonorsRetentionAndIsIdempotent(t *testing.T) {
	h, activation := activatedRetirementHarness(t)
	fixed := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	retired, err := Retire(t.Context(), RetireOptions{
		JournalPath: h.stage.JournalPath, ServiceManager: h.manager, RuntimeProbe: activation.RuntimeProbe,
		ServiceRetirer: h.fixture.controller, Retention: 48 * time.Hour, Now: func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	if retired.RetainedSHA256 == "" {
		t.Fatal("retirement did not fingerprint retained rollback state")
	}

	_, err = CleanupRetainedBackup(t.Context(), CleanupOptions{
		JournalPath: h.stage.JournalPath,
		Now:         func() time.Time { return fixed.Add(24 * time.Hour) },
	})
	if err == nil || !strings.Contains(err.Error(), "protected until") {
		t.Fatalf("early cleanup error=%v", err)
	}
	if _, err := os.Stat(h.fixture.sourceRoot); err != nil {
		t.Fatalf("early cleanup removed rollback source: %v", err)
	}

	cleaned, err := CleanupRetainedBackup(t.Context(), CleanupOptions{
		JournalPath: h.stage.JournalPath,
		Now:         func() time.Time { return fixed.Add(49 * time.Hour) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cleaned.BackupRemoved || cleaned.CleanedAt == nil {
		t.Fatalf("cleanup result=%#v", cleaned)
	}
	if _, err := os.Stat(h.fixture.sourceRoot); !os.IsNotExist(err) {
		t.Fatalf("retained backup still exists: %v", err)
	}

	repeated, err := CleanupRetainedBackup(t.Context(), CleanupOptions{
		JournalPath: h.stage.JournalPath,
		Now:         func() time.Time { return fixed.Add(72 * time.Hour) },
	})
	if err != nil || !repeated.AlreadyClean {
		t.Fatalf("cleanup rerun=%#v err=%v", repeated, err)
	}
	status, err := InspectStatus(h.fixture.targetRoot, fixed.Add(72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if status.Cleaned != 1 || status.RetainedBackups != 0 || status.MissingBackups != 0 {
		t.Fatalf("migration status=%#v", status)
	}
}

func TestFingerprintRetainedTreeStreamsFilesLargerThanMigrationInspectionLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "workspaces", "ws_one", "checkpoints", "data", "cp_large", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxRegularFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "config.json"), "{}")

	first, err := fingerprintRetainedTree(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fingerprintRetainedTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("retained fingerprint is not deterministic: first=%q second=%q", first, second)
	}
}

func TestCleanupRetainedBackupRefusesChangedRollbackState(t *testing.T) {
	h, activation := activatedRetirementHarness(t)
	fixed := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	if _, err := Retire(t.Context(), RetireOptions{
		JournalPath: h.stage.JournalPath, ServiceManager: h.manager, RuntimeProbe: activation.RuntimeProbe,
		ServiceRetirer: h.fixture.controller, Retention: time.Hour, Now: func() time.Time { return fixed },
	}); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(h.fixture.sourceRoot, "operator-note.txt")
	if err := os.WriteFile(changed, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := CleanupRetainedBackup(t.Context(), CleanupOptions{
		JournalPath: h.stage.JournalPath,
		Now:         func() time.Time { return fixed.Add(2 * time.Hour) },
	})
	if err == nil || !strings.Contains(err.Error(), "changed after retirement") {
		t.Fatalf("changed backup cleanup error=%v", err)
	}
	if data, err := os.ReadFile(changed); err != nil || string(data) != "keep" {
		t.Fatalf("changed rollback state was modified: data=%q err=%v", data, err)
	}
}

func TestRecoverInterruptedStagingAllowsSafeRerun(t *testing.T) {
	fixture := newStageFixture(t, true)
	stage, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	journal, ok := loadStageJournal(stage.JournalPath)
	if !ok {
		t.Fatal("staging journal missing")
	}
	journal.Phase = stagePhaseStaging
	journal.FailureStage = "simulated-crash"
	if err := writeStageJournal(stage.JournalPath, journal); err != nil {
		t.Fatal(err)
	}

	recovery, err := RecoverInterrupted(t.Context(), RecoveryOptions{
		JournalPath: stage.JournalPath, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !recovery.ReadyForRetry || recovery.Phase != stagePhaseFailed {
		t.Fatalf("recovery=%#v", recovery)
	}
	if _, err := os.Stat(stage.StageRoot); !os.IsNotExist(err) {
		t.Fatalf("staging recovery left staged state: %v", err)
	}
	if !fixture.controller.running {
		t.Fatal("historical service was not restored before retry")
	}

	rerun, err := Stage(t.Context(), StageOptions{
		Manifest: fixture.manifest, TargetRoot: fixture.targetRoot,
		RefreshManifest: fixture.refresh, ServiceControl: fixture.controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rerun.AlreadyStaged || rerun.StagedSHA256 == "" {
		t.Fatalf("rerun stage=%#v", rerun)
	}
}

func TestRecoverInterruptedActivationPreservesOperatorWorkspaceChange(t *testing.T) {
	h := newActivationHarness(t)
	options := h.options(t)
	options.InjectFailure = func(point string) error {
		if point != "workspaces-activated" {
			return nil
		}
		path := filepath.Join(h.fixture.workspaceRoot, ".cm", "operator-change.txt")
		if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
		return errors.New("simulated interruption")
	}
	if _, err := Activate(t.Context(), options); err == nil || !strings.Contains(err.Error(), "requires recovery") {
		t.Fatalf("activation interruption error=%v", err)
	}

	recovery, err := RecoverInterrupted(t.Context(), RecoveryOptions{
		JournalPath: h.stage.JournalPath, ServiceControl: h.fixture.controller, ServiceManager: h.manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !recovery.ReadyForRetry || recovery.Phase != stagePhaseStaged {
		t.Fatalf("recovery=%#v", recovery)
	}
	operatorPath := filepath.Join(h.fixture.workspaceRoot, ".cm", "operator-change.txt")
	if data, err := os.ReadFile(operatorPath); err != nil || string(data) != "preserve" {
		t.Fatalf("operator workspace change after recovery=%q err=%v", data, err)
	}

	result, err := Activate(t.Context(), h.options(t))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed {
		t.Fatalf("retry activation=%#v", result)
	}
	if data, err := os.ReadFile(operatorPath); err != nil || string(data) != "preserve" {
		t.Fatalf("operator workspace change after retry=%q err=%v", data, err)
	}
}
