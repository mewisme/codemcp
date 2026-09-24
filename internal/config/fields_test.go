package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestFieldSetValuePreservesTypedBehavior(t *testing.T) {
	cfg := Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	for key, value := range map[string]string{
		"server.port": "4000", "server.expose": "true", "admin.enabled": "false",
		"integrations.ponytail.active": "false", "integrations.ponytail.mode": "ULTRA", "integrations.caveman.active": "false", "integrations.caveman.mode": "WENYAN-ULTRA",
		"integrations.rtk.enabled": "false", "integrations.rtk.path": "/opt/rtk/bin/rtk",
		"integrations.codegraph.enabled": "true", "integrations.codegraph.path": "/opt/codegraph/bin/codegraph",
		"permissions.allow_dirs": "/tmp\n/var/tmp", "shell.path": "/opt/tools,/usr/local/custom/bin",
	} {
		if err := SetValue(&cfg, key, value); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	if cfg.Server.Port != 4000 || cfg.Server.Expose.Mode != ExposureWildcard || cfg.Admin.Enabled || cfg.Integrations.Ponytail.Active || cfg.Integrations.Ponytail.Mode != "ultra" || cfg.Integrations.Caveman.Active || cfg.Integrations.Caveman.Mode != "wenyan-ultra" || cfg.Integrations.RTK.Enabled || cfg.Integrations.RTK.Path != "/opt/rtk/bin/rtk" || !cfg.Integrations.CodeGraph.Enabled || cfg.Integrations.CodeGraph.Path != "/opt/codegraph/bin/codegraph" || len(cfg.Permissions.AllowDirs) != 2 || len(cfg.Shell.Path) != 2 {
		t.Fatalf("cfg=%#v", cfg)
	}
	if value, err := RawValue(cfg, "shell.path"); err != nil || value != "/opt/tools,/usr/local/custom/bin" {
		t.Fatalf("shell path value=%q err=%v", value, err)
	}
}

func TestInteractiveFieldIsRemoved(t *testing.T) {
	if _, ok := FieldByKey("interactive"); ok {
		t.Fatal("interactive field still exposed")
	}
	cfg := Default()
	if err := SetValue(&cfg, "interactive", "false"); err == nil || !strings.Contains(err.Error(), "unsupported config key") {
		t.Fatalf("err=%v", err)
	}
}

func TestFieldSetValueValidationIsTransactional(t *testing.T) {
	cfg := Default()
	cfg.Auth.MCPTokenHash = "mcp"
	cfg.Auth.AdminTokenHash = "admin"
	original := cfg.Server.Port
	if err := SetValueValidated(&cfg, "server.port", "70000"); err == nil || !strings.Contains(err.Error(), "between 1 and 65535") {
		t.Fatalf("err=%v", err)
	}
	if cfg.Server.Port != original {
		t.Fatalf("invalid value mutated config: %d", cfg.Server.Port)
	}
	if err := SetValueValidated(&cfg, "server.enabled", "false"); err == nil || !strings.Contains(err.Error(), "at least one MCP transport") {
		t.Fatalf("last MCP transport disable err=%v", err)
	}
	if !cfg.Server.Enabled {
		t.Fatal("invalid transport update mutated config")
	}
	if err := SetValue(&cfg, "integrations.ponytail.mode", "review"); err == nil || err.Error() != "integrations.ponytail.mode must be lite, full, or ultra" {
		t.Fatalf("ponytail err=%v", err)
	}
	if err := SetValue(&cfg, "integrations.caveman.mode", "wenyan"); err == nil || !strings.Contains(err.Error(), "wenyan-lite") {
		t.Fatalf("caveman err=%v", err)
	}
	if err := SetValueValidated(&cfg, "integrations.rtk.path", "relative/rtk"); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("rtk path err=%v", err)
	}
	if err := SetValueValidated(&cfg, "integrations.codegraph.path", "relative/codegraph"); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("codegraph path err=%v", err)
	}
}

