package typesafe

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeUsesModelsEndpointAndBearerAuth(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != ModelsPath {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Fatalf("authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"name":"jev-latest"},{"name":"jev-preview"}]}`)
	}))
	defer server.Close()

	result, err := Probe(t.Context(), "test-secret", "jev-latest", time.Second, ProbeOptions{Client: server.Client(), BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || result.HTTPStatus != 200 || !result.ModelAvailable || len(result.Models) != 2 {
		t.Fatalf("result=%#v calls=%d", result, calls.Load())
	}
}

func TestProbeClassifiesFailuresWithoutLeakingSecretOrBody(t *testing.T) {
	const secret = "probe-secret-sentinel"
	for _, tc := range []struct {
		status int
		want   ProbeErrorCategory
	}{
		{http.StatusUnauthorized, ProbeErrorUnauthorized},
		{http.StatusUnprocessableEntity, ProbeErrorInvalid},
		{http.StatusTooManyRequests, ProbeErrorRateLimited},
		{529, ProbeErrorOverloaded},
		{http.StatusInternalServerError, ProbeErrorHTTP},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, "provider-body-sentinel")
		}))
		_, err := Probe(t.Context(), secret, DefaultModel, time.Second, ProbeOptions{Client: server.Client(), BaseURL: server.URL})
		server.Close()
		probeErr, ok := err.(*ProbeError)
		if !ok || probeErr.Category != tc.want {
			t.Fatalf("status=%d err=%#v", tc.status, err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "provider-body-sentinel") {
			t.Fatalf("probe error leaked sensitive material: %v", err)
		}
	}
}

func TestProbeRejectsUnavailableConfiguredModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"name":"jev-latest"}]}`)
	}))
	defer server.Close()
	result, err := Probe(t.Context(), "secret", "jev-missing", time.Second, ProbeOptions{Client: server.Client(), BaseURL: server.URL})
	probeErr, ok := err.(*ProbeError)
	if !ok || probeErr.Category != ProbeErrorInvalid || result.ModelAvailable {
		t.Fatalf("result=%#v err=%#v", result, err)
	}
}

func TestProbeRejectsCrossHostRedirectBeforeCredentialForwarding(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()
	redirectTarget := strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + ModelsPath
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	_, err := Probe(t.Context(), "redirect-secret", DefaultModel, time.Second, ProbeOptions{
		Client: redirector.Client(), BaseURL: redirector.URL,
	})
	probeErr, ok := err.(*ProbeError)
	if !ok || probeErr.Category != ProbeErrorNetwork {
		t.Fatalf("redirect err=%#v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("cross-host redirect reached target %d time(s)", targetCalls.Load())
	}
}
