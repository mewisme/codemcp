package released024

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
)

func TestRepresentativeReleasedInstallCutover(t *testing.T) {
	h, activation := activatedRetirementHarness(t)
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
	if !cfg.Auth.MCPLegacyBearer {
		t.Fatal("released MCP legacy bearer compatibility setting was not preserved")
	}
	if _, err := os.Stat(filepath.Join(h.fixture.workspaceRoot, ".cm", "workspace.json")); err != nil {
		t.Fatalf("workspace-local migrated identity missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.fixture.sourceRoot, "config.json")); err != nil {
		t.Fatalf("released rollback source was not preserved: %v", err)
	}
	journal, ok := loadStageJournal(h.stage.JournalPath)
	if !ok || journal.Phase != stagePhaseRetired || journal.Canonical.ServiceID == "" {
		t.Fatalf("release cutover journal=%#v ok=%t", journal, ok)
	}
}
