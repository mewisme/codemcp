package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testEvent(t *testing.T, feature string) Event {
	t.Helper()
	event, err := NewEvent(EventOperationCompleted, ClientFields{
		AnonymousID: "123e4567-e89b-42d3-a456-426614174000",
		Version:     "0.3.0", OS: "linux", Arch: "amd64",
	}, EventFields{Interface: InterfaceCLI, Command: "status", Feature: feature})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func routedClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := server.Client().Transport
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		clone.Host = target.Host
		return base.RoundTrip(clone)
	})}
}

func TestClientFlushSendsExactAuditedBatchSchema(t *testing.T) {
	var (
		attempts atomic.Int32
		body     []byte
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/v1/products/codemcp/events" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type=%q", request.Header.Get("Content-Type"))
		}
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":2}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{
		Endpoint: testEndpoint, HTTPClient: routedClient(t, server), Enabled: true,
		FlushInterval: time.Hour, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Enqueue(testEvent(t, "status.overview")) || !client.Enqueue(testEvent(t, "workspace.list")) {
		t.Fatal("valid events were not enqueued")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client.Flush(ctx)
	client.Close(ctx)
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d want=1", attempts.Load())
	}
	var batch map[string]any
	if err := json.Unmarshal(body, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 || batch["schema"] != float64(SchemaVersion) {
		t.Fatalf("batch=%s", body)
	}
	events, ok := batch["events"].([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("events=%#v", batch["events"])
	}
	allowed := map[string]bool{
		"name": true, "anonymous_id": true, "version": true, "os": true, "arch": true,
		"interface": true, "command": true, "feature": true, "error_code": true,
		"duration_ms": true, "success": true,
	}
	for _, raw := range events {
		for key := range raw.(map[string]any) {
			if !allowed[key] {
				t.Fatalf("unexpected event field %q in %s", key, body)
			}
		}
	}
}

func TestClientUsesOneAttemptAndDropsFailedBatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		code   DiagnosticCode
	}{
		{name: "4xx", status: http.StatusBadRequest, code: DiagnosticRejected},
		{name: "5xx", status: http.StatusServiceUnavailable, code: DiagnosticRejected},
		{name: "invalid accepted response", status: http.StatusAccepted, body: `{"accepted":0}`, code: DiagnosticInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts atomic.Int32
			diagnostics := make(chan Diagnostic, 4)
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				attempts.Add(1)
				writer.WriteHeader(test.status)
				if test.body != "" {
					_, _ = writer.Write([]byte(test.body))
				}
			}))
			defer server.Close()
			client, err := NewClient(ClientOptions{
				Endpoint: testEndpoint, HTTPClient: routedClient(t, server), Enabled: true,
				FlushInterval: time.Hour, RequestTimeout: time.Second,
				Observe: func(value Diagnostic) { diagnostics <- value },
			})
			if err != nil {
				t.Fatal(err)
			}
			if !client.Enqueue(testEvent(t, "status.overview")) {
				t.Fatal("enqueue failed")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			client.Flush(ctx)
			client.Close(ctx)
			if attempts.Load() != 1 || client.Pending() != 0 {
				t.Fatalf("attempts=%d pending=%d", attempts.Load(), client.Pending())
			}
			select {
			case got := <-diagnostics:
				if got.Code != test.code {
					t.Fatalf("diagnostic=%#v want=%q", got, test.code)
				}
			default:
				t.Fatalf("missing diagnostic %q", test.code)
			}
		})
	}
}

func TestClientTimeoutAndTransportFailureAreSanitizedAndNotRetried(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport http.RoundTripper
		code      DiagnosticCode
		timeout   time.Duration
	}{
		{
			name: "transport",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("private transport detail /home/user/token")
			}),
			code: DiagnosticTransport, timeout: time.Second,
		},
		{
			name: "timeout",
			transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
			code: DiagnosticTimeout, timeout: 20 * time.Millisecond,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts atomic.Int32
			diagnostics := make(chan Diagnostic, 2)
			transport := test.transport
			httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts.Add(1)
				return transport.RoundTrip(request)
			})}
			client, err := NewClient(ClientOptions{
				Endpoint: testEndpoint, HTTPClient: httpClient, Enabled: true,
				FlushInterval: time.Hour, RequestTimeout: test.timeout,
				Observe: func(value Diagnostic) { diagnostics <- value },
			})
			if err != nil {
				t.Fatal(err)
			}
			if !client.Enqueue(testEvent(t, "status.overview")) {
				t.Fatal("enqueue failed")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			client.Flush(ctx)
			client.Close(ctx)
			if attempts.Load() != 1 {
				t.Fatalf("attempts=%d", attempts.Load())
			}
			got := <-diagnostics
			if got.Code != test.code || got.EventCount != 1 || got.HTTPStatus != 0 {
				t.Fatalf("diagnostic=%#v", got)
			}
		})
	}
}