func TestFieldReadOnlyAndSensitiveValuesNeverExposeSecrets(t *testing.T) {
	cfg := Default()
	cfg.Auth.MCPTokenHash = "mcp-secret"
	cfg.Auth.AdminTokenHash = "admin-secret"
	cfg.Tunnel.APIKey = "runtime-secret"
	cfg.Tunnel.AdminKey = "admin-tunnel-secret"
	for _, key := range []string{"auth.mcp_token_hash", "auth.admin_token_hash", "tunnel.api_key", "tunnel.admin_key"} {
		spec, ok := FieldByKey(key)
		if !ok || !spec.Sensitive {
			t.Fatalf("spec=%#v ok=%t", spec, ok)
		}
		value, err := DisplayValue(cfg, spec)
		if err != nil || value != "configured" {
			t.Fatalf("%s display=%q err=%v", key, value, err)
		}
	}
	tree, err := RedactedTree(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(strings.TrimSpace(toJSONForTest(t, tree)))
	for _, secret := range []string{"mcp-secret", "admin-secret", "runtime-secret", "admin-tunnel-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	if value, err := RedactedValueAt(cfg, "auth.mcp_token_hash"); err != nil || value != RedactedValue {
		t.Fatalf("redacted value=%#v err=%v", value, err)
	}
}

func TestFieldsReturnsDefensiveCopy(t *testing.T) {
	fields := Fields()
	if len(fields) == 0 {
		t.Fatal("no fields")
	}
	fields[0].Key = "mutated"
	for index := range fields {
		if len(fields[index].Options) > 0 {
			fields[index].Options[0] = "mutated"
		}
		if len(fields[index].Values) > 0 {
			fields[index].Values[0].Value = "mutated"
		}
		if len(fields[index].Related) > 0 {
			fields[index].Related[0] = "mutated"
		}
	}
	next := Fields()
	if next[0].Key == "mutated" {
		t.Fatal("field key mutation escaped copy")
	}
	for _, spec := range next {
		for _, option := range spec.Options {
			if option == "mutated" {
				t.Fatal("field option mutation escaped copy")
			}
		}
		for _, value := range spec.Values {
			if value.Value == "mutated" {
				t.Fatal("field value metadata mutation escaped copy")
			}
		}
		for _, related := range spec.Related {
			if related == "mutated" {
				t.Fatal("field related metadata mutation escaped copy")
			}
		}
	}
}

func TestExplainResolvesLeafBranchAndRoot(t *testing.T) {
	leaf, err := Explain("shell.path")
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Branch || leaf.Kind != FieldList || leaf.Key != "shell.path" || leaf.Label != "Executable search paths" {
		t.Fatalf("leaf=%#v", leaf)
	}
	branch, err := Explain("shell")
	if err != nil {
		t.Fatal(err)
	}
	if !branch.Branch || branch.Key != "shell" || !hasExplanationChild(branch, "shell.path") {
		t.Fatalf("branch=%#v", branch)
	}
	root, err := Explain("")
	if err != nil {
		t.Fatal(err)
	}
	if !root.Branch || !hasExplanationChild(root, "shell") || !hasExplanationChild(root, "server") {
		t.Fatalf("root=%#v", root)
	}
	if _, err := Explain("integrations.ponytail.enabled"); err == nil || !strings.Contains(err.Error(), "unsupported config key") {
		t.Fatalf("legacy alias unexpectedly resolved: %v", err)
	}
	if _, err := Explain("does.not.exist"); err == nil || !strings.Contains(err.Error(), "unsupported config key") {
		t.Fatalf("unsupported err=%v", err)
	}
}

func TestExplainSchemaKeysIncludeBranchesAndLeaves(t *testing.T) {
	keys := SchemaKeys()
	for _, want := range []string{"server", "server.expose", "server.expose.mode", "shell", "shell.path"} {
		if !slices.Contains(keys, want) {
			t.Fatalf("schema keys missing %q: %#v", want, keys)
		}
	}
}

func TestFieldRegistryCoversConfigSchema(t *testing.T) {
	leaves := map[string]bool{}
	collectConfigSchemaLeaves(reflect.TypeOf(Config{}), "", leaves)
	for key := range leaves {
		if _, ok := FieldByKey(key); !ok {
			t.Fatalf("config schema leaf %q has no FieldSpec", key)
		}
	}
	for _, spec := range Fields() {
		if !leaves[spec.Key] {
			t.Fatalf("FieldSpec %q has no config schema leaf", spec.Key)
		}
		if len(spec.Values) > 0 {
			if len(spec.Values) != len(spec.Options) {
				t.Fatalf("field %q value descriptions do not match options", spec.Key)
			}
			for index, value := range spec.Values {
				if value.Value != spec.Options[index] {
					t.Fatalf("field %q value[%d]=%q option=%q", spec.Key, index, value.Value, spec.Options[index])
				}
			}
		}
	}
}

func collectConfigSchemaLeaves(value reflect.Type, prefix string, leaves map[string]bool) {
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			collectConfigSchemaLeaves(fieldType, key, leaves)
			continue
		}
		leaves[key] = true
	}
}

