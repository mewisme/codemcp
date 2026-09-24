package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestHelpAlwaysUsesDefaultCobraRenderer(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"workspace", "--help"}} {
		root := newRootCommand()
		var output bytes.Buffer
		writer := presentation.WrapWriter(&output, presentation.Capabilities{
			StdoutTTY: true, StderrTTY: true, Width: 120, Unicode: true, RawUnicode: true, Interactive: true, Color: true,
		})
		root.SetOut(writer)
		root.SetErr(writer)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("args=%v: %v", args, err)
		}

		text := output.String()
		for _, expected := range []string{"Usage:", "Available Commands:", "Flags:"} {
			if !strings.Contains(text, expected) {
				t.Fatalf("default Cobra help missing %q for %v:\n%s", expected, args, text)
			}
		}
		for _, custom := range []string{"┌", "│", "└", "◆", "◇"} {
			if strings.Contains(text, custom) {
				t.Fatalf("help was routed through custom presenter (%q) for %v:\n%s", custom, args, text)
			}
		}
		if strings.Contains(text, "\x1b[") {
			t.Fatalf("default Cobra help unexpectedly contains ANSI for %v: %q", args, text)
		}
	}
}
