package config

import (
	"slices"
	"strings"
	"testing"
)

func TestUniversalSettingInventoryCoversPersistedConfigSchema(t *testing.T) {
	want := []string{
		"admin.enabled", "admin.port",
		"auth.admin_enabled", "auth.admin_token_hash", "auth.mcp_enabled", "auth.mcp_legacy_bearer", "auth.mcp_token_hash",
		"integrations.caveman.active", "integrations.caveman.mode", "integrations.codegraph.enabled", "integrations.codegraph.path",
		"integrations.ponytail.active", "integrations.ponytail.mode", "integrations.rtk.enabled", "integrations.rtk.path",
		"permissions.allow_dirs",
		"server.allow_insecure_http", "server.allow_unauthenticated_loopback", "server.enabled", "server.expose.interfaces", "server.expose.mode", "server.port",
		"shell.path",
		"tunnel.admin.enabled", "tunnel.admin.key", "tunnel.admin.manage_access", "tunnel.admin.organization_id", "tunnel.admin.read_access",
		"tunnel.admin.tenant_id", "tunnel.admin.verified", "tunnel.admin.workspace_id", "tunnel.api_key",
		"tunnel.control_plane_base_url", "tunnel.enabled", "tunnel.id", "tunnel.organization_id",
	}
	got := make([]string, 0, len(fieldSpecs))
	for _, spec := range fieldSpecs {
		got = append(got, spec.Key)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("persisted setting inventory drifted\ngot=%v\nwant=%v", got, want)
	}
}

func TestUniversalSettingMetadataIsCompleteAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range Settings() {
		if spec.Key == "" || seen[spec.Key] {
			t.Fatalf("invalid or duplicate setting identity %q", spec.Key)
		}
		seen[spec.Key] = true
		if spec.Domain == "" || spec.ApplicationOwner == "" {
			t.Fatalf("setting %q missing domain/application owner: %#v", spec.Key, spec)
		}
		if spec.Presentation == "" {
			t.Fatalf("setting %q missing presentation policy", spec.Key)
		}
		if spec.ValueRole == "" {
			t.Fatalf("setting %q missing configured/generated/derived/internal value role", spec.Key)
		}
		if !spec.InternalOnly && len(spec.ScopedCommands) == 0 && spec.ScopedExemption == "" {
			t.Fatalf("setting %q has neither scoped CLI facade nor explicit exemption", spec.Key)
		}
		if spec.InternalOnly {
			if spec.Readable || spec.Writable || spec.ReadKey != "" || spec.WriteKey != "" || spec.Presentation != SettingPresentationInternal {
				t.Fatalf("internal setting exposed public behavior: %#v", spec)
			}
		}
		if spec.Secret {
			if spec.Readable || spec.ConfiguredStateKey == "" {
				t.Fatalf("managed secret has unsafe read metadata: %#v", spec)
			}
			if spec.Presentation != SettingPresentationConfiguredState && spec.Presentation != SettingPresentationMaskedPreview {
				t.Fatalf("managed secret %q has unsafe presentation policy %q", spec.Key, spec.Presentation)
			}
			state, ok := SettingByKey(spec.ConfiguredStateKey)
			if !ok || !state.Readable || !state.Derived || state.Secret || state.InternalOnly {
				t.Fatalf("managed secret %q configured state %q is invalid: %#v ok=%t", spec.Key, spec.ConfiguredStateKey, state, ok)
			}
		}
		if spec.Writable && spec.WriteKey == "" {
			t.Fatalf("writable setting %q has no write identity", spec.Key)
		}
		if spec.Readable && spec.ReadKey == "" {
			t.Fatalf("readable setting %q has no read identity", spec.Key)
		}
		if spec.Selector != nil {
			if !strings.Contains(spec.Key, "[<id>]") || spec.Selector.Template != spec.Key || spec.Selector.Resource == "" || spec.Selector.InventoryOwner == "" || spec.Selector.CompletionOwner == "" || spec.Selector.IDValidator == "" || spec.Selector.IDEncoding == "" {
				t.Fatalf("dynamic selector metadata incomplete: %#v", spec)
			}
		}
	}
}

func TestTunnelAdminSettingRolesSeparateConfiguredInputsFromDerivedState(t *testing.T) {
	configured := []string{
		"tunnel.admin.enabled",
		"tunnel.admin.key",
		"tunnel.admin.organization_id",
		"tunnel.admin.workspace_id",
		"tunnel.admin.tenant_id",
	}
	for _, key := range configured {
		spec, ok := SettingByKey(key)
		if !ok || spec.ValueRole != SettingValueConfigured || !spec.Writable || spec.Derived {
			t.Fatalf("configured input %q has invalid metadata: %#v ok=%t", key, spec, ok)
		}
	}
	derived := []string{
		"tunnel.admin.key_configured",
		"tunnel.admin.configured",
		"tunnel.admin.verified",
		"tunnel.admin.read_access",
		"tunnel.admin.manage_access",
	}
	for _, key := range derived {
		spec, ok := SettingByKey(key)
		if !ok || spec.ValueRole != SettingValueDerived || spec.Writable || !spec.Derived {
			t.Fatalf("derived state %q has invalid metadata: %#v ok=%t", key, spec, ok)
		}
	}
	for _, key := range []string{"auth.mcp_token", "auth.admin_token"} {
		spec, ok := SettingByKey(key)
		if !ok || spec.ValueRole != SettingValueGenerated {
			t.Fatalf("generated setting %q has invalid metadata: %#v ok=%t", key, spec, ok)
		}
	}
}

