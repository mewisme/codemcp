package cli

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/integrations/chatgptweb"
)

func TestChatGPTWebLoginNeverAcceptsCredentialMaterial(t *testing.T) {
	cmd := chatGPTWebLoginCommand()
	for _, forbidden := range []string{"email", "password", "username", "token", "cookie", "session"} {
		if flag := cmd.Flags().Lookup(forbidden); flag != nil {
			t.Fatalf("login command exposes credential flag --%s", forbidden)
		}
	}
	if cmd.Args == nil {
		t.Fatal("login command has no argument contract")
	}
	if err := cmd.Args(cmd, []string{"secret"}); err == nil {
		t.Fatal("login command accepted positional credential material")
	}
}

func TestChatGPTWebLogoutRequiresExplicitConfirmation(t *testing.T) {
	cmd := chatGPTWebLogoutCommand()
	cmd.SetArgs(nil)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("logout error=%v", err)
	}
	if cmd.Flags().Lookup("yes") == nil || cmd.Flags().Lookup("force") == nil {
		t.Fatal("logout command is missing --yes or --force")
	}
}

func TestBrowserAndChatGPTWebStayUnderIntegrationScope(t *testing.T) {
	root := newRootCommand()
	for _, path := range []string{
		"integration browser status",
		"integration browser doctor",
		"integration chatgpt-web status",
		"integration chatgpt-web login",
		"integration chatgpt-web logout",
		"integration chatgpt-web doctor",
	} {
		if command := commandByRelativePath(root, path); command == nil || !command.Runnable() {
			t.Fatalf("canonical integration command %q is not runnable", path)
		}
	}
	for _, path := range []string{"browser", "chatgpt", "chatgpt-web", "subagent"} {
		if command := commandByRelativePath(root, path); command != nil {
			t.Fatalf("unexpected root command %q remains reachable as %q", path, command.CommandPath())
		}
	}
}

func TestChatGPTWebStatusPresentationContainsNoProfileOrIdentityFields(t *testing.T) {
	var output strings.Builder
	presenter := presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100})
	renderChatGPTWebStatus(presenter, application.ChatGPTWebStatus{
		Enabled:            true,
		State:              chatgptweb.StateReady,
		BrowserState:       application.BrowserIntegrationAvailable,
		Authenticated:      true,
		ConnectorName:      "CodeMCP",
		ConnectorAvailable: true,
		MaxAgents:          5,
	})
	text := strings.ToLower(output.String())
	for _, forbidden := range []string{"profile path", "cookie", "email", "account", "access token", "localstorage"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status output contains forbidden auth/profile field %q: %s", forbidden, output.String())
		}
	}
}
