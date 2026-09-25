package mcpconfig_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/mcpconfig"
	"go.mewis.me/codemcp/internal/tools"
)

func TestContractSchemasAreBoundedDraft202012Compatible(t *testing.T) {
	for name, schema := range map[string]struct {
		raw         json.RawMessage
		inputObject bool
	}{
		"list-input":  {mcpconfig.ListInputSchema, true},
		"get-input":   {mcpconfig.GetInputSchema, true},
		"set-input":   {mcpconfig.SetInputSchema, true},
		"list-output": {mcpconfig.ListOutputSchema, false},
		"get-output":  {mcpconfig.GetOutputSchema, false},
		"set-output":  {mcpconfig.SetOutputSchema, false},
	} {
		t.Run(name, func(t *testing.T) {
			if err := tools.ValidateSchemaDocument(schema.raw, schema.inputObject); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentConfigEligibilityIsExplicitAndDisabledByDefault(t *testing.T) {
	cfg := config.Default()
	if mcpconfig.Eligible(cfg, mcpconfig.AccessRead) || mcpconfig.Eligible(cfg, mcpconfig.AccessWrite) {
		t.Fatal("default config unexpectedly grants agent config access")
	}
	cfg.Auth.MCPEnabled = true
	cfg.Permissions.AllowDirs = []string{"/tmp"}
	if mcpconfig.Eligible(cfg, mcpconfig.AccessRead) || mcpconfig.Eligible(cfg, mcpconfig.AccessWrite) {
		t.Fatal("transport authentication or workspace permissions elevated global config access")
	}
	cfg.Permissions.MCPConfigRead = true
	if !mcpconfig.Eligible(cfg, mcpconfig.AccessRead) || mcpconfig.Eligible(cfg, mcpconfig.AccessWrite) {
		t.Fatal("read eligibility did not remain independent")
	}
	cfg.Permissions.MCPConfigWrite = true
	if !mcpconfig.Eligible(cfg, mcpconfig.AccessWrite) {
		t.Fatal("write eligibility opt-in was ignored")
	}
}

func TestSecretProjectionNeverContainsSecretOrMaskedPreview(t *testing.T) {
	for _, key := range []string{"auth.mcp_token", "auth.admin_token", "tunnel.api_key", "tunnel.admin.key"} {
		spec, ok := config.SettingByKey(key)
		if !ok {
			t.Fatalf("setting %q missing", key)
		}
		configured := true
		projected, ok := mcpconfig.ProjectSetting(spec, "TOP-SECRET-1234", &configured)
		if !ok {
			t.Fatalf("secret setting %q unexpectedly excluded", key)
		}
		data, err := json.Marshal(projected)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "TOP-SECRET") || strings.Contains(text, config.RedactedValue) || strings.Contains(strings.ToLower(text), "preview") {
			t.Fatalf("secret projection leaked value or masked fragment for %q: %s", key, text)
		}
		if projected.Value != nil || projected.Configured == nil || !*projected.Configured || projected.Writable {
			t.Fatalf("secret projection=%#v", projected)
		}
	}
}

func TestUnsafeCredentialBearingConfiguredValuesAreExcluded(t *testing.T) {
	for _, key := range []string{
		"tunnel.control_plane_base_url",
		"upstream.servers[<id>].url",
		"upstream.servers[<id>].command",
		"upstream.servers[<id>].args",
	} {
		spec, ok := config.SettingByKey(key)
		if !ok {
			t.Fatalf("setting %q missing", key)
		}
		if mcpconfig.AgentReadable(spec) || mcpconfig.AgentWritable(spec) {
			t.Fatalf("credential-bearing setting %q unexpectedly agent-visible: %#v", key, spec)
		}
	}
	if _, ok := mcpconfig.ResolveSetting("upstream.servers[server_a].url", mcpconfig.AccessRead); ok {
		t.Fatal("dynamic upstream URL unexpectedly readable")
	}
	if spec, ok := mcpconfig.ResolveSetting("upstream.servers[server_a].enabled", mcpconfig.AccessRead); !ok || spec.Key != "upstream.servers[server_a].enabled" {
		t.Fatalf("safe dynamic setting resolution=%#v ok=%t", spec, ok)
	}
}

func TestAgentCannotUseConfigSetToEnableItsOwnEligibility(t *testing.T) {
	for _, key := range []string{mcpconfig.ReadEligibilityKey, mcpconfig.WriteEligibilityKey} {
		spec, ok := config.SettingByKey(key)
		if !ok {
			t.Fatalf("eligibility setting %q missing", key)
		}
		if !mcpconfig.AgentReadable(spec) {
			t.Fatalf("eligibility setting %q should be inspectable", key)
		}
		if mcpconfig.AgentWritable(spec) {
			t.Fatalf("eligibility setting %q is self-writable", key)
		}
	}
}

func TestSupportedSettingScopeExcludesInternalAndUnsafeFields(t *testing.T) {
	read := mcpconfig.SupportedSettings(mcpconfig.AccessRead)
	write := mcpconfig.SupportedSettings(mcpconfig.AccessWrite)
	if len(read) == 0 || len(write) == 0 {
		t.Fatalf("supported scope read=%d write=%d", len(read), len(write))
	}
	for _, specs := range [][]config.FieldSpec{read, write} {
		for _, spec := range specs {
			if spec.InternalOnly || spec.ValueRole == config.SettingValueInternal {
				t.Fatalf("internal setting leaked into agent scope: %#v", spec)
			}
			if strings.Contains(spec.Key, "token_hash") || spec.Key == "tunnel.control_plane_base_url" {
				t.Fatalf("unsafe setting leaked into agent scope: %s", spec.Key)
			}
		}
	}
	for _, spec := range write {
		if spec.Secret || spec.Derived || spec.Key == mcpconfig.ReadEligibilityKey || spec.Key == mcpconfig.WriteEligibilityKey {
			t.Fatalf("non-writable setting leaked into write scope: %#v", spec)
		}
	}
}

func TestBatchSummaryAndMutationResultAreValueFree(t *testing.T) {
	changes := []mcpconfig.Change{
		{Key: "server.port", Value: "4000"},
		{Key: "permissions.allow_dirs", Value: "/secret/path"},
	}
	if err := mcpconfig.ValidateChanges(changes); err != nil {
		t.Fatal(err)
	}
	summary := mcpconfig.SummarizeChanges(changes)
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "4000") || strings.Contains(string(data), "/secret/path") {
		t.Fatalf("batch summary leaked values: %s", data)
	}
	if summary.ChangeCount != 2 || len(summary.Keys) != 2 || summary.Keys[0] != "server.port" {
		t.Fatalf("summary=%#v", summary)
	}

	result := mcpconfig.MutationResult{
		State: mcpconfig.MutationRuntimeSynced, Keys: summary.Keys, ChangeCount: summary.ChangeCount,
		Outcomes: []mcpconfig.MutationOutcome{{Key: "server.port", Changed: true}, {Key: "permissions.allow_dirs", Changed: true}},
		Changed:  true, RuntimeReloaded: true, RuntimeSync: mcpconfig.RuntimeSyncCurrent,
	}
	data, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "4000") || strings.Contains(string(data), "/secret/path") {
		t.Fatalf("mutation result leaked values: %s", data)
	}
}

func TestValidateChangesRejectsDuplicateKeys(t *testing.T) {
	err := mcpconfig.ValidateChanges([]mcpconfig.Change{
		{Key: "server.port", Value: "4000"},
		{Key: " server.port ", Value: "5000"},
	})
	if err == nil {
		t.Fatal("duplicate setting keys unexpectedly accepted")
	}
}