func TestTunnelAdminRegistryUsesOnlyNestedCurrentNamespace(t *testing.T) {
	for _, spec := range Settings() {
		if strings.HasPrefix(spec.Key, "tunnel.admin_") {
			t.Fatalf("flat tunnel admin key remains in current registry: %q", spec.Key)
		}
	}
	for _, spec := range Fields() {
		if strings.HasPrefix(spec.Key, "tunnel.admin_") {
			t.Fatalf("flat tunnel admin field remains in current config schema: %q", spec.Key)
		}
	}
}

func TestUniversalSettingManagedCredentialVocabulary(t *testing.T) {
	want := map[string]struct {
		state        string
		presentation SettingPresentationPolicy
		writable     bool
		rotatable    bool
		clearable    bool
		verifiable   bool
	}{
		"auth.mcp_token":   {state: "auth.mcp_token_configured", presentation: SettingPresentationConfiguredState, rotatable: true},
		"auth.admin_token": {state: "auth.admin_token_configured", presentation: SettingPresentationConfiguredState, rotatable: true},
		"tunnel.api_key":   {state: "tunnel.api_key_configured", presentation: SettingPresentationMaskedPreview, writable: true, clearable: true},
		"tunnel.admin.key": {state: "tunnel.admin.key_configured", presentation: SettingPresentationMaskedPreview, writable: true, clearable: true, verifiable: true},
	}
	for key, expected := range want {
		spec, ok := SettingByKey(key)
		if !ok || !spec.Secret {
			t.Fatalf("managed credential %q missing: %#v ok=%t", key, spec, ok)
		}
		if spec.ConfiguredStateKey != expected.state || spec.Presentation != expected.presentation || spec.Writable != expected.writable || spec.Rotatable != expected.rotatable || spec.Clearable != expected.clearable || spec.Verifiable != expected.verifiable {
			t.Fatalf("managed credential metadata mismatch for %q: %#v", key, spec)
		}
	}
	for _, hash := range []string{"auth.mcp_token_hash", "auth.admin_token_hash"} {
		spec, ok := SettingByKey(hash)
		if !ok || !spec.InternalOnly || spec.Secret {
			t.Fatalf("storage hash %q became a user-managed secret: %#v ok=%t", hash, spec, ok)
		}
	}
	for _, spec := range SettingsByPrefix("auth") {
		if strings.HasSuffix(spec.Key, "_token_hash") || spec.Key == "auth.mcp_token" || spec.Key == "auth.admin_token" {
			t.Fatalf("internal/write-only credential leaked into readable settings: %#v", spec)
		}
	}
}

func TestUniversalDynamicSettingCatalogIsFrozen(t *testing.T) {
	want := []string{
		"tunnel.managed[<id>].description", "tunnel.managed[<id>].name", "tunnel.managed[<id>].organization_ids",
		"tunnel.managed[<id>].tenant_ids", "tunnel.managed[<id>].workspace_ids",
		"upstream.servers[<id>].allow_private_network", "upstream.servers[<id>].args", "upstream.servers[<id>].auth.scope",
		"upstream.servers[<id>].auth.type", "upstream.servers[<id>].bearer_token_env_var", "upstream.servers[<id>].command",
		"upstream.servers[<id>].cwd", "upstream.servers[<id>].disabled_tools", "upstream.servers[<id>].enabled",
		"upstream.servers[<id>].expose", "upstream.servers[<id>].idle_timeout_sec", "upstream.servers[<id>].name",
		"upstream.servers[<id>].tool_prefix", "upstream.servers[<id>].tools", "upstream.servers[<id>].transport",
		"upstream.servers[<id>].url",
	}
	var got []string
	for _, spec := range Settings() {
		if spec.Selector != nil {
			got = append(got, spec.Key)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("dynamic setting catalog drifted\ngot=%v\nwant=%v", got, want)
	}
}

func TestUniversalDynamicSettingSelectorsAreUnambiguous(t *testing.T) {
	tests := []struct {
		key      string
		template string
		id       string
	}{
		{key: "upstream.servers[docs.v2].enabled", template: "upstream.servers[<id>].enabled", id: "docs.v2"},
		{key: "upstream.servers[docs%2Ev2].auth.type", template: "upstream.servers[<id>].auth.type", id: "docs.v2"},
		{key: "tunnel.managed[tun_demo].description", template: "tunnel.managed[<id>].description", id: "tun_demo"},
	}
	for _, test := range tests {
		match, ok := MatchSettingSelector(test.key)
		if !ok || match.Spec.Key != test.template || match.ResourceID != test.id {
			t.Fatalf("selector %q => %#v ok=%t", test.key, match, ok)
		}
	}
	for _, invalid := range []string{
		"upstream.servers[<id>].enabled",
		"upstream.servers.docs.v2.enabled",
		"upstream.servers[].enabled",
		"upstream.servers[docs/v2].enabled",
		"upstream.servers[docs%2Fv2].enabled",
		"tunnel.managed[<id>].name",
		"tunnel.managed[].name",
		"mcp.profiles[default].enabled",
		"logs[default].level",
	} {
		if match, ok := MatchSettingSelector(invalid); ok {
			t.Fatalf("invalid selector %q resolved to %#v", invalid, match)
		}
	}
}

func TestUniversalSettingRegistryDoesNotModelResourceCRUDOrGenericMCPProfiles(t *testing.T) {
	for _, spec := range Settings() {
		for _, forbidden := range []string{".create", ".delete", ".register", ".unregister"} {
			if strings.Contains(spec.Key, forbidden) {
				t.Fatalf("resource CRUD mislabeled as setting: %q", spec.Key)
			}
		}
		if strings.HasPrefix(spec.Key, "mcp.") {
			t.Fatalf("generic MCP profile setting introduced without a product domain: %q", spec.Key)
		}
	}
}
