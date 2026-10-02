package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestInstallCutoverCleanupSuccessIsSeparatedFromChildren(t *testing.T) {
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true})
	observe := installCutoverProgressObserver(session)
	observe(application.InstallCutoverEvent{Stage: "cleanup", State: "success", Message: "Telemetry ready", Child: true})
	observe(application.InstallCutoverEvent{Stage: "cleanup", State: "success", Message: "Install supplements processed"})
	session.CloseWith("Done")

	got := output.String()
	want := "│  ✓ Telemetry ready\n│\n◆  Install supplements processed"
	if !strings.Contains(got, want) {
		t.Fatalf("cleanup summary spacing mismatch: %q", got)
	}
}

func TestInstallCutoverUnsupportedStateUsesReadableHierarchy(t *testing.T) {
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true})
	observe := installCutoverProgressObserver(session)
	observe(application.InstallCutoverEvent{Stage: "detect", State: "warning", Message: "Previous CodeMCP state requires a clean install"})
	observe(application.InstallCutoverEvent{Stage: "detect", State: "warning", Message: "7 unsupported artifacts cannot be migrated", Child: true})
	observe(application.InstallCutoverEvent{Stage: "cleanup", State: "running", Message: "Removing previous CodeMCP state"})
	observe(application.InstallCutoverEvent{Stage: "cleanup", State: "success", Message: "Previous CodeMCP state removed"})
	session.CloseWith("Done")

	got := output.String()
	want := "!  Previous CodeMCP state requires a clean install\n│\n│  ! 7 unsupported artifacts cannot be migrated\n│\n◆  Previous CodeMCP state removed"
	if !strings.Contains(got, want) {
		t.Fatalf("unsupported-state presentation mismatch: %q", got)
	}
}
