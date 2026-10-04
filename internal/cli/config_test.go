package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestSetConfigValueTyped(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := setConfigValue(&cfg, "http.mcp.port", "4000"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "http.exposure", "true"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "http.admin.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "tunnel.control_plane_base_url", "https://api.openai.com"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "tunnel.organization_id", "org-test"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "integrations.ponytail.active", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "integrations.ponytail.mode", "ULTRA"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "integrations.caveman.active", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "integrations.caveman.mode", "WENYAN-ULTRA"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "integrations.fanout.active", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "integrations.fanout.mode", "AGGRESSIVE"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "permissions.allow_dirs", "/tmp,/var/tmp"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "shell.path", "/opt/tools,/usr/local/custom/bin"); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.MCP.Port != 4000 || cfg.HTTP.Exposure.Mode != config.ExposureWildcard || cfg.HTTP.Admin.Enabled || cfg.Integrations.Ponytail.Active || cfg.Integrations.Ponytail.Mode != "ultra" || cfg.Integrations.Caveman.Active || cfg.Integrations.Caveman.Mode != "wenyan-ultra" || cfg.Integrations.Fanout.Active || cfg.Integrations.Fanout.Mode != "aggressive" || cfg.Tunnel.ControlPlaneBaseURL != "https://api.openai.com" || cfg.Tunnel.OrganizationID != "org-test" || len(cfg.Permissions.AllowDirs) != 2 || len(cfg.Shell.Path) != 2 {
		t.Fatalf("cfg = %#v", cfg)
	}
	if err := setConfigValue(&cfg, "integrations.ponytail.mode", "review"); err == nil {
		t.Fatal("session-only review accepted as configured Ponytail mode")
	}
	if err := setConfigValue(&cfg, "integrations.caveman.mode", "wenyan"); err == nil {
		t.Fatal("Caveman runtime alias accepted as configured mode")
	}
	if err := setConfigValue(&cfg, "integrations.fanout.mode", "off"); err == nil {
		t.Fatal("transient Fanout off accepted as configured mode")
	}
}

func TestConfigSetValidationMatchesSharedDomain(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	previous := configformat.RootPath()
	defer configformat.SetRootPath(previous)
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	want := cfg
	wantErr := config.SetValueValidated(&want, "http.mcp.port", "70000")
	if wantErr == nil {
		t.Fatal("shared config validation unexpectedly accepted invalid port")
	}
	_, err := executeRequestCommandError(root, []string{"config", "set", "http.mcp.port", "70000"})
	if err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("CLI err=%v want=%v", err, wantErr)
	}
}

func TestConfigSetSecretValueDoesNotLeakIntoPresentationOrDiagnostics(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	previous := configformat.RootPath()
	defer configformat.SetRootPath(previous)
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	const secret = "sk-runtime-super-secret-value"
	output, err := executeRequestCommandError(root, []string{"--verbose", "config", "set", "tunnel.api_key", secret})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, secret) {
		t.Fatalf("secret mutation value leaked into CLI output: %q", output)
	}
	for _, expected := range []string{"Update configuration", "Setting saved", "tunnel.api_key"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("config mutation output missing %q: %q", expected, output)
		}
	}
}

func TestTunnelAdminConfiguredInputsDoNotBypassSecretOwnershipOrRetainVerification(t *testing.T) {
	cfg := config.Default()
	if err := setConfigValue(&cfg, "tunnel.admin.key", "secret"); err == nil || !strings.Contains(err.Error(), "canonical secret setting service") {
		t.Fatalf("admin key low-level set error = %v", err)
	}
	if err := setConfigValue(&cfg, "tunnel.admin.organization_id", "org_one"); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.Admin.Verified = true
	cfg.Tunnel.Admin.ReadAccess = true
	cfg.Tunnel.Admin.ManageAccess = true
	if err := setConfigValue(&cfg, "tunnel.admin.workspace_id", "ws_one"); err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.Admin.OrganizationID != "" || cfg.Tunnel.Admin.WorkspaceID != "ws_one" || cfg.Tunnel.Admin.TenantID != "" {
		t.Fatalf("admin scope is not exclusive: %#v", cfg.Tunnel)
	}
	if cfg.Tunnel.Admin.Verified || cfg.Tunnel.Admin.ReadAccess || cfg.Tunnel.Admin.ManageAccess {
		t.Fatalf("scope mutation retained stale verification: %#v", cfg.Tunnel)
	}
}

