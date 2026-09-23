package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/tools"
)

func TestHTTPRuntimeCancellationPropagatesAndSuppressesResponse(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan error, 1)
	registry := tools.NewRegistry()
	registry.MustRegister("cancel_probe", tools.Schema{
		Name:        "cancel_probe",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		close(started)
		<-ctx.Done()
		canceled <- ctx.Err()
		return tools.Result{}, ctx.Err()
	})
	runtime := NewHTTPRuntimeWithTools(&tools.Runtime{Registry: registry})

	ctx, cancel := context.WithCancel(context.Background())
	req := modernRequest("tools/call", `{"jsonrpc":"2.0","id":50,"method":"tools/call","params":{"name":"cancel_probe","arguments":{}}}`)
	req.Header.Set(NameHeader, "cancel_probe")
	req = req.WithContext(ctx)
	res := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		runtime.ServeHTTP(res, req)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tool handler did not start")
	}
	cancel()

	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler context error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool handler did not observe request cancellation")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HTTP runtime did not return after cancellation")
	}
	if res.Body.Len() != 0 {
		t.Fatalf("server emitted a post-cancellation response: %s", res.Body.String())
	}
}
