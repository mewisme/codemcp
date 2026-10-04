package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestWorkspaceRelocateCommandExposesResolveFlag(t *testing.T) {
	cmd := workspaceRelocateCommand()
	flag := cmd.Flags().Lookup("resolve")
	if flag == nil {
		t.Fatal("missing --resolve flag")
	}
	if flag.DefValue != "" {
		t.Fatalf("resolve default=%q", flag.DefValue)
	}
}

func TestPromptWorkspaceRelocationResolutionSelections(t *testing.T) {
	conflict := application.WorkspaceRelocationConflict{WorkspaceID: "ws_test"}
	tests := []struct {
		input     string
		want      workspace.RelocationResolution
		cancelled bool
	}{
		{input: "1\n", want: workspace.RelocationResolutionDestination},
		{input: "registered\n", want: workspace.RelocationResolutionRegistered},
		{input: "3\n", want: workspace.RelocationResolutionMerge},
		{input: "cancel\n", cancelled: true},
		{input: "bad\nbad\n4\n", cancelled: true},
	}
	for _, test := range tests {
		var output bytes.Buffer
		cmd := workspaceRelocateCommand()
		cmd.SetIn(strings.NewReader(test.input))
		cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, RawUnicode: true, Interactive: true}))
		setCommandPresentationTitle(cmd, "Workspace relocation")
		got, cancelled, err := promptWorkspaceRelocationResolution(cmd, conflict)
		if err != nil {
			t.Fatalf("input %q error=%v output=%q", test.input, err, output.String())
		}
		if got != test.want || cancelled != test.cancelled {
			t.Fatalf("input %q got=%q cancelled=%v want=%q cancelled=%v", test.input, got, cancelled, test.want, test.cancelled)
		}
		commandProgressSession(cmd).SetCompletion("Done")
		closeCommandProgress(cmd, nil)
		text := output.String()
		for _, want := range []string{
			"┌  Workspace relocation",
			"Duplicate workspace identity ws_test exists at both roots",
			"│  ▸ Resolution",
			"│  │  1 destination — Keep destination .cm state",
			"Select resolution [1-4], then press Enter",
			"└  Done",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("input %q prompt missing %q: %q", test.input, want, text)
			}
		}
		if strings.Count(text, "┌  Workspace relocation") != 1 || strings.Count(text, "└  Done") != 1 {
			t.Fatalf("input %q prompt frame duplicated: %q", test.input, text)
		}
		if strings.Contains(test.input, "bad") && !strings.Contains(text, "Invalid selection") {
			t.Fatalf("input %q missing invalid-selection feedback: %q", test.input, text)
		}
	}
}

func TestWorkspaceRelocateInteractiveRejectsNonTTYInput(t *testing.T) {
	cmd := workspaceRelocateCommand()
	cmd.SetIn(strings.NewReader("1\n"))
	var output bytes.Buffer
	cmd.SetOut(&output)
	if workspaceRelocateInteractive(cmd) {
		t.Fatal("non-TTY command became interactive")
	}
}
