package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSPAHandlerServesIndexWithoutRedirect(t *testing.T) {
	handler := Handler()
	for _, target := range []string{"/", "/index.html", "/overview", "/workspaces", "/tools", "/servers", "/tunnel", "/activity", "/settings"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", target, recorder.Code)
		}
		if location := recorder.Header().Get("Location"); location != "" {
			t.Fatalf("%s: unexpected redirect to %s", target, location)
		}
		if recorder.Header().Get("Content-Security-Policy") == "" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: security headers missing: %#v", target, recorder.Header())
		}
	}
}
