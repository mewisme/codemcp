package cli

import (
	"bytes"
	"strings"
	"testing"
)

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
		"CodeMCP workspace-bound local MCP server for ChatGPT",
		"Usage:\n  cm [flags]",
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
