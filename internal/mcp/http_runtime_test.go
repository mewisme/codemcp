package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/integrations"
	"go.mewis.me/codemcp/internal/tools"
)

func modernRequest(method, body string) *http.Request {
	return rawModernRequest(method, modernRequestBody(body))
}

func rawModernRequest(method, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set(ProtocolVersionHeader, SupportedProtocolVersion)
	req.Header.Set(MethodHeader, method)
	return req
}

func modernRequestBody(body string) string {
	if len(body) > 1<<20 {
		return body
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var request map[string]any
	if err := decoder.Decode(&request); err != nil {
		return body
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return body
	}
	params, _ := request["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
		request["params"] = params
	}
	meta, _ := params["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		params["_meta"] = meta
	}
	if _, exists := meta["io.modelcontextprotocol/protocolVersion"]; !exists {
		meta["io.modelcontextprotocol/protocolVersion"] = SupportedProtocolVersion
	}
	if _, exists := meta["io.modelcontextprotocol/clientCapabilities"]; !exists {
		meta["io.modelcontextprotocol/clientCapabilities"] = map[string]any{}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return body
	}
	return string(data)
}

func decodeResponse(t *testing.T, res *httptest.ResponseRecorder) Response {
	t.Helper()
	var response Response
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHTTPRuntimeDiscoverIsStateless(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("server/discover", `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test-client","version":"1.0.0"}}}}`)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusOK, res.Body.String())
	}
	if res.Header().Get("MCP-Session-Id") != "" {
		t.Fatal("modern response must not create MCP-Session-Id")
	}
	response := decodeResponse(t, res)
	result, ok := response.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", response.Result)
	}
	versions, ok := result["supportedVersions"].([]any)
	if !ok || len(versions) != 1 || versions[0] != SupportedProtocolVersion {
		t.Fatalf("supportedVersions = %#v", result["supportedVersions"])
	}
	if result["cacheScope"] != defaultCacheScope || result["ttlMs"] != float64(defaultCacheTTLMS) {
		t.Fatalf("cache hints = %#v/%#v", result["ttlMs"], result["cacheScope"])
	}
	instructions, ok := result["instructions"].(string)
	if !ok || instructions != ServerInstructions {
		t.Fatalf("instructions = %#v", result["instructions"])
	}
	for _, expected := range []string{"workspace_register", "workspace_status", "workspace_container_context", "agent_status", "project_context", "list_skills", "load_skill", "load_path_rules", "persisted shell cwd"} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("instructions missing %q: %s", expected, instructions)
		}
	}
	capabilities, _ := result["capabilities"].(map[string]any)
	toolsCapability, _ := capabilities["tools"].(map[string]any)
	if toolsCapability["listChanged"] != true {
		t.Fatalf("tools capability = %#v", toolsCapability)
	}
}

func TestHTTPRuntimeToolsListHasCacheHints(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("tools/list", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusOK, res.Body.String())
	}
	response := decodeResponse(t, res)
	result, ok := response.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", response.Result)
	}
	if result["cacheScope"] != defaultCacheScope || result["ttlMs"] != float64(defaultCacheTTLMS) {
		t.Fatalf("cache hints = %#v/%#v", result["ttlMs"], result["cacheScope"])
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools = %#v", result["tools"])
	}
	var previous string
	for i, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tool[%d] = %#v", i, item)
		}
		name, _ := tool["name"].(string)
		if previous != "" && name < previous {
			t.Fatalf("tools are not sorted: %q before %q", previous, name)
		}
		previous = name
	}
}

