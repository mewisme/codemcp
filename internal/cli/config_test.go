package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestSetConfigValueTyped(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	if err := setConfigValue(&cfg, "server.port", "4000"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "server.expose", "true"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "admin.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "tunnel.control_plane_base_url", "https://api.openai.com"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "tunnel.organization_id", "org-test"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "features.ponytail.active", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "features.ponytail.mode", "ULTRA"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "features.caveman.active", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "features.caveman.mode", "WENYAN-ULTRA"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "permissions.allow_dirs", "/tmp,/var/tmp"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValue(&cfg, "shell.path", "/opt/tools,/usr/local/custom/bin"); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 4000 || cfg.Server.Expose.Mode != config.ExposureWildcard || cfg.Admin.Enabled || cfg.Features.Ponytail.Active || cfg.Features.Ponytail.Mode != "ultra" || cfg.Features.Caveman.Active || cfg.Features.Caveman.Mode != "wenyan-ultra" || cfg.Tunnel.ControlPlaneBaseURL != "https://api.openai.com" || cfg.Tunnel.OrganizationID != "org-test" || len(cfg.Permissions.AllowDirs) != 2 || len(cfg.Shell.Path) != 2 {
		t.Fatalf("cfg = %#v", cfg)
	}
	if err := setConfigValue(&cfg, "features.ponytail.mode", "review"); err == nil {
		t.Fatal("session-only review accepted as configured Ponytail mode")
	}
	if err := setConfigValue(&cfg, "features.caveman.mode", "wenyan"); err == nil {
		t.Fatal("Caveman runtime alias accepted as configured mode")
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
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	want := cfg
	wantErr := config.SetValueValidated(&want, "server.port", "70000")
	if wantErr == nil {
		t.Fatal("shared config validation unexpectedly accepted invalid port")
	}
	_, err := executeRequestCommandError(root, []string{"config", "set", "server.port", "70000"})
	if err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("CLI err=%v want=%v", err, wantErr)
	}
}

func TestTunnelAdminCredentialsCannotBypassVerificationThroughConfigSet(t *testing.T) {
	cfg := config.Default()
	for _, key := range []string{"tunnel.admin_key", "tunnel.admin_organization_id", "tunnel.admin_workspace_id", "tunnel.admin_tenant_id"} {
		if err := setConfigValue(&cfg, key, "value"); err == nil || !strings.Contains(err.Error(), "tunnel admin key") {
			t.Fatalf("%s error = %v", key, err)
		}
	}
}

func TestFeatureConfigTraversal(t *testing.T) {
	cfg := config.Default()
	value, err := getConfigValue(cfg, "features")
	if err != nil {
		t.Fatal(err)
	}
	features, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("features = %#v", value)
	}
	ponytail, ok := features["ponytail"].(map[string]any)
	if !ok || ponytail["active"] != true || ponytail["mode"] != "full" {
		t.Fatalf("ponytail = %#v", features["ponytail"])
	}
	mode, err := getConfigValue(cfg, "features.ponytail.mode")
	if err != nil || mode != "full" {
		t.Fatalf("ponytail mode = %#v %v", mode, err)
	}
	leaf, err := getConfigValue(cfg, "features.caveman.active")
	if err != nil || leaf != true {
		t.Fatalf("caveman leaf = %#v %v", leaf, err)
	}
	cavemanMode, err := getConfigValue(cfg, "features.caveman.mode")
	if err != nil || cavemanMode != "full" {
		t.Fatalf("caveman mode = %#v %v", cavemanMode, err)
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
	cfg.Auth.MCPTokenHash = "secret"
	cfg.Auth.AdminTokenHash = "admin-secret"
	cfg.Tunnel.APIKey = "secret"
	cfg.Tunnel.AdminKey = "tunnel-admin-secret"
	for _, key := range []string{"auth.mcp_token_hash", "auth.admin_token_hash", "tunnel.api_key", "tunnel.admin_key"} {
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
	cfg.Auth.MCPTokenHash = "secret"
	cfg.Auth.AdminTokenHash = "secret"
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
	if !strings.Contains(text, `auth.mcp_token_hash = "<redacted>"`) || !strings.Contains(text, `tunnel.api_key = "<redacted>"`) {
		t.Fatalf("redaction marker missing: %s", text)
	}
}

func TestConfigParentTraversalAndFlatOutput(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-secret"
	cfg.Auth.AdminTokenHash = "admin-secret"
	cfg.Tunnel.APIKey = "tunnel-secret"

	parent, err := getConfigValue(cfg, "admin")
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
	if err := printConfigSelection(cmd, cfg, "admin", true, configOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"admin.enabled = true", "admin.port = 37422"} {
		if !strings.Contains(text, want) {
			t.Fatalf("flat output missing %q:\n%s", want, text)
		}
	}
}

func TestConfigOutputFormatsAndRedaction(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-secret"
	cfg.Auth.AdminTokenHash = "admin-secret"
	cfg.Tunnel.APIKey = "tunnel-secret"

	for _, test := range []struct {
		name    string
		options configOutputOptions
		want    []string
	}{
		{name: "json", options: configOutputOptions{json: true}, want: []string{`"auth"`, `"mcp_token_hash": "<redacted>"`}},
		{name: "yaml", options: configOutputOptions{yaml: true}, want: []string{"auth:", `mcp_token_hash: <redacted>`}},
		{name: "toml", options: configOutputOptions{toml: true}, want: []string{"[auth]", `mcp_token_hash = '<redacted>'`}},
		{name: "format yaml", options: configOutputOptions{format: "yaml"}, want: []string{"auth:", `admin_token_hash: <redacted>`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := printConfigSelection(cmd, cfg, "", true, test.options); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			if strings.Contains(text, "mcp-secret") || strings.Contains(text, "admin-secret") || strings.Contains(text, "tunnel-secret") {
				t.Fatalf("secret leaked in %s output: %s", test.name, text)
			}
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Fatalf("%s output missing %q:\n%s", test.name, want, text)
				}
			}
		})
	}
}

