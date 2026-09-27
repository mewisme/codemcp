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