func TestHTTPRuntimeToolCallRequiresMatchingNameHeader(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("tools/call", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"git_status","arguments":{"workspace_id":"ws_missing"}}}`)
	req.Header.Set(NameHeader, "read_text_file")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrHeaderMismatch {
		t.Fatalf("error = %#v, want code %d", response.Error, ErrHeaderMismatch)
	}
}

func TestHTTPRuntimePonytailUsesBuiltInConfiguredMode(t *testing.T) {
	integrationConfig := integrations.Default()
	integrationConfig.Ponytail.Mode = "ultra"
	toolRuntime := tools.NewRuntimeWithIntegrations(integrationConfig)
	item, err := toolRuntime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewHTTPRuntimeWithTools(toolRuntime)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"ponytail_turn","arguments":{"workspace_id":%q,"prompt":"continue","action":"refresh"}}}`, item.ID)
	req := modernRequest("tools/call", body)
	req.Header.Set(NameHeader, "ponytail_turn")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	for _, expected := range []string{`"available":true`, `"active":true`, `"mode":"ultra"`, "PONYTAIL MODE ACTIVE", "## The ladder"} {
		if !strings.Contains(res.Body.String(), expected) {
			t.Fatalf("response missing %q: %s", expected, res.Body.String())
		}
	}
}

func TestHTTPRuntimeCavemanUsesBuiltInConfiguredMode(t *testing.T) {
	integrationConfig := integrations.Default()
	integrationConfig.Caveman.Mode = "wenyan-ultra"
	toolRuntime := tools.NewRuntimeWithIntegrations(integrationConfig)
	item, err := toolRuntime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewHTTPRuntimeWithTools(toolRuntime)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"caveman_turn","arguments":{"workspace_id":%q,"prompt":"continue","action":"refresh"}}}`, item.ID)
	req := modernRequest("tools/call", body)
	req.Header.Set(NameHeader, "caveman_turn")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	for _, expected := range []string{`"available":true`, `"active":true`, `"mode":"wenyan-ultra"`, "CAVEMAN MODE ACTIVE", "## Rules", "| **wenyan-ultra** |"} {
		if !strings.Contains(res.Body.String(), expected) {
			t.Fatalf("response missing %q: %s", expected, res.Body.String())
		}
	}
	if strings.Contains(res.Body.String(), "| **full** |") {
		t.Fatalf("response retained inactive Caveman intensity row: %s", res.Body.String())
	}
}

func TestHTTPRuntimeModernContextIgnoresTransportSessionMetadata(t *testing.T) {
	registry := tools.NewRegistry()
	type observed struct {
		session     string
		correlation tools.ApprovalCorrelation
		request     RequestContext
	}
	seen := make(chan observed, 2)
	registry.MustRegister("session_probe", tools.Schema{Name: "session_probe", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		seen <- observed{session: tools.MCPSessionID(ctx), correlation: tools.ApprovalCorrelationFromContext(ctx), request: RequestContextFromContext(ctx)}
		return tools.TextResult("ok"), nil
	})
	toolRuntime := &tools.Runtime{Registry: registry, SessionAccess: tools.NewSessionWorkspaceAccessManager()}
	runtime := NewHTTPRuntimeWithTools(toolRuntime)
	body := `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"session_probe","arguments":{},"requestState":"opaque","inputResponses":{"confirm":{"action":"accept","content":{}}},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"stateless-test","version":"1.2.3"},"io.modelcontextprotocol/clientCapabilities":{"extensions":{"example/one":{"enabled":true}}},"io.modelcontextprotocol/logLevel":"warning"}}}`
	for index, sessionID := range []string{"session-http-a", "session-http-b"} {
		req := modernRequest("tools/call", strings.Replace(body, `"id":31`, fmt.Sprintf(`"id":%d`, 31+index), 1))
		req.Header.Set(NameHeader, "session_probe")
		req.Header.Set(SessionIDHeader, sessionID)
		res := httptest.NewRecorder()
		runtime.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", res.Code, res.Body.String())
		}
	}
	first, second := <-seen, <-seen
	if first.session != "" || second.session != "" {
		t.Fatalf("modern request inherited transport session: %q / %q", first.session, second.session)
	}
	if first.correlation.CallerID == "" || first.correlation.CallerID != second.correlation.CallerID || !strings.HasPrefix(first.correlation.CallerID, "apc_") || first.correlation.RequestID == second.correlation.RequestID || !strings.HasPrefix(first.correlation.RequestID, "apr_") || !strings.HasPrefix(second.correlation.RequestID, "apr_") {
		t.Fatalf("approval correlations=%#v / %#v", first.correlation, second.correlation)
	}
	firstJSON, _ := json.Marshal(first.request)
	secondJSON, _ := json.Marshal(second.request)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("equivalent modern contexts differ: %s / %s", firstJSON, secondJSON)
	}
	if first.request.ProtocolVersion != SupportedProtocolVersion || first.request.ClientInfo == nil || first.request.ClientInfo.Name != "stateless-test" || first.request.LogLevelHint != "warning" || first.request.RequestState != "opaque" || first.request.NegotiatedExtensions["example/one"] == nil || first.request.InputResponses["confirm"] == nil {
		t.Fatalf("canonical modern context=%#v", first.request)
	}
	if toolRuntime.SessionAccess.Count() != 0 {
		t.Fatalf("spoofed modern session metadata created legacy session state: %d", toolRuntime.SessionAccess.Count())
	}
}

func TestHTTPRuntimeRejectsMissingProtocolHeader(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`))
	req.Header.Set(MethodHeader, "tools/list")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrHeaderMismatch {
		t.Fatalf("error = %#v, want code %d", response.Error, ErrHeaderMismatch)
	}
}

