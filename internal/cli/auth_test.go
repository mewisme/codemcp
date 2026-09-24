package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
)

func TestAuthCommandUsesNestedHierarchy(t *testing.T) {
	cmd := authCommand()
	for _, path := range [][]string{{"mcp", "create"}, {"mcp", "enable"}, {"mcp", "disable"}, {"admin", "create"}, {"admin", "enable"}, {"admin", "disable"}} {
		resolved, _, err := cmd.Find(path)
		if err != nil || resolved.Name() != path[len(path)-1] {
			t.Fatalf("auth path %v resolved to %v: %v", path, resolved, err)
		}
	}
	if resolved, _, err := cmd.Find([]string{"mcp-create"}); err == nil && resolved.Name() == "mcp-create" {
		t.Fatal("legacy dashed auth command still exists")
	}
}

func TestSubcommandNamesDoNotUseDashes(t *testing.T) {
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		for _, child := range command.Commands() {
			if strings.Contains(child.Name(), "-") {
				t.Errorf("dashed subcommand: %s", child.CommandPath())
			}
			visit(child)
		}
	}
	visit(newRootCommand())
}

func TestUsefulCommandAliasesResolve(t *testing.T) {
	root := newRootCommand()
	for _, test := range []struct {
		path []string
		want string
	}{
		{[]string{"cfg"}, "config"},
		{[]string{"cfg", "ls"}, "list"},
		{[]string{"ws"}, "workspace"},
		{[]string{"ws", "ls"}, "list"},
		{[]string{"ws", "access", "ls"}, "list"},
		{[]string{"upstream", "server", "ls"}, "list"},
		{[]string{"upstream", "server", "st"}, "status"},
		{[]string{"auth", "st"}, "status"},
		{[]string{"tunnel", "st"}, "status"},
		{[]string{"st"}, "status"},
	} {
		resolved, _, err := root.Find(test.path)
		if err != nil || resolved.Name() != test.want {
			t.Fatalf("alias path %v resolved to %v: %v", test.path, resolved, err)
		}
	}
}

func TestAuthStatusUsesStructuredPresenterWithoutHashes(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled = true
	cfg.Auth.AdminEnabled = false
	cfg.Auth.MCPTokenHash = "mcp-sensitive-hash"
	cfg.Auth.AdminTokenHash = "admin-sensitive-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--config-dir", root, "auth", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"Authentication", "MCP", "Admin", "enabled", "configured", "legacy bearer"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("auth status missing %q: %q", expected, text)
		}
	}
	for _, forbidden := range []string{"mcp-sensitive-hash", "admin-sensitive-hash", "enabled=true", "configured=true"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("auth status exposed legacy/detail value %q: %q", forbidden, text)
		}
	}
}

func TestAuthStatusRichPaletteKeepsSectionTextNeutral(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	caps := presentation.Capabilities{Width: 100, Unicode: true, Color: true, Interactive: true}
	cmd := authStatusCommand()
	cmd.SetOut(presentation.WrapWriter(&output, caps))
	cmd.SetErr(presentation.WrapWriter(&output, caps))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	theme := presentation.NewTheme(caps)
	text := output.String()
	for _, expected := range []string{
		theme.Render(presentation.RoleRail, "┌"),
		theme.Render(presentation.RoleStructure, "◆") + "  " + theme.Render(presentation.RoleHeading, "MCP"),
		theme.Render(presentation.RoleLabel, "enabled"),
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("auth palette missing %q: %q", expected, text)
		}
	}
	for _, forbidden := range []string{
		theme.Render(presentation.RoleActive, "Authentication"),
		theme.Render(presentation.RoleActive, "MCP"),
		theme.Render(presentation.RoleStructure, "MCP"),
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("auth palette over-colored settled text %q: %q", forbidden, text)
		}
	}
}
