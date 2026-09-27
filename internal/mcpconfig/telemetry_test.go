package mcpconfig

import (
	"testing"

	"go.mewis.me/codemcp/internal/config"
)

func TestTelemetryPreferenceIsExcludedFromAgentConfigProjection(t *testing.T) {
	spec, ok := config.SettingByKey("telemetry.enabled")
	if !ok {
		t.Fatal("telemetry setting missing")
	}
	if AgentReadable(spec) || AgentWritable(spec) {
		t.Fatalf("telemetry privacy preference became agent-visible: %#v", spec)
	}
	if _, ok := ResolveSetting("telemetry.enabled", AccessRead); ok {
		t.Fatal("telemetry preference resolved for MCP read")
	}
	if _, ok := ResolveSetting("telemetry.enabled", AccessWrite); ok {
		t.Fatal("telemetry preference resolved for MCP write")
	}
}

func TestTelegramAuthorizationSettingsAreExcludedFromAgentConfigProjection(t *testing.T) {
	for _, key := range []string{"telegram.enabled", "telegram.allowed_user_ids"} {
		spec, ok := config.SettingByKey(key)
		if !ok {
			t.Fatalf("telegram setting %q missing", key)
		}
		if AgentReadable(spec) || AgentWritable(spec) {
			t.Fatalf("telegram authorization setting became agent-visible: %#v", spec)
		}
		if _, ok := ResolveSetting(key, AccessRead); ok {
			t.Fatalf("%q resolved for MCP read", key)
		}
		if _, ok := ResolveSetting(key, AccessWrite); ok {
			t.Fatalf("%q resolved for MCP write", key)
		}
	}
}
