package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/configformat"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestParseAssignments(t *testing.T) {
	value, err := parseAssignments([]string{"A=1", "B=two=parts"}, "env")
	if err != nil {
		t.Fatal(err)
	}
	if value["A"] != "1" || value["B"] != "two=parts" {
		t.Fatalf("value = %#v", value)
	}
	if _, err := parseAssignments([]string{"broken"}, "env"); err == nil {
		t.Fatal("invalid assignment was accepted")
	}
}

func TestRedactUpstreamServer(t *testing.T) {
	server := upstream.Server{
		Headers: map[string]string{"Authorization": "Bearer secret", "X-Test": "ok"},
		Env:     map[string]string{"API_TOKEN": "secret", "MODE": "test"},
	}
	value := redactUpstreamServer(server)
	if value.Headers["Authorization"] != "<redacted>" || value.Headers["X-Test"] != "ok" {
		t.Fatalf("headers = %#v", value.Headers)
	}
	if value.Env["API_TOKEN"] != "<redacted>" || value.Env["MODE"] != "test" {
		t.Fatalf("env = %#v", value.Env)
	}
	if server.Headers["Authorization"] != "Bearer secret" || server.Env["API_TOKEN"] != "secret" {
		t.Fatal("redaction mutated source config")
	}
}

func TestUpstreamServerShowDefaultsToTextAndSupportsJSON(t *testing.T) {
	defer configformat.SetRootPath("")
	root := filepath.Join(t.TempDir(), "config")
	if _, err := executeRequestCommandError(root, []string{"upstream", "server", "add", "demo", "--transport", "http", "--url", "https://mcp.example.test", "--header", "Authorization=secret-value"}); err != nil {
		t.Fatal(err)
	}
	plain := executeRequestCommand(t, root, []string{"upstream", "server", "show", "demo"})
	if !strings.Contains(plain, "Upstream server") || !strings.Contains(plain, "demo") || !strings.Contains(plain, "<redacted>") || strings.Contains(plain, "secret-value") || strings.HasPrefix(strings.TrimSpace(plain), "{") {
		t.Fatalf("plain=%q", plain)
	}
	structured := executeRequestCommand(t, root, []string{"upstream", "server", "show", "demo", "--json"})
	var server upstream.Server
	if err := json.Unmarshal([]byte(strings.TrimSpace(structured)), &server); err != nil || server.ID != "demo" || server.Headers["Authorization"] != "<redacted>" {
		t.Fatalf("json=%q server=%#v err=%v", structured, server, err)
	}
}

func TestMCPCommandDoesNotExposeServerManagement(t *testing.T) {
	cmd := mcpCommand()
	if found, _, err := cmd.Find([]string{"server"}); err == nil && found != cmd {
		t.Fatalf("mcp server compatibility command remains public: %s", found.CommandPath())
	}
	for _, child := range cmd.Commands() {
		if child.Name() == "server" {
			t.Fatal("mcp server compatibility command remains public")
		}
	}
}

func TestMCPHTTPCommandExposesDeterministicProfileSelection(t *testing.T) {
	command := mcpHTTPCommand()
	flag := command.Flags().Lookup("profile")
	if flag == nil || flag.DefValue != "base" {
		t.Fatalf("profile flag=%#v", flag)
	}
}

func TestUpstreamHelpUsesCanonicalTerminology(t *testing.T) {
	cmd := upstreamCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "Manage Upstream servers") || !strings.Contains(text, "server") {
		t.Fatalf("upstream help missing canonical terminology: %q", text)
	}
	for _, legacy := range []string{"MCP Servers", "upstream MCP server", "Upstream MCP server"} {
		if strings.Contains(text, legacy) {
			t.Fatalf("upstream help retained legacy terminology %q: %q", legacy, text)
		}
	}
}

func TestRenderUpstreamStatusUsesCLIFormatter(t *testing.T) {
	var output bytes.Buffer
	status := upstream.Status{ID: "demo", Name: "Demo", Enabled: true, Transport: "http", Auth: "oauth", Health: upstream.HealthConnected, Connected: true, ToolCount: 2, Expose: "all", ProxiedTools: []string{"demo_one", "demo_two"}}
	renderUpstreamStatus(presentation.New(&output, presentation.ModePlain, presentation.Capabilities{Width: 100, Unicode: false}), status)
	text := output.String()
	if !strings.Contains(text, "Upstream server connected") || !strings.Contains(text, "health") || !strings.Contains(text, "connected") || !strings.Contains(text, "tools") || !strings.Contains(text, "2") || strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("output=%q", text)
	}
}

func TestUpstreamReadRenderersUseRailHierarchyWithoutDenseOrSecretValues(t *testing.T) {
	server := redactUpstreamServer(upstream.Server{
		ID: "demo", Name: "Demo", Transport: "http", Enabled: true, URL: "https://mcp.example.test", Expose: "all",
		Headers: map[string]string{"Authorization": "Bearer secret-value", "X-Test": "ok"},
	})
	var output bytes.Buffer
	renderUpstreamServer(presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true}), server)
	text := output.String()
	for _, expected := range []string{"┌  Upstream server", "│  ◆ demo", "│  │  endpoint — https://mcp.example.test", "<redacted>", "└  Done"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("upstream detail missing %q: %q", expected, text)
		}
	}
	for _, forbidden := range []string{"secret-value", "enabled=", "health=", "tools="} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("upstream detail contains forbidden dense/secret value %q: %q", forbidden, text)
		}
	}

	output.Reset()
	renderUpstreamOAuthStatus(presentation.New(&output, presentation.ModeHuman, presentation.Capabilities{Width: 100, Unicode: true}), mcpoauth.Status{
		ServerID: "demo", Configured: true, Issuer: "https://issuer.example.test", Registration: "dynamic", Scopes: []string{"openid", "mcp"}, HasRefreshToken: true,
	})
	oauthText := output.String()
	for _, expected := range []string{"┌  Upstream OAuth authorization", "✓  Authorization configured", "│  ◆ demo", "│  │  issuer — https://issuer.example.test"} {
		if !strings.Contains(oauthText, expected) {
			t.Fatalf("oauth detail missing %q: %q", expected, oauthText)
		}
	}
}