func TestConfigOutputFormatConflict(t *testing.T) {
	if _, _, err := resolveConfigOutputFormat(configOutputOptions{format: "json", yaml: true}); err == nil {
		t.Fatal("expected conflicting output formats to fail")
	}
	format, selected, err := resolveConfigOutputFormat(configOutputOptions{format: "json", json: true})
	if err != nil || !selected || format != configformat.JSON {
		t.Fatalf("same format flags = %q selected=%t err=%v", format, selected, err)
	}
}

func TestConfigCommandAliases(t *testing.T) {
	cmd := configCommand()
	convert, _, err := cmd.Find([]string{"transform"})
	if err != nil || convert.Name() != "convert" {
		t.Fatalf("transform alias = %v %v", convert, err)
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
	root := newRootCommand()
	root.SetArgs([]string{"config", "reload"})
	root.SilenceUsage = true
	root.SilenceErrors = true
	if err := root.Execute(); err == nil {
		t.Fatal("config reload positional fallback unexpectedly succeeded")
	}
}

func TestConfigExplainLeafBranchAndJSON(t *testing.T) {
	leaf := configExplainCommand()
	var out bytes.Buffer
	leaf.SetOut(&out)
	leaf.SetArgs([]string{"shell.path"})
	if err := leaf.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"shell.path", "Executable search paths", "PATH"} {
		if !strings.Contains(text, want) {
			t.Fatalf("leaf explain missing %q:\n%s", want, text)
		}
	}
	markdown := configExplanationMarkdown(config.Explanation{Key: "example.key", Label: "Example", Description: "Example description", Kind: "string", Default: "value", Editable: true, Guidance: "Use this setting.", Related: []string{"other.key"}})
	for _, want := range []string{"# example.key", "**Example**", "- **Type:** `string`", "- **Default:** `value`", "**Guidance:** Use this setting.", "- `other.key`"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown explanation missing %q:\n%s", want, markdown)
		}
	}

	branch := configExplainCommand()
	out.Reset()
	branch.SetOut(&out)
	branch.SetArgs([]string{"shell"})
	if err := branch.Execute(); err != nil {
		t.Fatal(err)
	}
	text = out.String()
	if !strings.Contains(text, "shell.path") {
		t.Fatalf("branch explain output:\n%s", text)
	}

	jsonCommand := configExplainCommand()
	out.Reset()
	jsonCommand.SetOut(&out)
	jsonCommand.SetArgs([]string{"shell.path", "--json"})
	if err := jsonCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	text = out.String()
	for _, want := range []string{`"key": "shell.path"`, `"label": "Executable search paths"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("json explain missing %q:\n%s", want, text)
		}
	}
}

func TestConfigBundleCommandsUseOptionalDefaultFile(t *testing.T) {
	if defaultConfigBundleFile != "codemcp-config.cgm" {
		t.Fatalf("default bundle identity = %q", defaultConfigBundleFile)
	}
	if got := configBundleFile(nil); got != defaultConfigBundleFile {
		t.Fatalf("default bundle file = %q", got)
	}
	if got := configBundleFile([]string{"custom.cgm"}); got != "custom.cgm" {
		t.Fatalf("custom bundle file = %q", got)
	}
	for _, command := range []*cobra.Command{configExportCommand(), configImportCommand()} {
		if err := command.Args(command, nil); err != nil {
			t.Fatalf("%s rejected default file: %v", command.Name(), err)
		}
		if err := command.Args(command, []string{"custom.cgm"}); err != nil {
			t.Fatalf("%s rejected custom file: %v", command.Name(), err)
		}
		if err := command.Args(command, []string{"one.cgm", "two.cgm"}); err == nil {
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
	if err := os.WriteFile(filepath.Join(root, "upstream.json"), []byte(`{"servers":[{"id":"alpha","headers":{"Authorization":"<secret-file>"}}]}`), 0600); err != nil {
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
	upstreamEntries, err := upstream.NewStore(filepath.Join(root, "upstream.json")).SecretEntries()
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
