package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestLogsWebHandlerServesDedicatedShellAndEmbeddedAssets(t *testing.T) {
	handler := logsWebHandler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/logs", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("logs shell status=%d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{"CodeMCP Logs", "data-codemcp-logs-root", "https://telegram.org/js/telegram-web-app.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("logs shell missing %q", want)
		}
	}
	match := regexp.MustCompile(`(?:src|href)="(/logs/assets/[^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("logs shell does not reference an embedded Vite asset: %s", body)
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, match[1], nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("embedded asset %s status=%d", match[1], asset.Code)
	}
	content, err := io.ReadAll(asset.Result().Body)
	if err != nil || len(content) == 0 {
		t.Fatalf("embedded asset %s empty: err=%v", match[1], err)
	}
}

func TestLogsWebHandlerDoesNotBecomeAnAdminOrLogsAPIFallback(t *testing.T) {
	handler := logsWebHandler()
	for _, target := range []string{"/logs/api/status", "/logs/auth", "/api/status", "/admin"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d want=404", target, recorder.Code)
		}
	}
}
