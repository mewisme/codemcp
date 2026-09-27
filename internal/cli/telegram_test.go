package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
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
