package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
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

func TestLogUpstreamStatusUsesCLIFormatter(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	status := upstream.Status{ID: "demo", Name: "Demo", Enabled: true, Transport: "http", Auth: "oauth", Health: upstream.HealthConnected, Connected: true, ToolCount: 2, Expose: "all", ProxiedTools: []string{"demo_one", "demo_two"}}
	logUpstreamStatus(commandLogger(cmd), status)
	text := output.String()
	if !strings.Contains(text, "Upstream server connected") || !strings.Contains(text, "health: connected") || !strings.Contains(text, "tools: 2") || strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("output=%q", text)
	}
}
