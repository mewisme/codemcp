package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

const telemetryTestEndpoint = "https://telemetry.example/v1/products/codemcp/events"

type telemetryReconcilerRecorder struct {
	values []bool
}

func TestTelemetryBootstrapEligibilityAndIdentityRecoveryMatrix(t *testing.T) {
	tests := []struct {
		name          string
		enabled       bool
		env           string
		endpoint      string
		seed          string
		wantPresent   bool
		wantCreated   bool
		wantUnchanged bool
	}{
		{name: "disabled", enabled: false, endpoint: telemetryTestEndpoint},
		{name: "env-disabled", enabled: true, env: "0", endpoint: telemetryTestEndpoint},
		{name: "endpoint-less", enabled: true, endpoint: ""},
		{name: "fresh", enabled: true, endpoint: telemetryTestEndpoint, wantPresent: true, wantCreated: true},
		{name: "existing", enabled: true, endpoint: telemetryTestEndpoint, seed: `{"schema":1,"anonymous_id":"123e4567-e89b-42d3-a456-426614174099"}`, wantPresent: true, wantUnchanged: true},
		{name: "corrupt-recovery", enabled: true, endpoint: telemetryTestEndpoint, seed: `{broken`, wantPresent: true, wantCreated: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Telemetry.Enabled = test.enabled
			service := telemetryTestService(t, cfg)
			service.Endpoint = func() string { return test.endpoint }
			service.Identity.NewID = func() (string, error) { return "123e4567-e89b-42d3-a456-426614174100", nil }
			if test.env != "" {
				t.Setenv(config.TelemetryEnv, test.env)
			}
			if test.seed != "" {
				if err := os.MkdirAll(filepath.Dir(service.Identity.Path()), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(service.Identity.Path(), []byte(test.seed), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(service.Identity.Path())
			result, err := service.Bootstrap(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if result.IdentityPresent != test.wantPresent || result.IdentityCreated != test.wantCreated {
				t.Fatalf("result=%#v", result)
			}
			after, readErr := os.ReadFile(service.Identity.Path())
			if !test.wantPresent && !os.IsNotExist(readErr) {
				t.Fatalf("ineligible bootstrap created identity: %v", readErr)
			}
			if test.wantUnchanged && string(before) != string(after) {
				t.Fatalf("existing identity changed: before=%s after=%s", before, after)
			}
		})
	}
}

func TestTelemetryBootstrapDoesNotInstallIntegrationsOrPerformNetwork(t *testing.T) {
	service := telemetryTestService(t, config.Default())
	result, err := service.Bootstrap(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !result.IdentityPresent {
		t.Fatalf("bootstrap result=%#v", result)
	}
}

func TestTelemetryBootstrapOwnerHasNoInstallOrIntegrationDependency(t *testing.T) {
	data, err := os.ReadFile("telemetry.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"internal/install", "internal/integrations", "EnsureAvailable", "InstallCurrent"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("telemetry bootstrap owns forbidden install/integration concern %q", forbidden)
		}
	}
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
	if !status.EndpointAvailable || status.EndpointHost != "telemetry.example" || status.Product != "codemcp" || status.IdentityPresent {
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
