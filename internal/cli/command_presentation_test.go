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
	var invalid []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Runnable() {
			title := commandPresentationTitle(cmd)
			exempt := commandPresentationExempt(cmd)
			if (title == "") == !exempt {
				invalid = append(invalid, relativeCommandPath(cmd))
			}
			if exempt {
				reason := strings.TrimSpace(cmd.Annotations[presentationExemptAnnotation])
				switch reason {
				case "machine-output", "alternate-ui", "internal-runtime":
				default:
					t.Errorf("command %q has unsupported presentation exemption %q", relativeCommandPath(cmd), reason)
				}
				if reason == "machine-output" && !commandExplicitMachineOutput(cmd) {
					t.Errorf("command %q claims machine-output exemption without machine output", relativeCommandPath(cmd))
				}
			}
			if commandExplicitMachineOutput(cmd) && strings.TrimSpace(cmd.Annotations[presentationExemptAnnotation]) != "machine-output" {
				t.Errorf("machine command %q lacks machine-output presentation exemption", relativeCommandPath(cmd))
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(invalid) != 0 {
		t.Fatalf("runnable commands must declare exactly one presentation title or exemption: %v", invalid)
	}

	for path, want := range map[string]string{
		"status":                     "CodeMCP status",
		"config set":                 "Update configuration",
		"workspace list":             "Registered workspaces",
		"tunnel list":                "Managed OpenAI tunnels",
		"tunnel use":                 "Select managed OpenAI tunnel",
		"auth mcp create":            "Authentication",
		"request approve":            "Control approval request",
		"tunnel admin key verify":    "Verify OpenAI tunnel admin key",
		"upstream server auth login": "Authorize Upstream server",
		"workspace container create": "Workspace container",
		"workspace access add":       "Workspace access",
		"config export":              "Export configuration",
		"config migrate secrets":     "Migrate secret files",
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

func TestAliasesInheritCanonicalPresentationContract(t *testing.T) {
	root := newRootCommand()
	for _, test := range []struct {
		canonical []string
		alias     []string
	}{
		{canonical: []string{"config", "list"}, alias: []string{"cfg", "ls"}},
		{canonical: []string{"workspace", "list"}, alias: []string{"ws", "ls"}},
		{canonical: []string{"tunnel", "use"}, alias: []string{"tunnel", "select"}},
		{canonical: []string{"request", "view"}, alias: []string{"req", "show"}},
		{canonical: []string{"status"}, alias: []string{"st"}},
	} {
		canonical, _, err := root.Find(test.canonical)
		if err != nil {
			t.Fatalf("canonical %v: %v", test.canonical, err)
		}
		alias, _, err := root.Find(test.alias)
		if err != nil {
			t.Fatalf("alias %v: %v", test.alias, err)
		}
		if canonical != alias {
			t.Fatalf("alias %v resolved to distinct command %q instead of %q", test.alias, alias.CommandPath(), canonical.CommandPath())
		}
		if commandPresentationTitle(alias) != commandPresentationTitle(canonical) || commandPresentationExempt(alias) != commandPresentationExempt(canonical) {
			t.Fatalf("alias %v presentation contract drifted from canonical %v", test.alias, test.canonical)
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
			presenter.Section("Result")
			presenter.Fields(presentation.Field{Label: "state", Value: "ready"})
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
	if !strings.Contains(text, "│  ▸ Result\n│\n│  state — ready") {
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
	if !strings.Contains(text, "×  invalid input") || !strings.Contains(text, "└  Failed") {
		t.Fatalf("fallback error did not render canonical failed workflow: %q", text)
	}
	if strings.Contains(text, "Command failed") || strings.Contains(text, "Usage:") {
		t.Fatalf("fallback error escaped workflow or printed usage: %q", text)
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
