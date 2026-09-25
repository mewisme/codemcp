package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestHelpAlwaysUsesDefaultCobraRenderer(t *testing.T) {
	tests := []struct {
		args []string
		path string
	}{
		{args: []string{"--help"}},
		{args: []string{"-h"}},
		{args: []string{"workspace", "--help"}, path: "workspace"},
		{args: []string{"workspace", "-h"}, path: "workspace"},
		{args: []string{"help", "workspace"}, path: "workspace"},
		{args: []string{"config", "set", "--help"}, path: "config set"},
		{args: []string{"tunnel", "use", "--help"}, path: "tunnel use"},
		{args: []string{"upstream", "server", "auth", "login", "--help"}, path: "upstream server auth login"},
		{args: []string{"request", "approve", "--help"}, path: "request approve"},
	}
	for _, test := range tests {
		args := test.args
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
		for _, expected := range []string{"Usage:", "Flags:"} {
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

		baseline := newRootCommand()
		var expected bytes.Buffer
		baseline.SetOut(&expected)
		baseline.SetErr(&expected)
		baseline.InitDefaultHelpCmd()
		baseline.InitDefaultHelpFlag()
		baseline.InitDefaultVersionFlag()
		target := commandByRelativePath(baseline, test.path)
		if target == nil {
			t.Fatalf("baseline help target %q not found", test.path)
		}
		target.InitDefaultHelpFlag()
		if err := target.Help(); err != nil {
			t.Fatalf("baseline help for %q: %v", test.path, err)
		}
		if output.String() != expected.String() {
			t.Fatalf("help differs from default Cobra rendering for %v:\nwant=%q\ngot =%q", args, expected.String(), output.String())
		}
	}
}
