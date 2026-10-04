package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommandWithoutArgumentsPrintsHelp(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := output.String()
	for _, required := range []string{
		"CodeMCP is a workspace-aware execution, context, and agent orchestration server for AI clients.",
		"Usage:\n  cm [command]",
		"Available Commands:",
		"serve",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("root invocation missing help content %q:\n%s", required, help)
		}
	}
	if strings.Contains(help, "Server ready") {
		t.Fatalf("root invocation unexpectedly entered server lifecycle:\n%s", help)
	}
}

func TestRootCommandWithOnlyPersistentFlagsPrintsHelp(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--config-dir", t.TempDir()})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := output.String()
	for _, required := range []string{"Usage:\n  cm [command]", "serve", "Start the MCP server"} {
		if !strings.Contains(help, required) {
			t.Fatalf("root invocation with persistent flags missing help content %q:\n%s", required, help)
		}
	}
}

func TestPublicCLIIdentityUsesOnlyCodeMCPAndCM(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := output.String()
	for _, required := range []string{
		"CodeMCP is a workspace-aware execution, context, and agent orchestration server for AI clients.",
		"Usage:\n  cm [command]",
		"Generate shell completion for cm",
		"env: CM_CONFIG_DIR",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("root help missing %q:\n%s", required, help)
		}
	}
	for _, legacy := range []string{"chatgpt-mcp", "cgm", "CHATGPT_MCP"} {
		if strings.Contains(help, legacy) {
			t.Fatalf("root help exposes legacy identity %q:\n%s", legacy, help)
		}
	}
	for _, command := range root.Commands() {
		if command.Name() == "alias" {
			t.Fatal("legacy executable-alias command is public")
		}
	}
	install, _, err := root.Find([]string{"install"})
	if err != nil {
		t.Fatal(err)
	}
	if install.Flags().Lookup("no-alias") != nil {
		t.Fatal("install still exposes --no-alias")
	}
}
