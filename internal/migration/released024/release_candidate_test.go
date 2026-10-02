package released024

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
)

func TestRepresentativeReleasedInstallCutover(t *testing.T) {
	h := newProductionActivationHarness(t)
	if h.fixture.manifest.Unsupported != 0 {
		t.Fatalf("representative production fixture is not migration-safe: unsupported=%d artifacts=%#v", h.fixture.manifest.Unsupported, h.fixture.manifest.Artifacts)
	}
	activation := h.options(t)
	if _, err := Activate(t.Context(), activation); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	result, err := Retire(t.Context(), RetireOptions{
		JournalPath:    h.stage.JournalPath,
		ServiceManager: h.manager,
		RuntimeProbe:   activation.RuntimeProbe,
		ServiceRetirer: h.fixture.controller,
		Now:            func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Retired || result.RetainUntil == nil {
		t.Fatalf("retirement result=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(h.fixture.targetRoot, "config.json")); err != nil {
		t.Fatalf("canonical migrated config missing: %v", err)
	}
	cfg, err := config.LoadAt(h.fixture.targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HTTP.MCP.Auth.LegacyBearer {
		t.Fatal("released MCP legacy bearer compatibility setting was not preserved")
	}
	if _, err := os.Stat(filepath.Join(h.fixture.workspaceRoot, ".cm", "workspace.json")); err != nil {
		t.Fatalf("workspace-local migrated identity missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.fixture.sourceRoot, "config.json")); err != nil {
		t.Fatalf("released rollback source was not preserved: %v", err)
	}
	largeSourceCheckpoint := filepath.Join(h.fixture.sourceRoot, "workspaces", "ws_legacy", "checkpoints", "data", "cp_large", "manifest.json")
	if info, err := os.Stat(largeSourceCheckpoint); err != nil || info.Size() != maxRegularFileBytes+1 {
		t.Fatalf("oversized rollback checkpoint was not preserved: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(h.fixture.workspaceRoot, ".cm", "checkpoints", "data", "cp_large")); !os.IsNotExist(err) {
		t.Fatalf("oversized legacy checkpoint was activated instead of skipped: %v", err)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || journal.Phase != stagePhaseRetired || journal.Canonical.ServiceID == "" {
		t.Fatalf("release cutover journal=%#v ok=%t", journal, ok)
	}
	foundSkipReport := false
	for _, outcome := range journal.Workspaces {
		if outcome.LegacyID == "ws_legacy" && strings.Contains(outcome.Detail, "oversized legacy checkpoint") {
			foundSkipReport = true
		}
	}
	if !foundSkipReport {
		t.Fatalf("release cutover did not report skipped oversized checkpoint: %#v", journal.Workspaces)
	}
}
