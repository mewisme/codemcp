package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestTelegramSetupCommandRegistered(t *testing.T) {
	cmd, _, err := newRootCommand().Find([]string{"telegram", "setup"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil || cmd.Name() != "setup" {
		t.Fatalf("telegram setup command=%v", cmd)
	}
}

func TestCompletedTelegramCLIEntryPointsAreRegistered(t *testing.T) {
	for _, item := range capability.TelegramRolloutInventory() {
		if item.State != capability.TelegramRolloutLive {
			continue
		}
		for _, entry := range item.EntryPoints {
			if entry.Kind != capability.TelegramEntryCLICommand {
				continue
			}
			parts := strings.Fields(entry.Value)
			cmd, remaining, err := newRootCommand().Find(parts)
			if err != nil || cmd == nil || len(remaining) != 0 || cmd.Name() != parts[len(parts)-1] {
				t.Fatalf("completed Telegram CLI entry point %q is unreachable: command=%v remaining=%v err=%v", entry.Value, cmd, remaining, err)
			}
		}
	}
}

func TestTelegramLogoutUsesCanonicalAuthorizationMutation(t *testing.T) {
	root := isolateUniversalConfigCLI(t)
	t.Setenv(configformat.EnvConfigDir, root)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Telegram.Enabled = true
	cfg.Telegram.AllowedUserIDs = []int64{42}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"telegram", "logout", "42"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.Enabled || len(cfg.Telegram.AllowedUserIDs) != 0 {
		t.Fatalf("Telegram logout config=%#v", cfg.Telegram)
	}
	if !strings.Contains(output.String(), "Telegram user logged out") {
		t.Fatalf("Telegram logout output=%q", output.String())
	}
}

func TestTelegramSetupRequiresConfiguredToken(t *testing.T) {
	root := isolateUniversalConfigCLI(t)
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()

	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"telegram", "setup"})
	err := cmd.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "telegram bot token is not configured") {
		t.Fatalf("setup error=%v output=%q", err, output.String())
	}
}

func TestTelegramTokenScopedAndGenericSettingsConverge(t *testing.T) {
	root := isolateUniversalConfigCLI(t)
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()

	const genericSecret = "123456:generic-telegram-secret"
	service := application.NewSettingService()
	if _, err := service.Set(t.Context(), "telegram.token", genericSecret); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"config", "list", "telegram"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "telegram.token") || !strings.Contains(output.String(), tracepkg.MaskSecret(genericSecret, true)) {
		t.Fatalf("config list missing Telegram token state: %q", output.String())
	}
	if strings.Contains(output.String(), genericSecret) {
		t.Fatalf("config list leaked Telegram token: %q", output.String())
	}

	const scopedSecret = "123456:scoped-telegram-secret"
	output.Reset()
	cmd = newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"telegram", "token", "set", scopedSecret})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), scopedSecret) {
		t.Fatalf("scoped token set leaked Telegram token: %q", output.String())
	}
	stored, err := secretstore.New(root).Get(secretstore.Name("telegram", "bot-token"))
	if err != nil || stored != scopedSecret {
		t.Fatalf("stored token=%q err=%v", stored, err)
	}

	output.Reset()
	cmd = newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"telegram", "token", "status"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "configured") || strings.Contains(output.String(), scopedSecret) {
		t.Fatalf("token status output=%q", output.String())
	}

	output.Reset()
	cmd = newRootCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"telegram", "token", "remove"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := service.Read(t.Context(), "telegram.token_configured")
	if err != nil || state.Value != "false" {
		t.Fatalf("configured state=%#v err=%v", state, err)
	}
}