func TestIntegrationConfigTraversal(t *testing.T) {
	cfg := config.Default()
	value, err := getConfigValue(cfg, "integrations")
	if err != nil {
		t.Fatal(err)
	}
	integrations, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("integrations = %#v", value)
	}
	ponytail, ok := integrations["ponytail"].(map[string]any)
	if !ok || ponytail["active"] != true || ponytail["mode"] != "full" {
		t.Fatalf("ponytail = %#v", integrations["ponytail"])
	}
	mode, err := getConfigValue(cfg, "integrations.ponytail.mode")
	if err != nil || mode != "full" {
		t.Fatalf("ponytail mode = %#v %v", mode, err)
	}
	leaf, err := getConfigValue(cfg, "integrations.caveman.active")
	if err != nil || leaf != true {
		t.Fatalf("caveman leaf = %#v %v", leaf, err)
	}
	cavemanMode, err := getConfigValue(cfg, "integrations.caveman.mode")
	if err != nil || cavemanMode != "full" {
		t.Fatalf("caveman mode = %#v %v", cavemanMode, err)
	}
	fanout, ok := integrations["fanout"].(map[string]any)
	if !ok || fanout["active"] != true || fanout["mode"] != "auto" {
		t.Fatalf("fanout = %#v", integrations["fanout"])
	}
	fanoutMode, err := getConfigValue(cfg, "integrations.fanout.mode")
	if err != nil || fanoutMode != "auto" {
		t.Fatalf("fanout mode = %#v %v", fanoutMode, err)
	}
}

func TestInteractiveConfigKeyIsRemoved(t *testing.T) {
	cfg := config.Default()
	if err := setConfigValue(&cfg, "interactive", "false"); err == nil || !strings.Contains(err.Error(), "unsupported config key") {
		t.Fatalf("set interactive err=%v", err)
	}
	if _, err := getConfigValue(cfg, "interactive"); err == nil || !strings.Contains(err.Error(), "unsupported config key") {
		t.Fatalf("get interactive err=%v", err)
	}
}

func TestSensitiveConfigValuesAreRedacted(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "secret"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-secret"
	cfg.Tunnel.APIKey = "secret"
	cfg.Tunnel.Admin.Key = "tunnel-admin-secret"
	for _, key := range []string{"http.mcp.auth.token_hash", "http.admin.auth.token_hash", "tunnel.api_key", "tunnel.admin.key"} {
		value, err := getConfigValue(cfg, key)
		if err != nil {
			t.Fatal(err)
		}
		if value != redactedValue {
			t.Fatalf("%s = %#v, want %q", key, value, redactedValue)
		}
	}
}

func TestConfigListDoesNotHTMLEscapeRedactionMarker(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "secret"
	cfg.HTTP.Admin.Auth.TokenHash = "secret"
	cfg.Tunnel.APIKey = "secret"
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printConfigSelection(cmd, cfg, "", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, `\u003c`) || strings.Contains(text, `\u003e`) {
		t.Fatalf("redaction marker was HTML-escaped: %s", text)
	}
	if !strings.Contains(text, `http.mcp.auth.token_hash = "<redacted>"`) || !strings.Contains(text, `tunnel.api_key = "<redacted>"`) {
		t.Fatalf("redaction marker missing: %s", text)
	}
}

