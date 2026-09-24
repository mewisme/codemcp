package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/configformat"
)

func TestHelpPresentationUsesRailHierarchyForInteractiveOutput(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	writer := presentation.WrapWriter(&output, presentation.Capabilities{
		StdoutTTY: true, StderrTTY: true, Width: 120, Unicode: true, RawUnicode: true, Interactive: true,
	})
	root.SetOut(writer)
	root.SetErr(writer)
	root.SetArgs([]string{"workspace", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"┌  cm workspace",
		"◆  Manage registered workspace roots",
		"◆  Command",
		"│  ◆ canonical — cm workspace",
		"│  ◆ aliases — cm ws",
		"◆  Usage",
		"◆  Commands",
		"◆  Flags",
		"◆  Global Flags",
		"└",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("interactive help missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "\n│\n│\n") {
		t.Fatalf("interactive help contains duplicate empty rail lines:\n%s", text)
	}
}

func TestHelpAliasMetadataHookExtendsCanonicalRenderer(t *testing.T) {
	root := newRootCommand()
	workspaceCmd, _, err := root.Find([]string{"workspace"})
	if err != nil {
		t.Fatal(err)
	}
	setCommandHelpAliasPaths(workspaceCmd, "cm work", "cm workspace")

	metadata := commandHelpMetadata(workspaceCmd)
	if metadata.CanonicalPath != "cm workspace" {
		t.Fatalf("canonical path=%q", metadata.CanonicalPath)
	}
	joined := strings.Join(metadata.AliasPaths, ",")
	for _, expected := range []string{"cm ws", "cm work"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("alias metadata missing %q: %#v", expected, metadata.AliasPaths)
		}
	}
	if strings.Count(joined, "cm workspace") != 0 {
		t.Fatalf("canonical path duplicated into aliases: %#v", metadata.AliasPaths)
	}
}

func TestHelpAndSubcommandHelpAreSideEffectFreeAndPlainWhenRedirected(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "config")
	t.Setenv(configformat.EnvConfigDir, configRoot)

	for _, args := range [][]string{{"--help"}, {"tunnel", "--help"}} {
		var first, second string
		for run := range 2 {
			root := newRootCommand()
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("args=%v: %v", args, err)
			}
			text := output.String()
			if strings.ContainsAny(text, "\r\x1b") {
				t.Fatalf("redirected help contains terminal control bytes for %v: %q", args, text)
			}
			for _, rail := range []string{"┌", "│", "└"} {
				if strings.Contains(text, rail) {
					t.Fatalf("redirected help contains rich rail %q for %v: %q", rail, args, text)
				}
			}
			if run == 0 {
				first = text
			} else {
				second = text
			}
		}
		if first != second {
			t.Fatalf("redirected help is not deterministic for %v:\nfirst=%q\nsecond=%q", args, first, second)
		}
	}
	assertDirectoryAbsentOrEmpty(t, configRoot)
}

func TestCompletionScriptsRemainByteCleanMachineOutput(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "config")
	t.Setenv(configformat.EnvConfigDir, configRoot)

	outputs := map[string]string{}
	for _, shell := range completionShells {
		root := newRootCommand()
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"completion", shell})
		if err := executeCommand(root); err != nil {
			t.Fatalf("%s completion: %v", shell, err)
		}
		text := stdout.String()
		if strings.TrimSpace(text) == "" {
			t.Fatalf("%s completion output is empty", shell)
		}
		if strings.ContainsAny(text, "\r\x1b") {
			t.Fatalf("%s completion contains terminal control bytes", shell)
		}
		for _, fragment := range []string{"┌", "│", "└", "◆", "✓", "Executing command"} {
			if strings.Contains(text, fragment) {
				t.Fatalf("%s completion contains presentation fragment %q", shell, fragment)
			}
		}
		if stderr.Len() != 0 {
			t.Fatalf("%s completion wrote diagnostics by default: %q", shell, stderr.String())
		}
		outputs[shell] = text
	}

	for _, logFlag := range []string{"--verbose", "--debug"} {
		root := newRootCommand()
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{logFlag, "completion", "bash"})
		if err := executeCommand(root); err != nil {
			t.Fatalf("%s completion: %v", logFlag, err)
		}
		if stdout.String() != outputs["bash"] {
			t.Fatalf("%s changed completion stdout bytes", logFlag)
		}
		if strings.ContainsAny(stdout.String(), "\r\x1b") {
			t.Fatalf("%s polluted completion stdout with terminal control", logFlag)
		}
	}
	assertDirectoryAbsentOrEmpty(t, configRoot)
}

func assertDirectoryAbsentOrEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("side-effect-free command wrote config/state entries under %s: %#v", path, entries)
	}
}
