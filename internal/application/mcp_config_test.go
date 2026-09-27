package application

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/mcpconfig"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestMCPConfigReadServiceRequiresOptInAndSanitizesCanonicalSettings(t *testing.T) {
	isolateSettingServiceConfig(t)
	provider := NewMCPConfigReadService()
	if _, code := provider.Get(t.Context(), "server.port"); code != mcpconfig.ErrorAccessDenied {
		t.Fatalf("read without opt-in code=%q", code)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Permissions.MCPConfigRead = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	shortSecret := "s3cr3t"
	longSecret := strings.Repeat("long-secret-material-", 128)
	unsafeURL := "https://operator:credential@example.invalid/control"
	settings := NewSettingService()
	for _, change := range []struct {
		key   string
		value string
	}{
		{"tunnel.api_key", shortSecret},
		{"tunnel.admin.key", longSecret},
		{"tunnel.control_plane_base_url", unsafeURL},
	} {
		if _, err := settings.Set(t.Context(), change.key, change.value); err != nil {
			t.Fatalf("set %s: %v", change.key, err)
		}
	}

	for _, key := range []string{"tunnel.api_key", "tunnel.admin.key"} {
		setting, code := provider.Get(t.Context(), key)
		if code != "" {
			t.Fatalf("get %s code=%q", key, code)
		}
		if !setting.Secret || setting.Value != nil || setting.Configured == nil || !*setting.Configured {
			t.Fatalf("secret projection %s=%#v", key, setting)
		}
	}

	listed, code := provider.List(t.Context(), "tunnel")
	if code != "" {
		t.Fatalf("list code=%q", code)
	}
	data, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{shortSecret, longSecret, unsafeURL, "operator:credential"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("safe list leaked %q: %s", forbidden, text)
		}
	}
	if strings.Contains(text, "tunnel.control_plane_base_url") {
		t.Fatalf("unsafe URL setting appeared in safe list: %s", text)
	}

	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{
		ID: "remote", Name: "Remote", Enabled: true, Transport: "http",
		URL: "https://example.invalid/mcp?token=nested-secret",
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"auth.mcp_token_hash",
		"telemetry.enabled",
		"tunnel.control_plane_base_url",
		"upstream.servers[remote].url",
		"upstream.servers[remote].command",
		"upstream.servers[remote].args",
		"upstream.servers[missing].enabled",
	} {
		if _, code := provider.Get(t.Context(), key); code != mcpconfig.ErrorUnsupportedSetting {
			t.Fatalf("unsafe/unavailable key %q code=%q", key, code)
		}
	}
	if listed, code := provider.List(t.Context(), "telemetry"); code != "" || len(listed) != 0 {
		t.Fatalf("telemetry preference list projection=%#v code=%q", listed, code)
	}

	port, code := provider.Get(t.Context(), "server.port")
	if code != "" || port.Value == nil || *port.Value == "" || port.Secret {
		t.Fatalf("safe non-secret projection=%#v code=%q", port, code)
	}
}

func TestMCPConfigSetApprovalBindingIsPrivateCanonicalAndStateBound(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	provider := NewMCPConfigReadService()
	args := map[string]any{
		"workspace_id": " ws_scope ",
		"changes": []any{
			map[string]any{"key": " server.port ", "value": "41001"},
			map[string]any{"key": "server.enabled", "value": "true"},
		},
	}
	if _, code := provider.BindSetApproval(t.Context(), args); code != mcpconfig.ErrorAccessDenied {
		t.Fatalf("write without opt-in code=%q", code)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Permissions.MCPConfigWrite = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	binding, code := provider.BindSetApproval(t.Context(), args)
	if code != "" {
		t.Fatalf("binding code=%q", code)
	}
	want := []mcpconfigwire.Change{{Key: "server.port", Value: "41001"}, {Key: "server.enabled", Value: "true"}}
	if len(binding.Changes) != len(want) {
		t.Fatalf("changes=%#v", binding.Changes)
	}
	for index := range want {
		if binding.Changes[index] != want[index] {
			t.Fatalf("change %d=%#v want=%#v", index, binding.Changes[index], want[index])
		}
	}
	if binding.ConfigRoot != root || binding.ConfigFingerprint == "" {
		t.Fatalf("binding root/fingerprint=%#v", binding)
	}
	firstFingerprint := binding.ConfigFingerprint

	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.Port++
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	binding, code = provider.BindSetApproval(t.Context(), args)
	if code != "" || binding.ConfigFingerprint == "" || binding.ConfigFingerprint == firstFingerprint {
		t.Fatalf("config change did not rotate binding fingerprint: %#v code=%q", binding, code)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
		want mcpconfig.ErrorCode
	}{
		{
			name: "operator telemetry privacy preference",
			args: map[string]any{"changes": []any{map[string]any{"key": "telemetry.enabled", "value": "false"}}},
			want: mcpconfig.ErrorUnsupportedSetting,
		},
		{
			name: "managed secret",
			args: map[string]any{"changes": []any{map[string]any{"key": "tunnel.api_key", "value": "must-never-enter-approval"}}},
			want: mcpconfig.ErrorSecretWriteForbidden,
		},
		{
			name: "credential bearing URL",
			args: map[string]any{"changes": []any{map[string]any{"key": "tunnel.control_plane_base_url", "value": "https://user:secret@example.invalid"}}},
			want: mcpconfig.ErrorUnsupportedSetting,
		},
		{
			name: "missing dynamic resource",
			args: map[string]any{"changes": []any{map[string]any{"key": "upstream.servers[missing].enabled", "value": "true"}}},
			want: mcpconfig.ErrorUnsupportedSetting,
		},
		{
			name: "duplicate canonical key",
			args: map[string]any{"changes": []any{
				map[string]any{"key": "server.port", "value": "41001"},
				map[string]any{"key": " server.port ", "value": "41002"},
			}},
			want: mcpconfig.ErrorInvalidRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := provider.BindSetApproval(t.Context(), tc.args); code != tc.want {
				t.Fatalf("code=%q want=%q", code, tc.want)
			}
		})
	}
}

func TestMCPConfigSetTraceContainsKeysButNeverUserValues(t *testing.T) {
	isolateSettingServiceConfig(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Permissions.MCPConfigWrite = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	provider := NewMCPConfigReadService()
	privateValue := "trace-private-credential-like-value"
	args := map[string]any{
		"changes": []any{
			map[string]any{"key": "tunnel.organization_id", "value": privateValue},
		},
	}
	events := make([]tracepkg.Event, 0, 16)
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) {
		events = append(events, event)
	})
	binding, code := provider.BindSetApproval(ctx, args)
	if code != "" {
		t.Fatalf("bind code=%q", code)
	}
	result, mutationErr := provider.ApplySet(ctx, args, binding)
	if mutationErr != nil || !result.Changed || result.ChangeCount != 1 {
		t.Fatalf("result=%#v mutationErr=%#v", result, mutationErr)
	}
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, privateValue) {
		t.Fatalf("config_set trace leaked user value: %s", text)
	}
	if !strings.Contains(text, "tunnel.organization_id") || !strings.Contains(text, "setting.apply") {
		t.Fatalf("config_set trace lost safe operation metadata: %s", text)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resultJSON), privateValue) {
		t.Fatalf("config_set result leaked user value: %s", resultJSON)
	}
}
