package config

import (
	"strings"
	"testing"
)

func TestChatGPTWebIntegrationDefaults(t *testing.T) {
	cfg := Default()
	if !cfg.Integrations.ChatGPTWeb.Enabled || cfg.Integrations.ChatGPTWeb.ConnectorName != "CodeMCP" || cfg.Integrations.ChatGPTWeb.MaxAgents != 5 {
		t.Fatalf("chatgpt web defaults=%#v", cfg.Integrations.ChatGPTWeb)
	}
	for _, test := range []struct {
		key  string
		want string
	}{
		{"integrations.chatgpt_web.enabled", "true"},
		{"integrations.chatgpt_web.connector_name", "CodeMCP"},
		{"integrations.chatgpt_web.max_agents", "5"},
	} {
		value, err := RawValue(cfg, test.key)
		if err != nil || value != test.want {
			t.Fatalf("%s=%q err=%v want=%q", test.key, value, err, test.want)
		}
	}
}

func TestChatGPTWebConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "connector empty", mutate: func(cfg *Config) { cfg.Integrations.ChatGPTWeb.ConnectorName = "" }, wantErr: "connector_name"},
		{name: "connector whitespace", mutate: func(cfg *Config) { cfg.Integrations.ChatGPTWeb.ConnectorName = " CodeMCP " }, wantErr: "connector_name"},
		{name: "max zero", mutate: func(cfg *Config) { cfg.Integrations.ChatGPTWeb.MaxAgents = 0 }, wantErr: "max_agents"},
		{name: "max too high", mutate: func(cfg *Config) { cfg.Integrations.ChatGPTWeb.MaxAgents = 6 }, wantErr: "max_agents"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.HTTP.MCP.Auth.TokenHash = "test-token-hash"
			cfg.HTTP.Admin.Auth.TokenHash = "test-admin-token-hash"
			test.mutate(&cfg)
			err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate error=%v want contains %q", err, test.wantErr)
			}
		})
	}
}
