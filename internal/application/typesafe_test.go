package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestTypeSafeStatusMutationAndProbeAreExplicit(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	if err := config.Save(typeSafeTestConfig()); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"name":"jev-latest"}]}`)
	}))
	defer server.Close()

	service := NewTypeSafeService()
	service.HTTPClient = server.Client()
	service.BaseURL = server.URL
	t.Setenv("TYPESAFE_API_KEY", "ignored-environment-secret")

	status, err := service.Status(t.Context())
	if err != nil || status.State != TypeSafeDisabled || status.APIKeyConfigured {
		t.Fatalf("initial status=%#v err=%v", status, err)
	}
	if _, err := service.Probe(t.Context()); err == nil {
		t.Fatal("disabled TypeSafe probe unexpectedly succeeded")
	}
	doctor, err := service.Doctor(t.Context(), false)
	if err != nil || len(doctor.Checks) != 2 || doctor.Probe != nil || calls.Load() != 0 {
		t.Fatalf("local doctor=%#v err=%v calls=%d", doctor, err, calls.Load())
	}
	if calls.Load() != 0 {
		t.Fatalf("disabled TypeSafe reached provider: calls=%d", calls.Load())
	}
	if _, err := service.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status(t.Context())
	if err != nil || status.State != TypeSafeMisconfigured || status.APIKeyConfigured {
		t.Fatalf("environment credential was implicitly consumed: status=%#v err=%v", status, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("enable unexpectedly probed provider: calls=%d", calls.Load())
	}

	settings := NewSettingService()
	const secret = "typesafe-application-secret"
	keyResult, err := settings.Set(t.Context(), "integrations.typesafe.api_key", secret)
	if err != nil {
		t.Fatal(err)
	}
	if keyResult.Value != tracepkg.MaskSecret(secret, true) || keyResult.Configured == nil || !*keyResult.Configured {
		t.Fatalf("secret presentation leaked value: %#v", keyResult)
	}
	if calls.Load() != 0 {
		t.Fatalf("key mutation unexpectedly probed provider: calls=%d", calls.Load())
	}
	status, err = service.Status(t.Context())
	if err != nil || status.State != TypeSafeReady || !status.APIKeyConfigured {
		t.Fatalf("ready status=%#v err=%v", status, err)
	}

	probe, err := service.Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !probe.Provider.ModelAvailable {
		t.Fatalf("probe=%#v calls=%d", probe, calls.Load())
	}
	doctor, err = service.Doctor(t.Context(), true)
	if err != nil || doctor.Probe == nil || !doctor.Probe.Provider.ModelAvailable || calls.Load() != 2 {
		t.Fatalf("probed doctor=%#v err=%v calls=%d", doctor, err, calls.Load())
	}

	data, err := os.ReadFile(config.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("TypeSafe API key leaked into config.json")
	}
}

func TestTypeSafeCanonicalOperationsAreBound(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	if err := config.Save(typeSafeTestConfig()); err != nil {
		t.Fatal(err)
	}
	service := NewTypeSafeService()
	dispatcher := NewDispatcher()
	if err := BindTypeSafeOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: capability.IntegrationTypeSafeStatus})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := result.Value.(TypeSafeStatus)
	if !ok || status.State != TypeSafeDisabled {
		t.Fatalf("status result=%#v", result.Value)
	}
	result, err = dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: capability.IntegrationTypeSafeDoctor, Input: TypeSafeDoctorInput{Probe: false}})
	if err != nil {
		t.Fatal(err)
	}
	if doctor, ok := result.Value.(TypeSafeDoctorResult); !ok || len(doctor.Checks) != 2 || doctor.Probe != nil {
		t.Fatalf("doctor result=%#v", result.Value)
	}
	for _, id := range []capability.ID{capability.IntegrationTypeSafeEnable, capability.IntegrationTypeSafeDisable} {
		result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: id})
		if err != nil || result.Operation != id {
			t.Fatalf("operation %q result=%#v err=%v", id, result, err)
		}
	}
	_, err = dispatcher.Dispatch(context.Background(), DispatchRequest{Operation: capability.IntegrationTypeSafeProbe})
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Code == ErrorUnsupported {
		t.Fatalf("probe binding err=%#v", err)
	}
}

func TestTypeSafeSettingServiceRejectsMixedSecretTransaction(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	if err := config.Save(typeSafeTestConfig()); err != nil {
		t.Fatal(err)
	}
	service := NewSettingService()
	err := service.ValidateApply(t.Context(), []SettingChange{
		{Key: "integrations.typesafe.api_key", Value: "secret"},
		{Key: "integrations.typesafe.model", Value: "jev-1.13.0"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("mixed transaction err=%v", err)
	}
}

func typeSafeTestConfig() config.Config {
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-configured-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-configured-hash"
	return cfg
}
