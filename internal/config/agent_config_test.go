package config

import (
	"strings"
	"testing"
)

func TestManagedAgentConfigDefaults(t *testing.T) {
	cfg := Default()
	cfg.HTTP.MCP.Auth.TokenHash = "test-mcp"
	cfg.HTTP.Admin.Auth.TokenHash = "test-admin"
	if cfg.Agent.DefaultBackend != "chatgpt-web" || cfg.Agent.MaxParallel != 5 {
		t.Fatalf("agent defaults=%#v", cfg.Agent)
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestManagedAgentConfigValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "empty backend", edit: func(cfg *Config) { cfg.Agent.DefaultBackend = "" }, want: "agent.default_backend"},
		{name: "whitespace backend", edit: func(cfg *Config) { cfg.Agent.DefaultBackend = " chatgpt-web " }, want: "agent.default_backend"},
		{name: "invalid backend", edit: func(cfg *Config) { cfg.Agent.DefaultBackend = "ChatGPT Web" }, want: "agent.default_backend"},
		{name: "zero parallel", edit: func(cfg *Config) { cfg.Agent.MaxParallel = 0 }, want: "agent.max_parallel"},
		{name: "parallel too high", edit: func(cfg *Config) { cfg.Agent.MaxParallel = 129 }, want: "agent.max_parallel"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.HTTP.MCP.Auth.TokenHash = "test-mcp"
			cfg.HTTP.Admin.Auth.TokenHash = "test-admin"
			test.edit(&cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error=%v want substring %q", err, test.want)
			}
		})
	}
}

func TestManagedAgentConfigFieldsRoundTrip(t *testing.T) {
	cfg := Default()
	cfg.HTTP.MCP.Auth.TokenHash = "test-mcp"
	cfg.HTTP.Admin.Auth.TokenHash = "test-admin"
	if err := SetValueValidated(&cfg, "agent.default_backend", "test-backend"); err != nil {
		t.Fatal(err)
	}
	if err := SetValueValidated(&cfg, "agent.max_parallel", "7"); err != nil {
		t.Fatal(err)
	}
	if got, err := RawValue(cfg, "agent.default_backend"); err != nil || got != "test-backend" {
		t.Fatalf("default backend=%q err=%v", got, err)
	}
	if got, err := RawValue(cfg, "agent.max_parallel"); err != nil || got != "7" {
		t.Fatalf("max parallel=%q err=%v", got, err)
	}
}
