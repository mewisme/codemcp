package app

import (
	"context"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	"go.mewis.me/codemcp/internal/tools"
)

type fakeRuntimeProductTelemetry struct {
	values  []capturedRuntimeProductEvent
	enabled []bool
	closed  int
}

type capturedRuntimeProductEvent struct {
	name  producttelemetry.EventName
	usage producttelemetry.Usage
}

func (fake *fakeRuntimeProductTelemetry) Record(_ context.Context, name producttelemetry.EventName, usage producttelemetry.Usage) bool {
	fake.values = append(fake.values, capturedRuntimeProductEvent{name: name, usage: usage})
	return true
}

func (fake *fakeRuntimeProductTelemetry) SetEnabled(value bool) {
	fake.enabled = append(fake.enabled, value)
}

func (fake *fakeRuntimeProductTelemetry) Close(context.Context) {
	fake.closed++
}

func TestProductToolObserverUsesOnlyCanonicalSafeMetadata(t *testing.T) {
	fake := &fakeRuntimeProductTelemetry{}
	observer := productToolObserver(fake)
	if observer == nil {
		t.Fatal("missing product tool observer")
	}
	observer(tools.CallObservation{
		Phase: "start", Source: "http", Tool: "workspace_list",
		Raw: map[string]any{"arguments": map[string]any{"path": "/home/private"}},
	})
	observer(tools.CallObservation{
		Phase: "finish", Source: "http", Tool: "workspace_list", Status: "ok", DurationMS: 17,
		Message: "SECRET_RESULT", WorkspaceID: "ws_secret", SessionHash: "session-secret",
		Raw: map[string]any{"result": "SECRET_RESULT", "arguments": "SECRET_ARGUMENT"},
	})
	if len(fake.values) != 1 {
		t.Fatalf("events=%d want=1", len(fake.values))
	}
	got := fake.values[0]
	if got.name != producttelemetry.EventOperationCompleted ||
		got.usage.Interface != producttelemetry.InterfaceMCP ||
		got.usage.Command != string(capability.WorkspaceList) ||
		got.usage.Feature != string(capability.WorkspaceList) ||
		!got.usage.Success || got.usage.Duration != 17*time.Millisecond {
		t.Fatalf("usage=%#v", got)
	}
}

func TestProductToolObserverIgnoresNonMCPAndUnmappedCalls(t *testing.T) {
	fake := &fakeRuntimeProductTelemetry{}
	observer := productToolObserver(fake)
	for _, observation := range []tools.CallObservation{
		{Phase: "finish", Source: "cli", Tool: "workspace_list", Status: "ok"},
		{Phase: "finish", Source: "http", Tool: "unknown_tool", Status: "ok"},
		{Phase: "start", Source: "http", Tool: "workspace_list"},
	} {
		observer(observation)
	}
	if len(fake.values) != 0 {
		t.Fatalf("unexpected events=%#v", fake.values)
	}
}

func TestProductToolObserverMapsCancelledAndErrorWithoutMessage(t *testing.T) {
	fake := &fakeRuntimeProductTelemetry{}
	observer := productToolObserver(fake)
	observer(tools.CallObservation{
		Phase: "finish", Source: "tunnel", Tool: "workspace_list", Status: "cancelled",
		Message: "private cancellation detail", DurationMS: -1,
	})
	observer(tools.CallObservation{
		Phase: "finish", Source: "stdio", Tool: "workspace_list", Status: "error",
		Message: "private error detail", Raw: map[string]any{"error": "private body"},
	})
	if len(fake.values) != 2 {
		t.Fatalf("events=%d", len(fake.values))
	}
	if fake.values[0].usage.ErrorCode != producttelemetry.ErrorCancelled ||
		fake.values[0].usage.Duration != 0 ||
		fake.values[1].usage.ErrorCode != producttelemetry.ErrorInternal {
		t.Fatalf("events=%#v", fake.values)
	}
}

func TestRuntimeLifecycleRecordsStartStopAndClosesTelemetry(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	cfg := config.Default()
	cfg.Tunnel.Enabled = false
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeRuntimeProductTelemetry{}
	application.ProductTelemetry = fake
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if len(fake.values) != 2 {
		t.Fatalf("runtime events=%#v", fake.values)
	}
	if fake.values[0].name != producttelemetry.EventRuntimeStarted || !fake.values[0].usage.Success ||
		fake.values[0].usage.Interface != producttelemetry.InterfaceRuntime {
		t.Fatalf("start event=%#v", fake.values[0])
	}
	if fake.values[1].name != producttelemetry.EventRuntimeStopped || !fake.values[1].usage.Success ||
		fake.values[1].usage.Interface != producttelemetry.InterfaceRuntime {
		t.Fatalf("stop event=%#v", fake.values[1])
	}
	if fake.closed != 1 {
		t.Fatalf("telemetry close calls=%d", fake.closed)
	}
}
