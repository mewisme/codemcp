package event

import (
	"errors"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/logger"
)

func TestLoggerEventRoundTripPreservesRuntimeMetadata(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	metadata := Metadata{RunID: "run_roundtrip", PID: 42, Managed: true, ServiceID: "service_test", ServiceScope: "system"}
	input := logger.Event{
		Time: now, Level: logger.Error, Visibility: logger.VisibilityVerbose, Kind: logger.KindError, Name: "tool.call.failed", Component: "TOOL", Message: "request failed", Err: errors.New("boom"),
		Fields: []logger.Field{
			logger.With("workspace_id", "ws_test"), logger.With("tool", "run_command"), logger.With("method", "POST"), logger.With("source", "tunnel"), logger.With("status", "error"), logger.With("duration_ms", int64(12)),
		},
	}
	event := fromLoggerEvent(input, metadata)
	if event.RunID != metadata.RunID || event.PID != metadata.PID || !event.Managed || event.ServiceID != metadata.ServiceID || event.ServiceScope != metadata.ServiceScope || event.WorkspaceID != "ws_test" || event.Tool != "run_command" || event.Method != "POST" || event.Source != "tunnel" || event.Status != "error" || event.DurationMS != 12 || event.Error != "boom" {
		t.Fatalf("event=%#v", event)
	}
	roundTrip := event.LoggerEvent()
	if roundTrip.Time != now || roundTrip.Level != logger.Error || roundTrip.Visibility != logger.VisibilityVerbose || roundTrip.Kind != logger.KindError || roundTrip.Name != input.Name || roundTrip.Component != input.Component || roundTrip.Message != input.Message || roundTrip.RunID != metadata.RunID || roundTrip.PID != metadata.PID || !roundTrip.Managed || roundTrip.ServiceID != metadata.ServiceID || roundTrip.ServiceScope != metadata.ServiceScope || roundTrip.Err == nil || roundTrip.Err.Error() != "boom" || len(roundTrip.Fields) != len(input.Fields) {
		t.Fatalf("roundTrip=%#v", roundTrip)
	}
}

func TestLoggerEventMapsLevelsKindsAndDurationTypes(t *testing.T) {
	for _, test := range []struct {
		name      string
		level     string
		kind      string
		wantLevel logger.Level
		wantKind  logger.Kind
	}{
		{name: "debug-action", level: "DEBUG", kind: "ACTION", wantLevel: logger.Debug, wantKind: logger.KindAction},
		{name: "warn-success", level: "warn", kind: "success", wantLevel: logger.Warn, wantKind: logger.KindSuccess},
		{name: "info-warning", level: "info", kind: "warning", wantLevel: logger.Info, wantKind: logger.KindWarning},
		{name: "default-info", level: "unknown", kind: "unknown", wantLevel: logger.Info, wantKind: logger.KindInfo},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := (Event{Level: test.level, Kind: test.kind}).LoggerEvent()
			if got.Level != test.wantLevel || got.Kind != test.wantKind {
				t.Fatalf("level=%v kind=%v", got.Level, got.Kind)
			}
		})
	}
	for _, value := range []any{int(10), int64(11), float64(12)} {
		event := fromLoggerEvent(logger.Event{Fields: []logger.Field{logger.With("duration_ms", value)}}, Metadata{})
		if event.DurationMS < 10 || event.DurationMS > 12 {
			t.Fatalf("duration %T=%v mapped to %d", value, value, event.DurationMS)
		}
	}
}
