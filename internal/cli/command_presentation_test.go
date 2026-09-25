package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func TestRunnableCommandsDeclarePresentationBehavior(t *testing.T) {
	root := newRootCommand()
	var missing []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Runnable() && commandPresentationTitle(cmd) == "" && !commandPresentationExempt(cmd) {
			missing = append(missing, relativeCommandPath(cmd))
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(missing) != 0 {
		t.Fatalf("runnable commands missing presentation metadata: %v", missing)
	}

	for path, want := range map[string]string{
		"status":         "CodeMCP status",
		"config set":     "Update configuration",
		"workspace list": "Registered workspaces",
		"tunnel list":    "Managed OpenAI tunnels",
		"tunnel use":     "Select managed OpenAI tunnel",
	} {
		cmd := commandByRelativePath(root, path)
		if cmd == nil {
			t.Fatalf("command %q not found", path)
		}
		if got := commandPresentationTitle(cmd); got != want {
			t.Fatalf("command %q title=%q want=%q", path, got, want)
		}
	}
}

func TestCommandSessionStartsBeforeProgressAndOwnsSingleFrame(t *testing.T) {
	var output bytes.Buffer
	caps := presentation.Capabilities{
		StdoutTTY: true, StderrTTY: true, Width: 100, Unicode: true, RawUnicode: true, Interactive: true,
	}
	writer := presentation.WrapWriter(&output, caps)
	root := &cobra.Command{
		Use:           "cm",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			prepareCommandPresentation(cmd)
		},
	}
	addLoggingFlags(root)
	addTerminalPresentationFlags(root)
	child := &cobra.Command{
		Use:   "demo",
		Short: "Demo command",
		RunE: func(cmd *cobra.Command, _ []string) error {
			commandProgressSession(cmd).Success("load", "Loading data", "Data loaded")
			presenter := commandPresenter(cmd)
			presenter.Frame("Duplicate frame request")
			presenter.Section("Result")
			presenter.Fields(presentation.Field{Label: "state", Value: "ready"})
			presenter.FrameEnd("Done")
			return nil
		},
	}
	setCommandPresentationTitle(child, "Demo command")
	root.AddCommand(child)
	root.SetOut(writer)
	root.SetErr(writer)
	root.SetArgs([]string{"demo"})

	if err := executeCommand(root); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.HasPrefix(text, "┌  Demo command\n") {
		t.Fatalf("command did not begin with frame: %q", text)
	}
	if strings.Count(text, "┌  ") != 1 || strings.Count(text, "└  Done") != 1 {
		t.Fatalf("command did not use exactly one frame: %q", text)
	}
	if strings.Contains(text, "Duplicate frame request") {
		t.Fatalf("idempotent frame request opened a second frame: %q", text)
	}
	frame := strings.Index(text, "┌  Demo command")
	progress := strings.Index(text, "◆  Data loaded")
	if frame < 0 || progress <= frame {
		t.Fatalf("progress appeared before frame: %q", text)
	}
	if !strings.Contains(text, "◆  Result\n│\n│  ◆ state — ready") {
		t.Fatalf("section/content spacing contract missing: %q", text)
	}
}

func TestCommandSessionFallbackFramesPreRunErrors(t *testing.T) {
	var output bytes.Buffer
	caps := presentation.Capabilities{
		StdoutTTY: true, StderrTTY: true, Width: 100, Unicode: true, RawUnicode: true, Interactive: true,
	}
	writer := presentation.WrapWriter(&output, caps)
	root := &cobra.Command{Use: "cm", SilenceErrors: true, SilenceUsage: true}
	addLoggingFlags(root)
	addTerminalPresentationFlags(root)
	child := &cobra.Command{
		Use:   "broken",
		Short: "Broken command",
		Args: func(*cobra.Command, []string) error {
			return errors.New("invalid input")
		},
		RunE: func(*cobra.Command, []string) error {
			return nil
		},
	}
	setCommandPresentationTitle(child, "Broken command")
	root.AddCommand(child)
	root.SetOut(writer)
	root.SetErr(writer)
	root.SetArgs([]string{"broken"})

	if err := executeCommand(root); err == nil {
		t.Fatal("pre-run error unexpectedly succeeded")
	}
	text := output.String()
	if !strings.HasPrefix(text, "┌  Broken command\n") {
		t.Fatalf("fallback error was not framed first: %q", text)
	}
	if strings.Count(text, "┌  ") != 1 || strings.Count(text, "└") != 1 {
		t.Fatalf("fallback error did not use one bounded frame: %q", text)
	}
}

func commandByRelativePath(root *cobra.Command, path string) *cobra.Command {
	var found *cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if found != nil {
			return
		}
		if relativeCommandPath(cmd) == path {
			found = cmd
			return
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	return found
}
