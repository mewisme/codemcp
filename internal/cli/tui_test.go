package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestTUICommandIsRegistered(t *testing.T) {
	command, _, err := newRootCommand().Find([]string{"tui"})
	if err != nil || command.Name() != "tui" {
		t.Fatalf("tui command = %v, %v", command, err)
	}
}

func TestTUICommandRequiresTerminal(t *testing.T) {
	command := tuiCommand()
	command.SetIn(&bytes.Buffer{})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs(nil)
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "requires terminal") {
		t.Fatalf("tui non-terminal error = %v", err)
	}
}

func TestRootTUICommandReportsNonTerminalFailure(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := newRootCommand()
	root.SetIn(&bytes.Buffer{})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"tui"})
	if err := executeCommand(root); err == nil || !strings.Contains(err.Error(), "requires terminal stdin and stdout") {
		t.Fatalf("tui non-terminal error = %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "cm tui requires terminal stdin and stdout") {
		t.Fatalf("tui non-terminal stderr = %q", got)
	} else if strings.Contains(got, "\x1b[") {
		t.Fatalf("tui non-terminal stderr contains ANSI: %q", got)
	}
}

func TestTUICommandRejectsUnknownDeepLinkBeforeLaunch(t *testing.T) {
	command := tuiCommand()
	command.SetIn(&bytes.Buffer{})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"missing"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown TUI path") {
		t.Fatalf("unknown TUI path error = %v", err)
	}
}