func TestHTTPRuntimeRejectsMethodHeaderMismatch(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("tools/call", `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrHeaderMismatch {
		t.Fatalf("error = %#v, want code %d", response.Error, ErrHeaderMismatch)
	}
}

func TestHTTPRuntimeRejectsInvalidToolParams(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("tools/call", `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"arguments":{}}}`)
	req.Header.Set(NameHeader, "missing")
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrHeaderMismatch {
		t.Fatalf("error = %#v, want header validation before params validation", response.Error)
	}
}

func TestHTTPRuntimeRejectsLegacyMethods(t *testing.T) {
	runtime := NewHTTPRuntime()
	for _, method := range []string{"initialize", "ping", "logging/setLevel", "resources/subscribe", "resources/unsubscribe"} {
		t.Run(method, func(t *testing.T) {
			req := modernRequest(method, `{"jsonrpc":"2.0","id":7,"method":"`+method+`","params":{}}`)
			res := httptest.NewRecorder()
			runtime.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d: %s", res.Code, http.StatusNotFound, res.Body.String())
			}
			response := decodeResponse(t, res)
			if response.Error == nil || response.Error.Code != ErrMethodNotFound {
				t.Fatalf("error = %#v, want code %d", response.Error, ErrMethodNotFound)
			}
		})
	}
}

func TestHTTPRuntimeRejectsGETAndDELETE(t *testing.T) {
	runtime := NewHTTPRuntime()
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/mcp", nil)
		res := httptest.NewRecorder()
		runtime.ServeHTTP(res, req)
		if res.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want %d", method, res.Code, http.StatusMethodNotAllowed)
		}
	}
}

func TestHTTPRuntimeRejectsOversizedBody(t *testing.T) {
	runtime := NewHTTPRuntime()
	body := `{"jsonrpc":"2.0","id":8,"method":"server/discover","params":{},"padding":"` + strings.Repeat("x", int(MaxRequestBodyBytes)) + `"}`
	req := modernRequest("server/discover", body)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHTTPRuntimeRejectsTrailingJSON(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("server/discover", `{"jsonrpc":"2.0","id":9,"method":"server/discover"}{}`)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrParse {
		t.Fatalf("error = %#v, want code %d", response.Error, ErrParse)
	}
}
func TestHTTPRuntimeListenRequiresNotificationsFilter(t *testing.T) {
	runtime := NewHTTPRuntime()
	req := modernRequest("subscriptions/listen", `{"jsonrpc":"2.0","id":10,"method":"subscriptions/listen","params":{}}`)
	res := httptest.NewRecorder()
	runtime.ServeHTTP(res, req)
	response := decodeResponse(t, res)
	if response.Error == nil || response.Error.Code != ErrInvalidParams {
		t.Fatalf("error = %#v, want code %d", response.Error, ErrInvalidParams)
	}
}

func TestHTTPRuntimeStreamsToolChangesAndGracefulClose(t *testing.T) {
	runtime := NewHTTPRuntime()
	server := httptest.NewServer(runtime.Handler())
	defer server.Close()

	body := strings.NewReader(modernRequestBody(`{"jsonrpc":"2.0","id":11,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true,"promptsListChanged":true},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`))
	req, err := http.NewRequest(http.MethodPost, server.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(ProtocolVersionHeader, SupportedProtocolVersion)
	req.Header.Set(MethodHeader, "subscriptions/listen")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status/content-type = %d/%q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(res.Body)

	ack := readSSEFrame(t, reader)
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("ack = %#v", ack)
	}
	ackParams, _ := ack["params"].(map[string]any)
	honored, _ := ackParams["notifications"].(map[string]any)
	if honored["toolsListChanged"] != true {
		t.Fatalf("honored notifications = %#v", honored)
	}
	if _, exists := honored["promptsListChanged"]; exists {
		t.Fatalf("unsupported prompt filter was honored: %#v", honored)
	}
	assertSubscriptionID(t, ackParams, float64(11))

	runtime.Server.Tools.Registry.MustRegister("subscription_test_tool", tools.Schema{
		Name: "subscription_test_tool", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, map[string]any) (tools.Result, error) {
		return tools.TextResult("ok"), nil
	})

	changed := readSSEFrame(t, reader)
	if changed["method"] != "notifications/tools/list_changed" {
		t.Fatalf("changed = %#v", changed)
	}
	changedParams, _ := changed["params"].(map[string]any)
	assertSubscriptionID(t, changedParams, float64(11))

	runtime.CloseSubscriptions()
	final := readSSEFrame(t, reader)
	if final["id"] != float64(11) {
		t.Fatalf("final id = %#v", final["id"])
	}
	result, _ := final["result"].(map[string]any)
	if result["resultType"] != "complete" {
		t.Fatalf("final result = %#v", result)
	}
	meta, _ := result["_meta"].(map[string]any)
	if meta["io.modelcontextprotocol/subscriptionId"] != float64(11) {
		t.Fatalf("final meta = %#v", meta)
	}
	if _, ok := meta["io.modelcontextprotocol/serverInfo"].(map[string]any); !ok {
		t.Fatalf("final server info = %#v", meta["io.modelcontextprotocol/serverInfo"])
	}
}

func TestHTTPRuntimeListenContextCancellationClosesStream(t *testing.T) {
	runtime := NewHTTPRuntime()
	server := httptest.NewServer(runtime.Handler())
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	body := strings.NewReader(modernRequestBody(`{"jsonrpc":"2.0","id":"listen-cancel","method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true}}}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(ProtocolVersionHeader, SupportedProtocolVersion)
	req.Header.Set(MethodHeader, "subscriptions/listen")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(res.Body)
	_ = readSSEFrame(t, reader)

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, reader)
		done <- err
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listen stream did not close after request cancellation")
	}
	_ = res.Body.Close()
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var value map[string]any
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			t.Fatalf("decode SSE frame: %v", err)
		}
		return value
	}
}

func assertSubscriptionID(t *testing.T, params map[string]any, want any) {
	t.Helper()
	meta, _ := params["_meta"].(map[string]any)
	if meta["io.modelcontextprotocol/subscriptionId"] != want {
		t.Fatalf("subscription id = %#v, want %#v", meta["io.modelcontextprotocol/subscriptionId"], want)
	}
}
