package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestMutationProgressAndResultShareOneFrame(t *testing.T) {
	var output bytes.Buffer
	caps := presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}
	cmd := &cobra.Command{}
	cmd.SetOut(presentation.WrapWriter(&output, caps))
	cmd.SetErr(presentation.WrapWriter(&output, caps))

	beginMutationProgress(cmd, "Install CodeMCP")
	commandProgressSession(cmd).Success("install.binary", "Installing binary", "Installed binary")
	renderMutationSuccess(cmd, "Install CodeMCP", "Installation complete", presentation.Field{Label: "version", Value: "v1.2.3"})

	text := output.String()
	if strings.Count(text, "┌  Install CodeMCP") != 1 || strings.Count(text, "└  Done") != 1 {
		t.Fatalf("progress/result did not share one frame: %q", text)
	}
	progress := strings.Index(text, "Installed binary")
	result := strings.Index(text, "Installation complete")
	done := strings.Index(text, "└  Done")
	if progress < 0 || result <= progress || done <= result {
		t.Fatalf("progress/result ordering is not continuous: %q", text)
	}
	if strings.Count(text, "Installed binary") != 1 || strings.Count(text, "Installation complete") != 1 {
		t.Fatalf("terminal phases were duplicated: %q", text)
	}
}

func TestMutationProgressAndResultShareOnePlainBlock(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	beginMutationProgress(cmd, "Install CodeMCP")
	commandProgressSession(cmd).Success("install.binary", "Installing binary", "Installed binary")
	renderMutationSuccess(cmd, "Install CodeMCP", "Installation complete", presentation.Field{Label: "version", Value: "v1.2.3"})

	text := output.String()
	if strings.Count(text, "Install CodeMCP") != 1 {
		t.Fatalf("plain mutation title duplicated: %q", text)
	}
	if !strings.Contains(text, "Installed binary") || !strings.Contains(text, "Installation complete") || !strings.HasSuffix(text, "Done\n") {
		t.Fatalf("plain mutation block is incomplete: %q", text)
	}
}
