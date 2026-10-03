package config

import (
	"os"
)

const TelemetryEnv = "CM_TELEMETRY"

type TelemetryEnabledSource string

const (
	TelemetrySourceEnv     TelemetryEnabledSource = "env"
	TelemetrySourceConfig  TelemetryEnabledSource = "config"
	TelemetrySourceDefault TelemetryEnabledSource = "default"
)

type TelemetryEnabledState struct {
	Enabled bool
	Source  TelemetryEnabledSource
}

func ResolveTelemetryEnabled(cfg Config, configured bool) TelemetryEnabledState {
	return ResolveTelemetryEnabledWithLookup(cfg, configured, os.LookupEnv)
}

func ResolveTelemetryEnabledWithLookup(cfg Config, configured bool, lookup func(string) (string, bool)) TelemetryEnabledState {
	if lookup != nil {
		if raw, ok := lookup(TelemetryEnv); ok {
			if value, valid := ParseEnvironmentBool(raw); valid {
				return TelemetryEnabledState{Enabled: value, Source: TelemetrySourceEnv}
			}
		}
	}
	source := TelemetrySourceDefault
	if configured {
		source = TelemetrySourceConfig
	}
	return TelemetryEnabledState{Enabled: cfg.Telemetry.Enabled, Source: source}
}
