package telegram

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	runtimeactivity "go.mewis.me/codemcp/internal/runtime/activity"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

func TestMiniAppStreamRequiresSessionAndValidFeed(t *testing.T) {
	now := time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC)
	runtime := newLogsMiniAppRuntime(&miniAppFakeLauncher{})
	runtime.now = func() time.Time { return now }
	runtime.config = config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, LogsMiniApp: config.TelegramLogsMiniAppConfig{Enabled: true}}
	runtime.health = LogsMiniAppHealth{Enabled: true, State: MiniAppReady, Generation: 4}
	runtime.sessions["session"] = miniAppSession{UserID: 42, Generation: 4, ExpiresAt: now.Add(time.Minute)}

	unauthorized := httptest.NewRecorder()
	runtime.handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/stream?feed=runtime", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized stream status=%d want=401", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/stream?feed=unknown", nil)
	request.AddCookie(&http.Cookie{Name: miniAppCookieName, Value: "session"})
	invalid := httptest.NewRecorder()
	runtime.handler().ServeHTTP(invalid, request)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid stream feed status=%d want=400", invalid.Code)
	}
}

func TestMiniAppStreamProjectionsAreBoundedAndRedacted(t *testing.T) {
	const secret = "projection-secret-marker"
	runtimePayload := projectRuntimeEvent(runtimeevent.Event{
		Sequence: 7,
		Time:     time.Now(),
		Level:    "info",
		Kind:     "info",
		Name:     "runtime.test",
		Message:  "Authorization: Bearer " + secret,
		Fields:   []runtimeevent.Field{{Key: "token", Value: secret}},
	})
	if strings.Contains(runtimePayload["message"].(string), secret) {
		t.Fatalf("runtime projection leaked secret: %#v", runtimePayload)
	}
	fields, ok := runtimePayload["fields"].([]map[string]any)
	if !ok || len(fields) != 1 || fields[0]["value"] != "<redacted>" {
		t.Fatalf("runtime projection did not centrally redact fields: %#v", runtimePayload)
	}

	execution := shellruntime.ExecutionInfo{
		ID: "exec_1", WorkspaceID: "ws_1", Tool: "run_command",
		Command:          "curl -H \"Authorization: Bearer " + secret + "\" example.test",
		RequestedCommand: "curl --token " + secret, EffectiveCommand: "curl --api-key=" + secret, SecurityCommand: secret,
		CWD: "/workspace", CallID: "call_private", SessionHash: secret,
		ReceivedByInstanceID: "instance_private", ExecutedByInstanceID: "instance_private",
		StartedAt: time.Now().UTC().Format(time.RFC3339), Status: "running",
	}
	executionPayload := projectExecutionInfo(execution)
	encoded := strings.Join([]string{
		executionPayload["command"].(string),
		stringValue(executionPayload["cwd"]),
		stringValue(executionPayload["source"]),
	}, " ")
	if strings.Contains(encoded, secret) {
		t.Fatalf("execution projection leaked secret: %#v", executionPayload)
	}
	for _, key := range []string{"security_command", "call_id", "session_hash", "received_by_instance_id", "executed_by_instance_id"} {
		if _, ok := executionPayload[key]; ok {
			t.Fatalf("execution projection exposed %s: %#v", key, executionPayload)
		}
	}
	if strings.Contains(stringValue(executionPayload["requested_command"]), secret) || strings.Contains(stringValue(executionPayload["effective_command"]), secret) {
		t.Fatalf("execution projection leaked raw command detail: %#v", executionPayload)
	}

	toolPayload := projectToolEvent(runtimeactivity.Event{
		Sequence: 9, CallID: "call_1", Kind: "tool_call", Phase: "finish", Tool: "run_command",
		Message: "Authorization: Bearer " + secret, SessionHash: secret,
		Raw: map[string]any{"token": secret}, Timestamp: time.Now().UTC(),
	})
	if strings.Contains(toolPayload["message"].(string), secret) {
		t.Fatalf("tool projection leaked secret: %#v", toolPayload)
	}
	if _, ok := toolPayload["raw"]; ok {
		t.Fatalf("tool projection exposed provider raw detail: %#v", toolPayload)
	}
	for _, key := range []string{"session_hash", "session_access", "received_by_instance_id", "executed_by_instance_id"} {
		if _, ok := toolPayload[key]; ok {
			t.Fatalf("tool projection exposed %s: %#v", key, toolPayload)
		}
	}
}

func TestProjectRuntimeSnapshotUsesRetainedSequenceAsCursor(t *testing.T) {
	payload, latest := projectRuntimeSnapshot(application.LogsSnapshot{
		Events: []runtimeevent.Event{
			{Sequence: 5, Time: time.Now(), Level: "info", Kind: "info", Name: "five"},
			{Sequence: 9, Time: time.Now(), Level: "info", Kind: "info", Name: "nine"},
		},
		Total: 2,
	})
	if latest != 9 || payload["latest_sequence"] != uint64(9) {
		t.Fatalf("runtime snapshot cursor latest=%d payload=%#v", latest, payload)
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	result, _ := value.(string)
	return result
}
