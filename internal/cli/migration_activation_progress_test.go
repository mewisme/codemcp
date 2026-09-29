package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/migration/released024"
)

func TestMigrationActivationObserverRendersStagesChildrenAndRollback(t *testing.T) {
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true})
	observe := migrationActivationProgressObserver(session)
	observe(released024.ActivationEvent{Stage: "health", State: "running", Message: "Checking migrated state"})
	observe(released024.ActivationEvent{Stage: "health", State: "success", Message: "config · verified", Child: true})
	observe(released024.ActivationEvent{Stage: "health", State: "warning", Message: "workspaces · one unavailable", Child: true})
	observe(released024.ActivationEvent{Stage: "health", State: "failed", Message: "Health checks failed"})
	observe(released024.ActivationEvent{Stage: "rollback", State: "running", Message: "Rolling back activation"})
	observe(released024.ActivationEvent{Stage: "rollback", State: "success", Message: "Activation rolled back"})
	session.Close()

	rendered := output.String()
	for _, want := range []string{
		"Activate migrated CodeMCP state",
		"Health checks",
		"config · verified",
		"workspaces · one unavailable",
		"Rollback migration",
		"Activation rolled back",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("progress output missing %q:\n%s", want, rendered)
		}
	}
	if strings.Index(rendered, "Rollback migration") < strings.Index(rendered, "Health checks") {
		t.Fatalf("rollback stage rendered before health stage:\n%s", rendered)
	}
}
