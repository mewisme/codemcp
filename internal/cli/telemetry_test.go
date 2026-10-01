package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
)

func prepareTelemetryCLIConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return root
}

func executeTelemetryCLI(t *testing.T, args ...string) (string, string) {
	t.Helper()
	root := newRootCommand()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--config-dir", config.RootPath()}, args...))
	if err := executeCommand(root); err != nil {
		t.Fatalf("execute %v: %v stderr=%s", args, err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestTelemetryCLIJSONStatusAndShowAreSanitized(t *testing.T) {
	prepareTelemetryCLIConfig(t)

	for _, command := range []string{"status", "show"} {
		t.Run(command, func(t *testing.T) {
			stdout, _ := executeTelemetryCLI(t, "telemetry", command, "--json")
			var status application.TelemetryStatus
			if err := json.Unmarshal([]byte(stdout), &status); err != nil {
				t.Fatalf("json=%q err=%v", stdout, err)
			}
			if !status.PersistedEnabled || !status.EffectiveEnabled {
				t.Fatalf("status=%#v", status)
			}
			if strings.Contains(stdout, "anonymous_id") || strings.Contains(stdout, "/v1/products/") {
				t.Fatalf("telemetry output leaked raw identity/endpoint: %q", stdout)
			}
		})
	}
}

func TestTelemetryCLIDisablePersistsFalse(t *testing.T) {
	prepareTelemetryCLIConfig(t)

	stdout, _ := executeTelemetryCLI(t, "telemetry", "disable", "--json")
	var status application.TelemetryStatus
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if status.PersistedEnabled || status.EffectiveEnabled {
		t.Fatalf("disable status=%#v", status)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Enabled {
		t.Fatal("telemetry disable did not persist false")
	}
}

func TestTelemetryCLIEnvironmentOverrideIsVisibleAndNonPersistent(t *testing.T) {
	prepareTelemetryCLIConfig(t)
	t.Setenv(config.TelemetryEnv, "0")

	stdout, _ := executeTelemetryCLI(t, "telemetry", "enable", "--json")
	var status application.TelemetryStatus
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if !status.PersistedEnabled || status.EffectiveEnabled || status.Source != config.TelemetrySourceEnv || !status.EnvironmentOverride {
		t.Fatalf("override status=%#v", status)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Telemetry.Enabled {
		t.Fatal("CM_TELEMETRY override mutated persisted config")
	}
}

func TestTelemetryCommandsAreSelfObservationSuppressed(t *testing.T) {
	root := newRootCommand()
	for _, path := range []string{
		"telemetry status", "telemetry enable", "telemetry disable", "telemetry show",
	} {
		command := commandByRelativePath(root, path)
		if command == nil || !productTelemetrySuppressed(command) {
			t.Fatalf("%q is not product telemetry suppressed", path)
		}
	}
}

func TestTelemetryHumanOutputShowsPersistedEffectiveAndSource(t *testing.T) {
	prepareTelemetryCLIConfig(t)
	t.Setenv(config.TelemetryEnv, "0")
	stdout, _ := executeTelemetryCLI(t, "telemetry", "status")
	for _, want := range []string{"persisted", "effective", "source", "CM_TELEMETRY"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("human/plain output missing %q: %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "anonymous_id") {
		t.Fatalf("human output leaked raw identity field: %q", stdout)
	}
	if _, err := os.Stat(config.RootPath() + "/state/product-telemetry.json"); !os.IsNotExist(err) {
		t.Fatalf("status created telemetry identity: %v", err)
	}
}