func TestConfigParentTraversalAndFlatOutput(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-secret"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-secret"
	cfg.Tunnel.APIKey = "tunnel-secret"

	parent, err := getConfigValue(cfg, "http.admin")
	if err != nil {
		t.Fatal(err)
	}
	object, ok := parent.(map[string]any)
	if !ok || object["enabled"] != true || object["port"] != int64(37422) {
		t.Fatalf("admin subtree = %#v", parent)
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printConfigSelection(cmd, cfg, "http.admin", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"http.admin.enabled = true", "http.admin.port = 37422"} {
		if !strings.Contains(text, want) {
			t.Fatalf("flat output missing %q:\n%s", want, text)
		}
	}
}

func TestConfigJSONOutputAndRedaction(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-secret"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-secret"
	cfg.Tunnel.APIKey = "tunnel-secret"

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printConfigSelection(cmd, cfg, "", true, configOutputOptions{json: true}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "mcp-secret") || strings.Contains(text, "admin-secret") || strings.Contains(text, "tunnel-secret") {
		t.Fatalf("secret leaked in JSON output: %s", text)
	}
	for _, want := range []string{`"auth"`, `"token_hash": "<redacted>"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("JSON output missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, `"token_hash": "<redacted>"`) != 2 {
		t.Fatalf("JSON output should redact both HTTP token hashes: %s", text)
	}
}

func TestConfigJSONResultStaysCleanUnderVerboseAndDebug(t *testing.T) {
	for _, flag := range []string{"--verbose", "--debug"} {
		t.Run(flag, func(t *testing.T) {
			defer configformat.SetRootPath("")
			root := t.TempDir()
			if err := configformat.SetRootPath(root); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.HTTP.MCP.Auth.TokenHash = "mcp-secret"
			if err := config.Save(cfg); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			cmd := newRootCommand()
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"--config-dir", root, flag, "config", "list", "--json"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("%s stdout is not pure JSON: %q err=%v", flag, stdout.String(), err)
			}
			if strings.Contains(stdout.String(), "mcp-secret") || strings.Contains(stdout.String(), "token_hash") || !strings.Contains(stdout.String(), `"http.mcp.auth.token": "mcp_********legacy"`) {
				t.Fatalf("%s JSON safe setting projection=%q", flag, stdout.String())
			}
			if stderr.Len() == 0 {
				t.Fatalf("%s expected diagnostics on stderr", flag)
			}
		})
	}
}

func TestConfigHumanListUsesPresenterRowsWhilePlainRemainsCompatible(t *testing.T) {
	cfg := config.Default()

	var humanOutput bytes.Buffer
	human := &cobra.Command{}
	human.SetOut(presentation.WrapWriter(&humanOutput, presentation.Capabilities{Width: 100, Unicode: true, Interactive: true}))
	setCommandPresentationTitle(human, "Configuration")
	if err := printConfigSelection(human, cfg, "http.admin", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(human, nil)
	for _, expected := range []string{"┌  Configuration", "│  ▸ http.admin", "│  Key", "Value", "http.admin.enabled", "true", "http.admin.port", "37422", "└  Done"} {
		if !strings.Contains(humanOutput.String(), expected) {
			t.Fatalf("human config output missing %q: %q", expected, humanOutput.String())
		}
	}

	var plainOutput bytes.Buffer
	plain := &cobra.Command{}
	plain.SetOut(&plainOutput)
	if err := printConfigSelection(plain, cfg, "http.admin", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"http.admin.enabled = true", "http.admin.port = 37422"} {
		if !strings.Contains(plainOutput.String(), expected) {
			t.Fatalf("plain config output missing %q: %q", expected, plainOutput.String())
		}
	}
}

func TestUniversalConfigHumanListGroupsSettingsByFirstChildKey(t *testing.T) {
	isolateUniversalConfigCLI(t)

	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 100, Unicode: true, Interactive: true}))
	setCommandPresentationTitle(cmd, "Configuration")
	if err := printSettingSelection(cmd, application.NewSettingService(), "", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(cmd, nil)

	text := output.String()
	for _, expected := range []string{
		"┌  Configuration",
		"│  ├─ http",
		"Key",
		"Value",
		"Accepts",
		"http.admin.enabled",
		"true | false",
		"http.admin.port",
		"integer 1..65535",
		"│  ├─ approval",
		"approval.semantic.enabled",
		"│  ├─ integrations",
		"integrations.codegraph.enabled",
		"│  └─ tunnel",
		"tunnel.admin.enabled",
		"└  Done",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("grouped config list missing %q: %q", expected, text)
		}
	}
	if !(strings.Index(text, "│  ├─ approval") < strings.Index(text, "│  ├─ http") &&
		strings.Index(text, "│  ├─ http") < strings.Index(text, "│  ├─ integrations") &&
		strings.Index(text, "│  ├─ integrations") < strings.Index(text, "│  └─ tunnel")) {
		t.Fatalf("config scopes are not sorted: %q", text)
	}
	if strings.Contains(text, "\n▸  http") || strings.Contains(text, "\n▸  approval") {
		t.Fatalf("config scopes escaped nested list level: %q", text)
	}

	headerLines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Key") && strings.Contains(line, "Value") && strings.Contains(line, "Accepts") {
			headerLines = append(headerLines, line)
		}
	}
	if len(headerLines) < 2 {
		t.Fatalf("expected repeated scoped table headers: %q", text)
	}
	for _, line := range headerLines[1:] {
		if line != headerLines[0] {
			t.Fatalf("scoped table columns do not share global widths:\nfirst=%q\nother=%q", headerLines[0], line)
		}
	}
	adminLine := configListLineContaining(text, "http.admin.enabled")
	llmLine := configListLineContaining(text, "llm.provider")
	if adminLine == "" || llmLine == "" {
		t.Fatalf("missing cross-scope rows: %q", text)
	}
	if strings.Index(adminLine, "http.admin.enabled") != strings.Index(llmLine, "llm.provider") ||
		strings.Index(adminLine, "true | false") != strings.Index(llmLine, "<provider-id>") {
		t.Fatalf("cross-scope Key/Accepts columns are not globally aligned:\nadmin=%q\nllm=%q", adminLine, llmLine)
	}
}

func TestUniversalConfigHumanListCanHideAcceptsAndStacksOnNarrowTerminals(t *testing.T) {
	isolateUniversalConfigCLI(t)
	service := application.NewSettingService()

	var narrow bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(presentation.WrapWriter(&narrow, presentation.Capabilities{Width: 48, Unicode: true, Interactive: true}))
	if err := printSettingSelection(cmd, service, "http", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(cmd, nil)
	text := narrow.String()
	semanticText := strings.NewReplacer("│", " ", "◆", " ", "▸", " ", "├", " ", "┌", " ", "└", " ", "─", " ", "—", " ").Replace(text)
	collapsed := strings.Join(strings.Fields(semanticText), " ")
	for _, want := range []string{"http.mcp.port", "Accepts integer 1..65535", "http.exposure.mode", "Accepts none | all | 0.0.0.0 | interfaces"} {
		if !strings.Contains(collapsed, want) {
			t.Fatalf("narrow config list missing semantic value %q: %q", want, text)
		}
	}

	var hidden bytes.Buffer
	hide := &cobra.Command{}
	hide.SetOut(presentation.WrapWriter(&hidden, presentation.Capabilities{Width: 100, Unicode: true, Interactive: true}))
	if err := printSettingSelection(hide, service, "http", true, configOutputOptions{noAccepts: true}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(hide, nil)
	if strings.Contains(hidden.String(), "Accepts") || strings.Contains(hidden.String(), "accepts:") || strings.Contains(hidden.String(), "integer 1..65535") {
		t.Fatalf("--no-accepts presentation still contains accepted-value hints: %q", hidden.String())
	}
}

func TestConfigListNoAcceptsUsesCanonicalAliasAuthority(t *testing.T) {
	rootPath := isolateUniversalConfigCLI(t)
	root := newRootCommand()
	canonical, _, err := root.Find([]string{"config", "list"})
	if err != nil {
		t.Fatal(err)
	}
	alias, remaining, err := root.Find([]string{"config", "ls"})
	if err != nil || alias != canonical || len(remaining) != 0 {
		t.Fatalf("config ls did not resolve to canonical list command: canonical=%p alias=%p remaining=%v err=%v", canonical, alias, remaining, err)
	}
	if flag := canonical.Flags().Lookup("no-accepts"); flag == nil {
		t.Fatal("canonical config list command is missing --no-accepts")
	}
	help := renderSideEffectFreeHelp(t, []string{"config", "ls", "--help"})
	if count := strings.Count(help, "--no-accepts"); count != 1 {
		t.Fatalf("config list/ls help exposes --no-accepts %d times:\n%s", count, help)
	}

	var output bytes.Buffer
	root.SetOut(presentation.WrapWriter(&output, presentation.Capabilities{Width: 120, Unicode: true, Interactive: true}))
	root.SetErr(&output)
	root.SetArgs([]string{"--config-dir", rootPath, "config", "ls", "http", "--no-accepts"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"│  └─ http", "http.mcp.enabled", "http.mcp.port"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config ls --no-accepts missing %q: %q", want, text)
		}
	}
	for _, forbidden := range []string{"Accepts", "accepts:", "true | false", "integer 1..65535"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("config ls --no-accepts rendered hint %q: %q", forbidden, text)
		}
	}
}

func TestConfigListNoAcceptsDoesNotChangeJSONOrPlainProjection(t *testing.T) {
	isolateUniversalConfigCLI(t)
	service := application.NewSettingService()

	renderJSON := func(noAccepts bool) string {
		t.Helper()
		var output bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&output)
		if err := printSettingSelection(cmd, service, "http", true, configOutputOptions{json: true, noAccepts: noAccepts}); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	if without, with := renderJSON(false), renderJSON(true); without != with {
		t.Fatalf("--no-accepts changed JSON projection:\ndefault=%q\nhidden =%q", without, with)
	} else if strings.Contains(strings.ToLower(with), "accepts") {
		t.Fatalf("JSON projection contains human accepted-value decoration: %q", with)
	}

	renderPlain := func(noAccepts bool) string {
		t.Helper()
		var output bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&output)
		if err := printSettingSelection(cmd, service, "http", true, configOutputOptions{noAccepts: noAccepts}); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	if without, with := renderPlain(false), renderPlain(true); without != with {
		t.Fatalf("--no-accepts changed plain projection:\ndefault=%q\nhidden =%q", without, with)
	} else if strings.Contains(with, "Accepts") || strings.Contains(with, "accepts:") {
		t.Fatalf("plain projection contains human accepted-value decoration: %q", with)
	}
}

func TestUniversalConfigHumanListDimsAcceptsValues(t *testing.T) {
	isolateUniversalConfigCLI(t)

	var output bytes.Buffer
	caps := presentation.Capabilities{Width: 180, Unicode: true, Color: true, Interactive: true}
	cmd := &cobra.Command{}
	cmd.SetOut(presentation.WrapWriter(&output, caps))
	if err := printSettingSelection(cmd, application.NewSettingService(), "", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(cmd, nil)

	theme := presentation.NewTheme(caps)
	for _, want := range []string{
		theme.Render(presentation.RoleMuted, "true | false"),
		theme.Render(presentation.RoleMuted, "<provider-id>"),
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("config accepts value is not dimmed with shared muted role %q: %q", want, output.String())
		}
	}
}

func configListLineContaining(output, value string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, value) {
			return line
		}
	}
	return ""
}

func TestConfigRichPaletteSeparatesStructureLabelsAndValues(t *testing.T) {
	cfg := config.Default()
	var output bytes.Buffer
	caps := presentation.Capabilities{Width: 100, Unicode: true, Color: true, Interactive: true}
	cmd := &cobra.Command{}
	cmd.SetOut(presentation.WrapWriter(&output, caps))
	setCommandPresentationTitle(cmd, "Configuration")
	if err := printConfigSelection(cmd, cfg, "http.admin", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(cmd, nil)
	theme := presentation.NewTheme(caps)
	text := output.String()
	for _, expected := range []string{
		theme.Render(presentation.RoleRail, "┌"),
		theme.Render(presentation.RoleRail, "│") + "  " + theme.Render(presentation.RoleStructure, "▸") + " " + theme.Render(presentation.RoleHeading, "http.admin"),
		theme.Render(presentation.RoleLabel, "http.admin.enabled"),
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("config palette missing %q: %q", expected, text)
		}
	}
	for _, forbidden := range []string{
		theme.Render(presentation.RoleActive, "http.admin"),
		theme.Render(presentation.RoleStructure, "http.admin"),
		theme.Render(presentation.RoleActive, "true"),
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("config palette over-colored settled text %q: %q", forbidden, text)
		}
	}
}

func TestConfigScalarGetUsesHumanFrameAndKeepsPlainValueContract(t *testing.T) {
	cfg := config.Default()
	var humanOutput bytes.Buffer
	human := &cobra.Command{}
	human.SetOut(presentation.WrapWriter(&humanOutput, presentation.Capabilities{Width: 100, Unicode: true, Interactive: true}))
	setCommandPresentationTitle(human, "Configuration")
	if err := printConfigSelection(human, cfg, "http.mcp.port", false, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(human, nil)
	for _, want := range []string{"┌  Configuration", "http.mcp.port — 37421", "└  Done"} {
		if !strings.Contains(humanOutput.String(), want) {
			t.Fatalf("human scalar config get missing %q: %q", want, humanOutput.String())
		}
	}

	var plainOutput bytes.Buffer
	plain := &cobra.Command{}
	plain.SetOut(&plainOutput)
	if err := printConfigSelection(plain, cfg, "http.mcp.port", false, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	if plainOutput.String() != "37421\n" {
		t.Fatalf("plain scalar config get = %q", plainOutput.String())
	}
}

func TestSettingScalarGetUsesHumanFrameAndKeepsPlainValueContract(t *testing.T) {
	isolateUniversalConfigCLI(t)
	service := application.NewSettingService()

	var humanOutput bytes.Buffer
	human := &cobra.Command{}
	human.SetOut(presentation.WrapWriter(&humanOutput, presentation.Capabilities{Width: 100, Unicode: true, Interactive: true}))
	setCommandPresentationTitle(human, "Configuration")
	if err := printSettingSelection(human, service, "http.mcp.port", false, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	closeCommandProgress(human, nil)
	for _, want := range []string{"┌  Configuration", "http.mcp.port — 37421", "└  Done"} {
		if !strings.Contains(humanOutput.String(), want) {
			t.Fatalf("human setting get missing %q: %q", want, humanOutput.String())
		}
	}

	var plainOutput bytes.Buffer
	plain := &cobra.Command{}
	plain.SetOut(&plainOutput)
	if err := printSettingSelection(plain, service, "http.mcp.port", false, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	if plainOutput.String() != "37421\n" {
		t.Fatalf("plain setting get = %q", plainOutput.String())
	}
}

func TestConfigGetScalarInteractiveLifecycleIsFrameFirst(t *testing.T) {
	rootPath := isolateUniversalConfigCLI(t)
	text, err := executeInteractiveLifecycleCommand(rootPath, "config", "get", "http.mcp.port")
	if err != nil {
		t.Fatal(err)
	}
	assertSingleHumanWorkflow(t, text, "Configuration", "Done")
	frame := strings.Index(text, "┌  Configuration")
	value := strings.Index(text, "http.mcp.port — 37421")
	if frame < 0 || value <= frame {
		t.Fatalf("interactive scalar result escaped before frame: %q", text)
	}
}

func TestConfigCommandAliases(t *testing.T) {
	root := newRootCommand()
	cmd, _, err := root.Find([]string{"config"})
	if err != nil {
		t.Fatal(err)
	}
	if convert, _, err := cmd.Find([]string{"convert"}); err == nil && convert != cmd {
		t.Fatalf("convert command still exposed: %v", convert)
	}
	if transform, _, err := cmd.Find([]string{"transform"}); err == nil && transform != cmd {
		t.Fatalf("transform alias still exposed: %v", transform)
	}
	verify, _, err := cmd.Find([]string{"validate"})
	if err != nil || verify.Name() != "verify" {
		t.Fatalf("validate alias = %v %v", verify, err)
	}
	for _, subcommand := range cmd.Commands() {
		if subcommand.Name() == "reload" {
			t.Fatal("config reload command is still registered")
		}
	}
	root.SetArgs([]string{"config", "reload"})
	root.SilenceUsage = true
	root.SilenceErrors = true
	if err := root.Execute(); err == nil {
		t.Fatal("config reload positional fallback unexpectedly succeeded")
	}
}

func TestCurrentConfigCommandsAdvertiseJSONOnly(t *testing.T) {
	root := newRootCommand()
	initCmd, _, err := root.Find([]string{"init"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"format", "json", "yaml", "toml"} {
		if flag := initCmd.Flags().Lookup(name); flag != nil {
			t.Fatalf("init still exposes storage-format flag --%s", name)
		}
	}
	configCmd, _, err := root.Find([]string{"config"})
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range configCmd.Commands() {
		if child.Name() == "convert" {
			t.Fatal("config convert command is still registered")
		}
		for _, alias := range child.Aliases {
			if alias == "transform" {
				t.Fatalf("config command %s still exposes transform alias", child.Name())
			}
		}
	}
	for _, name := range []string{"path", "get", "list", "why", "diff", "set", "unset", "rotate", "reveal", "verify", "export", "import"} {
		found, _, err := configCmd.Find([]string{name})
		if err != nil || found == nil || found.Name() != name {
			t.Fatalf("config %s missing: found=%v err=%v", name, found, err)
		}
	}
	for _, path := range [][]string{{"init"}, {"config"}, {"config", "get"}, {"config", "list"}, {"config", "verify"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(strings.Join([]string{cmd.Use, cmd.Short, cmd.Long, cmd.Example}, "\n"))
		for _, forbidden := range []string{"yaml", "toml"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s advertises %s config support: %q", strings.Join(path, " "), forbidden, text)
			}
		}
	}
	for _, path := range [][]string{{"config", "path"}, {"config", "get"}, {"config", "list"}, {"config", "why"}, {"config", "diff"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Flags().Lookup("json") == nil {
			t.Fatalf("%s lost JSON output flag", strings.Join(path, " "))
		}
		for _, name := range []string{"format", "yaml", "toml"} {
			if flag := cmd.Flags().Lookup(name); flag != nil {
				t.Fatalf("%s still exposes --%s", strings.Join(path, " "), name)
			}
		}
	}
	reveal, _, err := root.Find([]string{"config", "reveal"})
	if err != nil {
		t.Fatal(err)
	}
	if reveal.Flags().Lookup("json") != nil {
		t.Fatal("config reveal unexpectedly exposes structured JSON output")
	}
	for _, path := range [][]string{{"config", "export"}, {"config", "import"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"format", "yaml", "toml"} {
			if cmd.Flags().Lookup(name) != nil {
				t.Fatalf("%s unexpectedly exposes --%s", strings.Join(path, " "), name)
			}
		}
	}
}

func TestConfigWhyLeafBranchAndJSON(t *testing.T) {
	leaf := configWhyCommand()
	var out bytes.Buffer
	leaf.SetOut(&out)
	leaf.SetArgs([]string{"shell.path"})
	if err := leaf.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"shell.path", "Executable search paths", "shell", "config", "Baseline"} {
		if !strings.Contains(text, want) {
			t.Fatalf("leaf why missing %q:\n%s", want, text)
		}
	}
	markdown := configWhyMarkdown([]configWhyEntry{{Spec: config.FieldSpec{Key: "example.key", Label: "Example", Description: "Example description", Kind: config.FieldString, Domain: "example", ApplicationOwner: "config", Readable: true, Writable: true, Guidance: "Use this setting.", Related: []string{"other.key"}}, Baseline: "value", HasBaseline: true}})
	for _, want := range []string{"# example.key", "**Example**", "- **Type:** `string`", "- **Domain:** `example`", "- **Application owner:** `config`", "- **Baseline:** `value`", "**Guidance:** Use this setting.", "- `other.key`"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("why markdown missing %q:\n%s", want, markdown)
		}
	}

	branch := configWhyCommand()
	out.Reset()
	branch.SetOut(&out)
	branch.SetArgs([]string{"shell"})
	if err := branch.Execute(); err != nil {
		t.Fatal(err)
	}
	text = out.String()
	if !strings.Contains(text, "shell.path") {
		t.Fatalf("branch why output:\n%s", text)
	}

	jsonCommand := configWhyCommand()
	out.Reset()
	jsonCommand.SetOut(&out)
	jsonCommand.SetArgs([]string{"shell.path", "--json"})
	if err := jsonCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	text = out.String()
	for _, want := range []string{`"key": "shell.path"`, `"label": "Executable search paths"`, `"application_owner": "config"`, `"baseline"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("json why missing %q:\n%s", want, text)
		}
	}
}

