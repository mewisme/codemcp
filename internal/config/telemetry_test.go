package config

import (
	"encoding/json"
	"testing"
)

func TestTelemetryConfigDefaultRoundTripAndEffectivePrecedence(t *testing.T) {
	cfg := Default()
	if !cfg.Telemetry.Enabled {
		t.Fatal("telemetry default must be enabled")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Telemetry.Enabled {
		t.Fatal("telemetry enabled did not survive JSON round-trip")
	}
	state := ResolveTelemetryEnabledWithLookup(cfg, false, func(string) (string, bool) { return "", false })
	if !state.Enabled || state.Source != TelemetrySourceDefault {
		t.Fatalf("default state=%#v", state)
	}
	cfg.Telemetry.Enabled = false
	state = ResolveTelemetryEnabledWithLookup(cfg, true, func(string) (string, bool) { return "", false })
	if state.Enabled || state.Source != TelemetrySourceConfig {
		t.Fatalf("config state=%#v", state)
	}
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{"1", true}, {"true", true}, {" YES ", true}, {"On", true},
		{"0", false}, {"false", false}, {" NO ", false}, {"Off", false},
	} {
		state = ResolveTelemetryEnabledWithLookup(cfg, true, func(string) (string, bool) { return test.raw, true })
		if state.Enabled != test.want || state.Source != TelemetrySourceEnv {
			t.Fatalf("env %q => %#v", test.raw, state)
		}
	}
	state = ResolveTelemetryEnabledWithLookup(cfg, true, func(string) (string, bool) { return "invalid", true })
	if state.Enabled || state.Source != TelemetrySourceConfig {
		t.Fatalf("invalid env should be ignored: %#v", state)
	}
}

func TestTelemetrySettingMetadataAndReset(t *testing.T) {
	cfg := Default()
	spec, ok := SettingByKey("telemetry.enabled")
	if !ok || !spec.Readable || !spec.Writable || spec.ApplicationOwner != "telemetry" {
		t.Fatalf("telemetry setting metadata=%#v ok=%t", spec, ok)
	}
	if err := SetValue(&cfg, "telemetry.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Enabled {
		t.Fatal("telemetry setting did not persist false")
	}
	value, err := RawValue(cfg, "telemetry.enabled")
	if err != nil || value != "false" {
		t.Fatalf("raw telemetry=%q err=%v", value, err)
	}
	defaultValue, err := RawValue(Default(), "telemetry.enabled")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetValue(&cfg, "telemetry.enabled", defaultValue); err != nil {
		t.Fatal(err)
	}
	if !cfg.Telemetry.Enabled {
		t.Fatal("telemetry reset did not restore default true")
	}
	if _, ok := SettingByKey("telemetry.endpoint"); ok {
		t.Fatal("telemetry endpoint must not be user-configurable")
	}
}
