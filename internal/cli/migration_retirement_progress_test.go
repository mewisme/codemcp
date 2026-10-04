package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/migration/released024"
)

func TestMigrationRetirementObserverRendersCleanupAndRetentionRail(t *testing.T) {
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{
		Width: 100, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true, Animation: true,
	})
	session.SetTitle("Retire released ChatGPT-MCP identities")
	observe := migrationRetirementProgressObserver(session)
	observe(released024.RetirementEvent{Stage: "canonical", State: "running", Message: "Verifying canonical runtime"})
	observe(released024.RetirementEvent{Stage: "canonical", State: "success", Message: "Canonical runtime verified"})
	observe(released024.RetirementEvent{Stage: "services", State: "running", Message: "Retiring services"})
	observe(released024.RetirementEvent{Stage: "services", State: "success", Message: "historical-user · retired", Child: true})
	observe(released024.RetirementEvent{Stage: "launchers", State: "warning", Message: "package-managed launcher · left untouched", Child: true})
	observe(released024.RetirementEvent{Stage: "retention", State: "success", Message: "Released snapshot retained until 2026-10-06T09:00:00Z"})
	observe(released024.RetirementEvent{Stage: "readiness", State: "running", Message: "Rechecking canonical runtime"})
	observe(released024.RetirementEvent{Stage: "readiness", State: "success", Message: "Canonical runtime remains ready"})
	session.Close()

	rendered := output.String()
	for _, want := range []string{
		"Retire released ChatGPT-MCP identities",
		"Verify CodeMCP",
		"Retire services",
		"historical-user · retired",
		"package-managed launcher · left untouched",
		"Released snapshot retained until",
		"Verify readiness",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("retirement progress missing %q:\n%s", want, rendered)
		}
	}
}
