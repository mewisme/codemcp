package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
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
	for _, want := range []string{"CodeMCP Mini App", "data-codemcp-mini-app-root", "https://telegram.org/js/telegram-web-app.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("logs shell missing %q", want)
		}
	}
	if !strings.Contains(body, `<script type="module"`) || strings.Contains(body, `/mini-app/assets/`) || strings.Contains(body, `src="/assets/`) || strings.Contains(body, `href="/assets/`) {
		t.Fatalf("Mini App shell is not self-contained")
	}
	content, err := io.ReadAll(recorder.Result().Body)
	if err != nil || len(content) == 0 {
		t.Fatalf("embedded Mini App shell empty: err=%v", err)
	}
}

func TestMiniAppContentSecurityPolicyAllowsOnlyTheEmbeddedModuleScript(t *testing.T) {
	policy := miniAppContentSecurityPolicy()
	if !strings.Contains(policy, "script-src 'self' https://telegram.org 'sha256-") {
		t.Fatalf("Mini App CSP does not authorize the embedded module script: %s", policy)
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
