package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestMiniAppWebHandlerServesDedicatedShellAndEmbeddedAssets(t *testing.T) {
	handler := miniAppWebHandler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mini-app", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("logs shell status=%d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{"CodeMCP Mini App", "data-codemcp-mini-app-root", "Loading CodeMCP Mini App"} {
		if !strings.Contains(body, want) {
			t.Fatalf("logs shell missing %q", want)
		}
	}
	if strings.Contains(body, "https://telegram.org/js/telegram-web-app.js") {
		t.Fatal("Mini App shell still parser-blocks on the Telegram SDK")
	}
	if !strings.Contains(body, "Loading CodeMCP Mini App") {
		t.Fatalf("Mini App shell has no pre-React loading fallback")
	}
	content, err := io.ReadAll(recorder.Result().Body)
	if err != nil || len(content) == 0 {
		t.Fatalf("embedded Mini App shell empty: err=%v", err)
	}
	match := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("Mini App shell does not reference an external Vite asset: %s", body)
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, match[1], nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("embedded Mini App asset %s status=%d", match[1], asset.Code)
	}
}

func TestMiniAppContentSecurityPolicyKeepsScriptsExternal(t *testing.T) {
	policy := miniAppContentSecurityPolicy()
	if !strings.Contains(policy, "script-src 'self' https://telegram.org") {
		t.Fatalf("Mini App CSP does not authorize same-origin and Telegram scripts: %s", policy)
	}
	if strings.Contains(policy, "script-src 'self' https://telegram.org 'unsafe-inline'") {
		t.Fatalf("Mini App CSP unexpectedly permits arbitrary inline scripts: %s", policy)
	}
}

func TestMiniAppWebHandlerDoesNotBecomeAnAdminOrAPIFallback(t *testing.T) {
	handler := miniAppWebHandler()
	for _, target := range []string{"/mini-app/api/status", "/mini-app/auth", "/api/status", "/admin"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d want=404", target, recorder.Code)
		}
	}
}
