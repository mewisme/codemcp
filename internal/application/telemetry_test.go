package application

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

const telemetryTestEndpoint = "https://telemetry.mewis.me/v1/products/codemcp/events"

type telemetryReconcilerRecorder struct {
	values []bool
}

func (recorder *telemetryReconcilerRecorder) SetEnabled(value bool) {
	recorder.values = append(recorder.values, value)
}

func telemetryTestService(t *testing.T, cfg config.Config) *TelemetryService {
	t.Helper()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	store := producttelemetry.NewIdentityStore()
	store.Root = func() string { return root }
	service := NewTelemetryService()
	service.Endpoint = func() string { return telemetryTestEndpoint }
	service.Identity = store
	return service
}

func TestTelemetryStatusAndShowAreSideEffectFreeAndSanitized(t *testing.T) {
	cfg := config.Default()
	service := telemetryTestService(t, cfg)
	identityPath := service.Identity.Path()

	status, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !status.PersistedEnabled || !status.EffectiveEnabled || status.Source != config.TelemetrySourceConfig {
		t.Fatalf("status=%#v", status)
	}
	if !status.EndpointAvailable || status.EndpointHost != "telemetry.mewis.me" || status.Product != "codemcp" || status.IdentityPresent {
		t.Fatalf("endpoint/identity status=%#v", status)
	}
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("status created identity state: %v", err)
	}

	shown, err := service.Show(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if shown != status {
		t.Fatalf("show=%#v status=%#v", shown, status)
	}
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("show created identity state: %v", err)
	}
	data, err := json.Marshal(shown)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, telemetryTestEndpoint) || strings.Contains(text, "anonymous_id") {
		t.Fatalf("status leaked raw endpoint or anonymous identity: %s", text)
	}
}

func TestTelemetryEnableDisablePersistAndReconcileEffectiveState(t *testing.T) {
	cfg := config.Default()
	service := telemetryTestService(t, cfg)
	recorder := &telemetryReconcilerRecorder{}
	service.Client = recorder

	disabled, err := service.Disable(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if disabled.PersistedEnabled || disabled.EffectiveEnabled {
		t.Fatalf("disabled status=%#v", disabled)
	}
	persisted, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Telemetry.Enabled {
		t.Fatal("telemetry disable did not persist false")
	}
	if len(recorder.values) != 1 || recorder.values[0] {
		t.Fatalf("disable reconciliation=%v", recorder.values)
	}

	enabled, err := service.Enable(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.PersistedEnabled || !enabled.EffectiveEnabled {
		t.Fatalf("enabled status=%#v", enabled)
	}
	persisted, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Telemetry.Enabled {
		t.Fatal("telemetry enable did not persist true")
	}
	if len(recorder.values) != 2 || !recorder.values[1] {
		t.Fatalf("enable reconciliation=%v", recorder.values)
	}
}

func TestTelemetryEnvironmentOverrideWinsWithoutMutatingConfig(t *testing.T) {
	cfg := config.Default()
	service := telemetryTestService(t, cfg)
	recorder := &telemetryReconcilerRecorder{}
	service.Client = recorder
	t.Setenv(config.TelemetryEnv, "0")

	status, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !status.PersistedEnabled || status.EffectiveEnabled || status.Source != config.TelemetrySourceEnv || !status.EnvironmentOverride {
		t.Fatalf("override status=%#v", status)
	}
	if _, err := service.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	persisted, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Telemetry.Enabled {
		t.Fatal("environment override mutated persisted telemetry preference")
	}
	if len(recorder.values) != 1 || recorder.values[0] {
		t.Fatalf("effective reconciliation ignored environment override: %v", recorder.values)
	}
}

func TestTelemetryCanonicalOperationsAreBound(t *testing.T) {
	service := telemetryTestService(t, config.Default())
	dispatcher := NewDispatcher()
	if err := BindTelemetryOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	for _, id := range []capability.ID{
		capability.TelemetryStatus,
		capability.TelemetryShow,
		capability.TelemetryDisable,
		capability.TelemetryEnable,
	} {
		result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: id})
		if err != nil || result.Operation != id {
			t.Fatalf("operation %q result=%#v err=%v", id, result, err)
		}
	}
}
