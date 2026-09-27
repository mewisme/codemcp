package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/secretstore"
)

func TestTypeSafeAdminStatusDoctorProbeAndConfigParity(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()

	cfg := config.Default()
	cfg.Auth.MCPTokenHash = "mcp-hash"
	cfg.Auth.AdminTokenHash = "admin-hash"
	cfg.Integrations.TypeSafe.Enabled = true
	if err := typesafeintegration.UpdateAPIKey(root, "admin-typesafe-secret"); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("provider request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"name":"jev-latest"}]}`)
	}))
	defer provider.Close()

	service := application.NewTypeSafeService()
	service.Root = func() string { return root }
	service.HTTPClient = provider.Client()
	service.BaseURL = provider.URL
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Config: store, TypeSafe: service, saveConfig: func(config.Config) error { return nil }})

	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/api/integrations/typesafe", nil))
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", statusRecorder.Code, statusRecorder.Body.String())
	}
	if got := statusRecorder.Header().Get(CanonicalOperationHeader); got != string(capability.IntegrationTypeSafeStatus) {
		t.Fatalf("status operation=%q", got)
	}
	var status application.TypeSafeStatus
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.State != application.TypeSafeReady || !status.APIKeyConfigured || calls.Load() != 0 {
		t.Fatalf("status=%#v calls=%d", status, calls.Load())
	}
	if strings.Contains(statusRecorder.Body.String(), "admin-typesafe-secret") {
		t.Fatal("status response leaked TypeSafe API key")
	}

	doctorRecorder := httptest.NewRecorder()
	handler.ServeHTTP(doctorRecorder, httptest.NewRequest(http.MethodGet, "/api/integrations/typesafe/doctor", nil))
	if doctorRecorder.Code != http.StatusOK || calls.Load() != 0 {
		t.Fatalf("local doctor code=%d calls=%d body=%s", doctorRecorder.Code, calls.Load(), doctorRecorder.Body.String())
	}
	if got := doctorRecorder.Header().Get(CanonicalOperationHeader); got != string(capability.IntegrationTypeSafeDoctor) {
		t.Fatalf("doctor operation=%q", got)
	}
	var doctor application.TypeSafeDoctorResult
	if err := json.Unmarshal(doctorRecorder.Body.Bytes(), &doctor); err != nil {
		t.Fatal(err)
	}
	if doctor.Probe != nil || len(doctor.Checks) != 2 {
		t.Fatalf("local doctor=%#v", doctor)
	}

	probedDoctorRecorder := httptest.NewRecorder()
	handler.ServeHTTP(probedDoctorRecorder, httptest.NewRequest(http.MethodGet, "/api/integrations/typesafe/doctor?probe=true", nil))
	if probedDoctorRecorder.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("probed doctor code=%d calls=%d body=%s", probedDoctorRecorder.Code, calls.Load(), probedDoctorRecorder.Body.String())
	}

	probeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(probeRecorder, httptest.NewRequest(http.MethodPost, "/api/integrations/typesafe/probe", nil))
	if probeRecorder.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("probe code=%d calls=%d body=%s", probeRecorder.Code, calls.Load(), probeRecorder.Body.String())
	}
	if got := probeRecorder.Header().Get(CanonicalOperationHeader); got != string(capability.IntegrationTypeSafeProbe) {
		t.Fatalf("probe operation=%q", got)
	}

	patch := `{"integrations":{"typesafe":{"enabled":false,"model":"jev-custom","timeout_ms":1200}}}`
	configRecorder := httptest.NewRecorder()
	handler.ServeHTTP(configRecorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(patch)))
	if configRecorder.Code != http.StatusOK {
		t.Fatalf("config patch code=%d body=%s", configRecorder.Code, configRecorder.Body.String())
	}
	next := store.Snapshot()
	if next.Integrations.TypeSafe.Enabled || next.Integrations.TypeSafe.Model != "jev-custom" || next.Integrations.TypeSafe.TimeoutMS != 1200 {
		t.Fatalf("patched TypeSafe config=%#v", next.Integrations.TypeSafe)
	}
	if strings.Contains(configRecorder.Body.String(), "admin-typesafe-secret") {
		t.Fatal("config response leaked TypeSafe API key")
	}
}