func hasExplanationChild(parent Explanation, key string) bool {
	for _, child := range parent.Children {
		if child.Key == key {
			return true
		}
	}
	return false
}

func TestFieldPresentationMetadataCoversRegistry(t *testing.T) {
	valid := map[FieldSection]bool{FieldSectionRuntime: true, FieldSectionAccess: true, FieldSectionShell: true, FieldSectionIntegrations: true, FieldSectionTunnel: true}
	seen := map[string]bool{}
	for _, spec := range Fields() {
		if strings.TrimSpace(spec.Label) == "" {
			t.Fatalf("field %s has no label", spec.Key)
		}
		if strings.TrimSpace(spec.Description) == "" {
			t.Fatalf("field %s has no description", spec.Key)
		}
		if strings.TrimSpace(spec.Details) == "" {
			t.Fatalf("field %s has no detailed explanation", spec.Key)
		}
		if !valid[spec.Section] {
			t.Fatalf("field %s has invalid section %q", spec.Key, spec.Section)
		}
		if seen[spec.Key] {
			t.Fatalf("duplicate field key %s", spec.Key)
		}
		if spec.Kind == FieldEnum {
			if len(spec.Options) == 0 || len(spec.Values) != len(spec.Options) {
				t.Fatalf("enum field %s has incomplete value explanations", spec.Key)
			}
			for index, value := range spec.Values {
				if value.Value != spec.Options[index] || strings.TrimSpace(value.Description) == "" {
					t.Fatalf("enum field %s value %q has incomplete explanation", spec.Key, value.Value)
				}
			}
		}
		seen[spec.Key] = true
	}
}

func TestFieldStateUsesDefaultsManagedAndNormalizedLists(t *testing.T) {
	cfg := Default()
	port, _ := FieldByKey("server.port")
	state, err := State(cfg, port)
	if err != nil || state != FieldStateDefault {
		t.Fatalf("default port state=%q err=%v", state, err)
	}
	cfg.Server.Port++
	state, err = State(cfg, port)
	if err != nil || state != FieldStateCustom {
		t.Fatalf("custom port state=%q err=%v", state, err)
	}
	managed, _ := FieldByKey("auth.mcp_token_hash")
	state, err = State(cfg, managed)
	if err != nil || state != FieldStateManaged {
		t.Fatalf("managed state=%q err=%v", state, err)
	}
	defaults := Default()
	list, _ := FieldByKey("permissions.allow_dirs")
	defaults.Permissions.AllowDirs = []string{"/b", "/a"}
	cfg = defaults
	cfg.Permissions.AllowDirs = []string{"/a", "/b"}
	current, err := comparableFieldValue(cfg, list)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := comparableFieldValue(defaults, list)
	if err != nil {
		t.Fatal(err)
	}
	if current != baseline {
		t.Fatalf("normalized list mismatch current=%q baseline=%q", current, baseline)
	}
}

func toJSONForTest(t *testing.T, value any) string {
	t.Helper()
	data, err := configformat.Marshal(configformat.JSON, value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