func TestClientQueueBoundsDisableAndEndpointlessLifecycle(t *testing.T) {
	var diagnosticsMu sync.Mutex
	var diagnostics []Diagnostic
	client, err := NewClient(ClientOptions{
		Endpoint: testEndpoint,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return nil, errors.New("network should not be reached in queue bound test")
		})},
		Enabled: true, QueueMaxEvents: 1, QueueMaxBytes: 16 * 1024,
		FlushInterval: time.Hour,
		Observe: func(value Diagnostic) {
			diagnosticsMu.Lock()
			diagnostics = append(diagnostics, value)
			diagnosticsMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Enqueue(testEvent(t, "status.overview")) {
		t.Fatal("first enqueue failed")
	}
	if client.Enqueue(testEvent(t, "workspace.list")) {
		t.Fatal("queue overflow was accepted")
	}
	byteBounded, err := NewClient(ClientOptions{
		Endpoint: testEndpoint,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return nil, errors.New("network should not be reached in byte bound test")
		})},
		Enabled: true, QueueMaxEvents: 10, QueueMaxBytes: 1, FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if byteBounded.Enqueue(testEvent(t, "status.overview")) || byteBounded.Pending() != 0 {
		t.Fatal("queue byte budget accepted an oversized queued event")
	}
	byteBounded.Close(t.Context())
	client.SetEnabled(false)
	if client.Pending() != 0 || client.Enqueue(testEvent(t, "status.overview")) {
		t.Fatalf("disable pending=%d", client.Pending())
	}
	diagnosticsMu.Lock()
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticQueueOverflow {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	diagnosticsMu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	client.Close(ctx)

	endpointless, err := NewClient(ClientOptions{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if endpointless.Active() || endpointless.done != nil || endpointless.wake != nil || endpointless.requests != nil {
		t.Fatalf("endpoint-less client allocated sender lifecycle: %#v", endpointless)
	}
	if endpointless.Enqueue(testEvent(t, "status.overview")) || endpointless.Pending() != 0 {
		t.Fatal("endpoint-less client accepted telemetry")
	}
	endpointless.Close(ctx)
}

func TestClientSplitsSerializedBatchesAtBackendBodyLimit(t *testing.T) {
	var (
		attempts atomic.Int32
		accepted atomic.Int32
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > MaxBodyBytes {
			t.Fatalf("request body=%d exceeds max=%d", len(body), MaxBodyBytes)
		}
		var batch Batch
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Fatal(err)
		}
		accepted.Add(int32(len(batch.Events)))
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":` + jsonNumber(len(batch.Events)) + `}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{
		Endpoint: testEndpoint, HTTPClient: routedClient(t, server), Enabled: true,
		QueueMaxEvents: 200, QueueMaxBytes: 512 * 1024, FlushInterval: time.Hour, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	large := ClientFields{
		AnonymousID: "123e4567-e89b-42d3-a456-426614174000",
		Version:     strings.Repeat("v", MaxStringLength),
		OS:          strings.Repeat("o", MaxStringLength),
		Arch:        strings.Repeat("a", MaxStringLength),
	}
	for range 100 {
		event, err := NewEvent(EventOperationCompleted, large, EventFields{
			Interface: InterfaceCLI,
			Command:   strings.Repeat("c", MaxStringLength),
			Feature:   strings.Repeat("f", MaxStringLength),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !client.Enqueue(event) {
			t.Fatal("large valid event was not enqueued")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client.Flush(ctx)
	client.Close(ctx)
	if accepted.Load() != 100 {
		t.Fatalf("accepted=%d want=100", accepted.Load())
	}
	if attempts.Load() < 2 {
		t.Fatalf("large serialized batch was not split: attempts=%d", attempts.Load())
	}
}

func jsonNumber(value int) string {
	return fmt.Sprintf("%d", value)
}

func TestClientPeriodicFlushAndCloseAreBounded(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":1}`))
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{
		Endpoint: testEndpoint, HTTPClient: routedClient(t, server), Enabled: true,
		FlushInterval: 10 * time.Millisecond, RequestTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Enqueue(testEvent(t, "status.overview")) {
		t.Fatal("enqueue failed")
	}
	deadline := time.Now().Add(time.Second)
	for attempts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if attempts.Load() != 1 {
		t.Fatalf("periodic attempts=%d", attempts.Load())
	}

	if !client.Enqueue(testEvent(t, "workspace.list")) {
		t.Fatal("second enqueue failed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client.Close(ctx)
	if attempts.Load() != 2 || client.Pending() != 0 {
		t.Fatalf("close attempts=%d pending=%d", attempts.Load(), client.Pending())
	}
	select {
	case <-client.done:
	default:
		t.Fatal("close returned before worker disposal")
	}
}

func TestClientCloseDeadlineCancelsInflightSendAndDisposesWorker(t *testing.T) {
	started := make(chan struct{}, 1)
	client, err := NewClient(ClientOptions{
		Endpoint: testEndpoint,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
		Enabled: true, FlushInterval: time.Hour, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range MaxBatchEvents {
		if !client.Enqueue(testEvent(t, "status.overview")) {
			t.Fatal("enqueue failed")
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("sender did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	client.Close(ctx)
	if elapsed := time.Since(startedAt); elapsed > 200*time.Millisecond {
		t.Fatalf("close exceeded bounded deadline: %s", elapsed)
	}
	select {
	case <-client.done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker did not dispose after bounded close cancellation")
	}
}

func TestClientCreatesNoPersistentSpool(t *testing.T) {
	root := t.TempDir()
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientOptions{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	client.Enqueue(testEvent(t, "status.overview"))
	client.Close(t.Context())
	after, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("endpoint-less client created persistent state: before=%d after=%d", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(root, "spool")); !os.IsNotExist(err) {
		t.Fatalf("persistent spool exists: %v", err)
	}
}
