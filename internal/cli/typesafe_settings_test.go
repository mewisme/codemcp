package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestTypeSafeScopedSettingsUseCanonicalSettingService(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	previousRoot := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previousRoot) })
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	generic := application.NewSettingService()
	if _, err := generic.Set(t.Context(), "integrations.typesafe.model", "jev-1.13.0"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Integrations.TypeSafe.Model != "jev-1.13.0" {
		t.Fatalf("generic config=%#v err=%v", cfg.Integrations.TypeSafe, err)
	}

	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"integration", "typesafe", "model", "jev-latest"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load()
	if err != nil || cfg.Integrations.TypeSafe.Model != "jev-latest" {
		t.Fatalf("scoped config=%#v err=%v output=%q", cfg.Integrations.TypeSafe, err, output.String())
	}

	output.Reset()
	cmd = newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"integration", "typesafe", "key", "set", "cli-secret-sentinel"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	presented, err := generic.Present(t.Context(), "integrations.typesafe.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if presented.Value != tracepkg.MaskSecret("cli-secret-sentinel", true) || presented.Configured == nil || !*presented.Configured || strings.Contains(output.String(), "cli-secret-sentinel") {
		t.Fatalf("secret presentation=%#v output=%q", presented, output.String())
	}
	data, err := os.ReadFile(config.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cli-secret-sentinel") {
		t.Fatal("scoped TypeSafe key leaked into config.json")
	}

	output.Reset()
	cmd = newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"integration", "typesafe", "key", "remove"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := generic.Read(t.Context(), "integrations.typesafe.api_key_configured")
	if err != nil || state.Value != "false" {
		t.Fatalf("configured state=%#v err=%v", state, err)
	}
}