func TestConfigExplainIsRemovedWithoutAlias(t *testing.T) {
	cmd := configCommand()
	if found, _, err := cmd.Find([]string{"explain"}); err == nil && found != nil && found.Name() == "explain" {
		t.Fatalf("config explain is still registered: %v", found)
	}
	root := newRootCommand()
	root.SetArgs([]string{"config", "explain", "http.mcp.port"})
	root.SilenceUsage = true
	root.SilenceErrors = true
	if err := root.Execute(); err == nil {
		t.Fatal("config explain compatibility path unexpectedly succeeded")
	}
}

func TestUniversalConfigReadProjectionAndWriteOnlySecrets(t *testing.T) {
	root := isolateUniversalConfigCLI(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTP.MCP.Port = 40123
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	output, err := executeRequestCommandError(root, []string{"config", "list", "http"})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token_hash", "mcp-configured-hash", "admin-configured-hash"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("config list leaked %q: %s", forbidden, output)
		}
	}
	for _, want := range []string{"http.mcp.auth.token", "http.admin.auth.token", "mcp_********legacy", "admin_********legacy"} {
		if !strings.Contains(output, want) {
			t.Fatalf("config list missing %q: %s", want, output)
		}
	}

	output, err = executeRequestCommandError(root, []string{"config", "get", "http.mcp.port"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "40123" {
		t.Fatalf("config get scalar=%q", output)
	}

	_, err = executeRequestCommandError(root, []string{"config", "get", "http.mcp.auth.token"})
	if err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("write-only secret get err=%v", err)
	}
}

