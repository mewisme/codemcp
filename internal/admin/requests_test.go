package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/controlguard"
)

func TestApprovalAPIListDetailApproveAndDeny(t *testing.T) {
	manager := approval.NewManager("instance-test")
	first := seedAdminApprovalRequest(t, manager, "session-a", "ws_a", "cgm update")
	second := seedAdminApprovalRequest(t, manager, "session-b", "ws_b", "cgm install")
	handler := New(API{Approvals: manager, Config: config.NewRuntimeStore(config.Default())})

	list := localAdminRequest(http.MethodGet, "/api/requests?status=pending&workspace_id=ws_a", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, list)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var requests []approval.Request
	if err := json.Unmarshal(recorder.Body.Bytes(), &requests); err != nil || len(requests) != 1 || requests[0].ID != first.ID {
		t.Fatalf("list=%#v err=%v", requests, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, localAdminRequest(http.MethodGet, "/api/requests/"+first.ID[:8], nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), first.ID) || !strings.Contains(recorder.Body.String(), `"command":"cgm update"`) {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, localAdminRequest(http.MethodPost, "/api/requests/"+first.ID+"/approve", strings.NewReader(`{"reason":"reviewed"}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"approved"`) || !strings.Contains(recorder.Body.String(), `"resolved_by":"admin"`) {
		t.Fatalf("approve status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, localAdminRequest(http.MethodPost, "/api/requests/"+second.ID+"/deny", strings.NewReader(`{"reason":"not now"}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"denied"`) {
		t.Fatalf("deny status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestApprovalAPIRemoteRequiresEnabledAdminAuthentication(t *testing.T) {
	manager := approval.NewManager("instance-test")
	seedAdminApprovalRequest(t, manager, "session-a", "ws_a", "cgm update")
	cfg := config.Default()
	cfg.Auth.AdminEnabled = false
	cfg.Auth.AdminTokenHash = auth.HashToken("admin-test")
	store := config.NewRuntimeStore(cfg)
	handler := New(API{Approvals: manager, Config: store})

	request := httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	request.RemoteAddr = "192.0.2.10:51234"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("auth-disabled remote status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if _, err := store.Update(func(next config.Config) (config.Config, error) {
		next.Auth.AdminEnabled = true
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	request.RemoteAddr = "192.0.2.10:51234"
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing-token remote status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	request.RemoteAddr = "192.0.2.10:51234"
	request.Header.Set("Authorization", "Bearer admin-test")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated remote status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestApprovalAPIIgnoresForwardedLoopbackAddress(t *testing.T) {
	manager := approval.NewManager("instance-test")
	seedAdminApprovalRequest(t, manager, "session-a", "ws_a", "cgm update")
	cfg := config.Default()
	cfg.Auth.AdminEnabled = false
	handler := New(API{Approvals: manager, Config: config.NewRuntimeStore(cfg)})
	request := httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	request.RemoteAddr = "192.0.2.10:51234"
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("forwarded loopback bypassed policy: status=%d", recorder.Code)
	}
}

func TestApprovalSSEPublishesLifecycleWithoutArguments(t *testing.T) {
	manager := approval.NewManager("instance-test")
	server := httptest.NewServer(New(API{Approvals: manager, Config: config.NewRuntimeStore(config.Default())}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/requests/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanUntil(scanner, "event: ready", time.Second) {
		t.Fatal("approval SSE missing ready event")
	}
	created := seedAdminApprovalRequest(t, manager, "session-a", "ws_a", "cgm update --version v2")
	line := scanEventData(t, scanner, approval.EventRequested)
	if !strings.Contains(line, created.ID) || strings.Contains(line, "cgm update") || strings.Contains(line, "arguments") {
		t.Fatalf("unsafe approval SSE data=%q", line)
	}
}

func TestApprovalSSESubscribesBeforeReadyFlush(t *testing.T) {
	manager := approval.NewManager("instance-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/requests/stream", nil).WithContext(ctx)
	writer := &approvalFlushWriter{header: make(http.Header)}
	writer.flush = func() {
		if writer.flushed {
			return
		}
		writer.flushed = true
		seedAdminApprovalRequest(t, manager, "session-a", "ws_a", "cgm update --version v2")
	}
	done := make(chan struct{})
	go func() {
		serveApprovalEvents(writer, request, manager.Events(), time.Hour)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(writer.String(), "event: "+approval.EventRequested) {
			cancel()
			<-done
			return
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("approval event published during ready flush was lost: %q", writer.String())
}

func TestApprovalSSEFiltersWorkspaceBeforeSubscriberBuffer(t *testing.T) {
	manager := approval.NewManager("instance-test")
	server := httptest.NewServer(New(API{Approvals: manager}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/requests/stream?workspace_id=ws_target", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanUntil(scanner, "event: ready", time.Second) {
		t.Fatal("approval SSE missing ready event")
	}
	for index := 0; index < 40; index++ {
		manager.Events().Publish(approval.Event{Name: approval.EventRequested, RequestID: fmt.Sprintf("req_other_%d", index), WorkspaceID: "ws_other", TargetTool: "run_command", Title: "Other", Status: approval.StatusPending})
	}
	targetID := "req_target"
	manager.Events().Publish(approval.Event{Name: approval.EventRequested, RequestID: targetID, WorkspaceID: "ws_target", TargetTool: "run_command", Title: "Target", Status: approval.StatusPending})
	event := scanEventData(t, scanner, approval.EventRequested)
	if !strings.Contains(event, targetID) || strings.Contains(event, "ws_other") {
		t.Fatalf("filtered event = %q", event)
	}
}

type approvalFlushWriter struct {
	mu      sync.Mutex
	header  http.Header
	body    strings.Builder
	flush   func()
	flushed bool
}

func (w *approvalFlushWriter) Header() http.Header { return w.header }
func (w *approvalFlushWriter) WriteHeader(int)     {}
func (w *approvalFlushWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(data)
}
func (w *approvalFlushWriter) Flush() {
	if w.flush != nil {
		w.flush()
	}
}
func (w *approvalFlushWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func localAdminRequest(method, target string, body *strings.Reader) *http.Request {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	request.RemoteAddr = "127.0.0.1:43123"
	return request
}

func seedAdminApprovalRequest(t *testing.T, manager *approval.Manager, sessionID, workspaceID, command string) approval.Request {
	t.Helper()
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{SessionID: sessionID, SessionHash: "hash-" + sessionID, WorkspaceID: workspaceID, Source: "tunnel", TargetTool: "run_command", Arguments: map[string]any{"workspace_id": workspaceID, "command": command}, GuardCode: controlguard.CodeControlPlaneMutation, GuardReason: "control-plane mutation denied", Title: "Allow controlled command"})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, sessionID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func scanUntil(scanner *bufio.Scanner, expected string, timeout time.Duration) bool {
	done := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			if scanner.Text() == expected {
				done <- true
				return
			}
		}
		done <- false
	}()
	select {
	case value := <-done:
		return value
	case <-time.After(timeout):
		return false
	}
}

func scanEventData(t *testing.T, scanner *bufio.Scanner, eventName string) string {
	t.Helper()
	deadline := time.After(time.Second)
	done := make(chan string, 1)
	go func() {
		matched := false
		for scanner.Scan() {
			line := scanner.Text()
			if line == "event: "+eventName {
				matched = true
				continue
			}
			if matched && strings.HasPrefix(line, "data: ") {
				done <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
		done <- ""
	}()
	select {
	case value := <-done:
		return value
	case <-deadline:
		t.Fatal("timed out waiting for approval SSE data")
		return ""
	}
}
