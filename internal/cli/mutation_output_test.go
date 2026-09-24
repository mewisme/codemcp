package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestMutationProgressClosesBeforeFinalResultFrame(t *testing.T) {
	var output bytes.Buffer
	caps := presentation.Capabilities{Width: 100, Unicode: true, Color: false, Interactive: true}
	cmd := &cobra.Command{}
	cmd.SetOut(presentation.WrapWriter(&output, caps))
	cmd.SetErr(presentation.WrapWriter(&output, caps))

	beginMutationProgress(cmd, "Install CodeMCP")
	commandProgressSession(cmd).Success("install.binary", "Installing binary", "Binary installed")
	renderMutationSuccess(cmd, "Install CodeMCP", "Installation complete", presentation.Field{Label: "version", Value: "v1.2.3"})

	text := output.String()
	if strings.Count(text, "┌  Install CodeMCP") != 2 || strings.Count(text, "└") != 2 {
		t.Fatalf("progress/result frames were not cleanly separated: %q", text)
	}
	progressEnd := strings.Index(text, "└")
	resultStart := strings.LastIndex(text, "┌  Install CodeMCP")
	if progressEnd < 0 || resultStart <= progressEnd {
		t.Fatalf("final result began before progress frame closed: %q", text)
	}
	if strings.Count(text, "Installing binary") != 1 || strings.Count(text, "Installation complete") != 1 {
		t.Fatalf("terminal phases were duplicated: %q", text)
	}
}
