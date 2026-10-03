package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevToolsHTTPEndpointFromBrowserWebSocket(t *testing.T) {
	for input, want := range map[string]string{
		"ws://127.0.0.1:9222/devtools/browser/abc":   "http://127.0.0.1:9222",
		"wss://browser.example/devtools/browser/abc": "https://browser.example",
	} {
		got, err := devToolsHTTPEndpoint(input)
		if err != nil || got != want {
			t.Fatalf("endpoint(%q)=%q err=%v want=%q", input, got, err, want)
		}
	}
	if _, err := devToolsHTTPEndpoint("http://127.0.0.1:9222"); err == nil {
		t.Fatal("non-WebSocket browser endpoint was accepted")
	}
}

func TestExistingPageTargetPrefersLaunchBootstrapBlankPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"id":"chatgpt-old","type":"page","url":"https://chatgpt.com/"},
			{"id":"bootstrap","type":"page","url":"about:blank"},
			{"id":"worker","type":"service_worker","url":"https://chatgpt.com/sw.js"}
		]`))
	}))
	defer server.Close()

	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"
	id, err := existingPageTargetID(context.Background(), websocketURL)
	if err != nil {
		t.Fatal(err)
	}
	if id != "bootstrap" {
		t.Fatalf("target id=%q want bootstrap", id)
	}
}
