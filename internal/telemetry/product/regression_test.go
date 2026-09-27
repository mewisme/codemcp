package product

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecorderEndToEndFakeIngest(t *testing.T) {
	var attempts atomic.Int32
	var body []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":1}`))
	}))
	defer server.Close()

	root := t.TempDir()
	store := NewIdentityStore()
	store.Root = func() string { return root }
	store.NewID = func() (string, error) { return "123e4567-e89b-42d3-a456-426614174111", nil }

	client, err := NewClient(ClientOptions{
		Endpoint: testEndpoint, HTTPClient: routedClient(t, server), Enabled: true,
		FlushInterval: time.Hour, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := NewRecorder(RecorderOptions{
		Enabled: true, Endpoint: testEndpoint, Identity: store, Client: client,
		Version: "1.2.3", OS: "linux", Arch: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !recorder.Record(t.Context(), EventOperationCompleted, Usage{
		Interface: InterfaceCLI, Command: "status", Feature: "status.overview", Success: true,
	}) {
		t.Fatal("recorder rejected safe event")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	recorder.Flush(ctx)
	recorder.Close(ctx)

	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d want=1", attempts.Load())
	}
	var batch Batch
	if err := json.Unmarshal(body, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Events) != 1 || batch.Events[0].AnonymousID != "123e4567-e89b-42d3-a456-426614174111" ||
		batch.Events[0].Feature != "status.overview" || batch.Events[0].Interface != InterfaceCLI {
		t.Fatalf("batch=%s", body)
	}
}

func TestClientConcurrentDisableFlushCloseIsBounded(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":1}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{
		Endpoint: testEndpoint, HTTPClient: routedClient(t, server), Enabled: true,
		FlushInterval: time.Hour, RequestTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Enqueue(testEvent(t, "status.overview")) {
		t.Fatal("enqueue failed")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); client.SetEnabled(false) }()
	go func() { defer wg.Done(); client.Flush(ctx) }()
	go func() { defer wg.Done(); client.Close(ctx) }()
	wg.Wait()

	if client.Pending() != 0 {
		t.Fatalf("pending=%d want=0", client.Pending())
	}
	client.Close(ctx)
}
