package cli

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/telegram"
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

func TestTelegramPairingProgressReplacesLoaderWithTerminalState(t *testing.T) {
	tests := []struct {
		name      string
		status    telegram.PairingStatus
		want      string
		wantError bool
	}{
		{name: "paired", status: telegram.PairingStatusPaired, want: "Telegram paired"},
		{name: "expired", status: telegram.PairingStatusExpired, want: "Telegram pairing expired", wantError: true},
		{name: "cancelled", status: telegram.PairingStatusCancelled, want: "Telegram pairing cancelled", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{
				Width: 80, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true,
			})
			if !startTelegramPairingProgress(session) {
				t.Fatal("pairing loader was not activated")
			}
			done, err := finishTelegramPairingProgress(session, test.status)
			if !done || (err != nil) != test.wantError {
				t.Fatalf("done=%t err=%v", done, err)
			}
			text := output.String()
			if strings.Contains(text, "Waiting for Telegram pairing") || !strings.Contains(text, test.want) {
				t.Fatalf("pairing progress output=%q", text)
			}
		})
	}
}

func TestTelegramPairingProgressPendingDoesNotTerminate(t *testing.T) {
	var output bytes.Buffer
	session := presentation.NewProgressSession(&output, presentation.ModeHuman, presentation.Capabilities{
		Width: 80, Unicode: true, RawUnicode: true, Interactive: true, CursorControl: true,
	})
	if !startTelegramPairingProgress(session) {
		t.Fatal("pairing loader was not activated")
	}
	done, err := finishTelegramPairingProgress(session, telegram.PairingStatusPending)
	if done || err != nil {
		t.Fatalf("pending pairing done=%t err=%v", done, err)
	}
	if strings.Contains(output.String(), "Telegram paired") || strings.Contains(output.String(), "expired") || strings.Contains(output.String(), "cancelled") {
		t.Fatalf("pending pairing emitted terminal state: %q", output.String())
	}
}

func TestTelegramSetupProductionFlowOwnsPairingTransitions(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve Telegram CLI test source")
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(currentFile), "telegram.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"PrepareTelegramPairing":        false,
		"startTelegramPairingProgress":  false,
		"finishTelegramPairingProgress": false,
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "runTelegramSetup" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch call := node.(type) {
			case *ast.CallExpr:
				switch target := call.Fun.(type) {
				case *ast.Ident:
					if _, exists := want[target.Name]; exists {
						want[target.Name] = true
					}
				case *ast.SelectorExpr:
					if _, exists := want[target.Sel.Name]; exists {
						want[target.Sel.Name] = true
					}
				}
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Fatalf("runTelegramSetup no longer reaches production pairing function %s", name)
		}
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