func TestConfigListUsesMaskedPreviewForEveryManagedSecret(t *testing.T) {
	root := isolateUniversalConfigCLI(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()

	mcpToken, _, err := application.RotateAuthToken(t.Context(), "mcp")
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := application.RotateAuthToken(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}

	service := application.NewSettingService()
	values := map[string]string{
		"telegram.token":                "123456:telegram-credential-value",
		"integrations.typesafe.api_key": "typesafe-credential-value",
	}
	for key, value := range values {
		if _, err := service.Set(t.Context(), key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.APIKey = "sk-runtime-credential-value"
	cfg.Tunnel.Admin.Key = "sk-admin-credential-value"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	values["http.mcp.auth.token"] = mcpToken
	values["http.admin.auth.token"] = adminToken
	values["tunnel.api_key"] = cfg.Tunnel.APIKey
	values["tunnel.admin.key"] = cfg.Tunnel.Admin.Key

	output, err := executeRequestCommandError(root, []string{"config", "list"})
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range values {
		masked := tracepkg.MaskSecret(value, true)
		if !strings.Contains(output, key+" = "+masked) {
			t.Fatalf("config list missing masked credential %s=%q: %s", key, masked, output)
		}
		if strings.Contains(output, key+" = configured") || strings.Contains(output, key+" = ********") || strings.Contains(output, value) {
			t.Fatalf("config list used inconsistent credential presentation for %s: %s", key, output)
		}
	}
}

func TestUniversalConfigDiffExcludesSecretMaterial(t *testing.T) {
	root := isolateUniversalConfigCLI(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTP.MCP.Port++
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	output, err := executeRequestCommandError(root, []string{"config", "diff"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "http.mcp.port") {
		t.Fatalf("config diff missing changed setting: %s", output)
	}
	for _, forbidden := range []string{"token_hash", "mcp-configured-hash", "admin-configured-hash"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("config diff leaked %q: %s", forbidden, output)
		}
	}
}

func TestUniversalConfigCredentialLifecycleIsExplicitAndMetadataGated(t *testing.T) {
	isolateUniversalConfigCLI(t)

	rotate := configRotateCommand()
	var out bytes.Buffer
	rotate.SetOut(&out)
	rotate.SetArgs([]string{"http.mcp.auth.token"})
	if err := rotate.Execute(); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(out.String())
	if !strings.HasPrefix(token, "mcp_") {
		t.Fatalf("rotate output=%q", token)
	}
	if !commandExplicitMachineOutput(rotate) {
		t.Fatal("config rotate is not marked as direct machine output")
	}

	reveal := configRevealCommand()
	if reveal.Flags().Lookup("json") != nil {
		t.Fatal("config reveal unexpectedly exposes --json")
	}
	if !commandExplicitMachineOutput(reveal) {
		t.Fatal("config reveal is not marked as direct machine output")
	}
	reveal.SetArgs([]string{"http.mcp.auth.token"})
	if err := reveal.Execute(); err == nil || !strings.Contains(err.Error(), "not revealable") {
		t.Fatalf("metadata-gated reveal err=%v", err)
	}
}

func TestUniversalConfigVerifyRetainsGlobalModeAndDelegatesSettingMode(t *testing.T) {
	isolateUniversalConfigCLI(t)
	global := configVerifyCommand()
	if err := global.Execute(); err != nil {
		t.Fatalf("global config verify failed: %v", err)
	}

	setting := configVerifyCommand()
	setting.SetArgs([]string{"tunnel.admin.key"})
	if err := setting.Execute(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "tunnel admin") {
		t.Fatalf("setting verify did not reach tunnel credential authority: %v", err)
	}
}

func TestUniversalConfigCLIRegistryCoverage(t *testing.T) {
	cmd := configCommand()
	for _, spec := range config.Settings() {
		if spec.InternalOnly {
			continue
		}
		if spec.Writable {
			assertConfigOperationCompletionContains(t, configSetCommand(), spec, completeConfigSet)
		}
		if spec.Clearable || spec.DefaultReset {
			assertConfigOperationCompletionContains(t, configUnsetCommand(), spec, completeConfigUnset)
		}
		if spec.Rotatable {
			assertConfigOperationCompletionContains(t, configRotateCommand(), spec, completeConfigRotate)
		}
		if spec.Revealable {
			assertConfigOperationCompletionContains(t, configRevealCommand(), spec, completeConfigReveal)
		}
		if spec.Verifiable {
			assertConfigOperationCompletionContains(t, configVerifyCommand(), spec, completeConfigVerify)
		}
	}
	for _, name := range []string{"path", "get", "list", "why", "diff", "set", "unset", "rotate", "reveal", "verify", "export", "import"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil || found == nil || found.Name() != name {
			t.Fatalf("config operation %s missing", name)
		}
	}
}

func assertConfigOperationCompletionContains(t *testing.T, cmd *cobra.Command, spec config.FieldSpec, completion cobra.CompletionFunc) {
	t.Helper()
	if spec.Selector != nil {
		return
	}
	values, _ := completion(cmd, nil, "")
	for _, value := range values {
		key, _, _ := strings.Cut(value, "	")
		if key == spec.Key {
			return
		}
	}
	t.Fatalf("completion for %s does not include eligible setting %s", cmd.Name(), spec.Key)
}

func isolateUniversalConfigCLI(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "config")
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-configured-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-configured-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestConfigEnvelopeCommandsUseOptionalDefaultFile(t *testing.T) {
	if defaultConfigBundleFile != "codemcp-config.json" {
		t.Fatalf("default envelope identity = %q", defaultConfigBundleFile)
	}
	if got := configBundleFile(nil); got != defaultConfigBundleFile {
		t.Fatalf("default envelope file = %q", got)
	}
	if got := configBundleFile([]string{"custom.json"}); got != "custom.json" {
		t.Fatalf("custom envelope file = %q", got)
	}
	for _, command := range []*cobra.Command{configExportCommand(), configImportCommand()} {
		if err := command.Args(command, nil); err != nil {
			t.Fatalf("%s rejected default file: %v", command.Name(), err)
		}
		if err := command.Args(command, []string{"custom.json"}); err != nil {
			t.Fatalf("%s rejected custom file: %v", command.Name(), err)
		}
		if err := command.Args(command, []string{"one.json", "two.json"}); err == nil {
			t.Fatalf("%s accepted multiple files", command.Name())
		}
	}
}

func TestConfigHasNoAllowDirSubcommand(t *testing.T) {
	for _, command := range configCommand().Commands() {
		if command.Name() == "allow-dir" {
			t.Fatal("config allow-dir should not exist")
		}
	}
}

func TestPurgeStoredSecretsRemovesPersistedCredentials(t *testing.T) {
	root := filepath.Join(t.TempDir(), "codemcp")
	if err := configformat.MarkRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tunnel.json"), []byte(`{"runtime_key_configured":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "oauth.json"), []byte(`{"version":1,"credentials":{"alpha":{"server_id":"alpha","access_token":"<secret-file>"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "upstreams.json"), []byte(`{"version":1,"upstreams":[{"id":"alpha","headers":{"Authorization":"<secret-file>"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := config.TunnelSecretEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	oauthEntries, err := mcpoauth.NewStore(filepath.Join(root, "oauth.json")).SecretEntries()
	if err != nil {
		t.Fatal(err)
	}
	upstreamEntries, err := upstream.NewStore(filepath.Join(root, "upstreams.json")).SecretEntries()
	if err != nil {
		t.Fatal(err)
	}
	entries = append(entries, oauthEntries...)
	entries = append(entries, upstreamEntries...)
	if len(entries) != 3 {
		t.Fatalf("entries = %#v", entries)
	}
	store := secretstore.New(root)
	for _, entry := range entries {
		if err := store.Set(entry, "credential"); err != nil {
			t.Fatal(err)
		}
	}
	if err := purgeStoredSecrets(root); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := store.Get(entry); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatalf("secret entry %q still exists: %v", entry, err)
		}
	}
}

func TestRemoveConfigRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "codemcp")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := configformat.MarkRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeConfigRoot(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root still exists: %v", err)
	}
	if err := removeConfigRoot(t.TempDir()); err == nil {
		t.Fatal("unsafe root was not rejected")
	}
}
