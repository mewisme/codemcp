package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/approval"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

func TestConfigToolsAreRegisteredWithCanonicalContracts(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	for _, tc := range []struct {
		name   string
		output json.RawMessage
	}{
		{mcpconfigwire.ListToolName, mcpconfigwire.ListOutputSchema},
		{mcpconfigwire.GetToolName, mcpconfigwire.GetOutputSchema},
	} {
		schema, ok := runtime.Registry.Schema(tc.name)
		if !ok {
			t.Fatalf("config read tool %q is not registered", tc.name)
		}
		if string(schema.OutputSchema) != string(tc.output) {
			t.Fatalf("%s output schema drifted\ngot=%s\nwant=%s", tc.name, schema.OutputSchema, tc.output)
		}
		if schema.Annotations["readOnlyHint"] != true || schema.Annotations["idempotentHint"] != true ||
			schema.Annotations["destructiveHint"] != false || schema.Annotations["openWorldHint"] != false {
			t.Fatalf("%s annotations=%#v", tc.name, schema.Annotations)
		}
	}
	set, ok := runtime.Registry.Schema(mcpconfigwire.SetToolName)
	if !ok {
		t.Fatal("config_set is not registered")
	}
	if string(set.OutputSchema) != string(mcpconfigwire.SetOutputSchema) {
		t.Fatalf("config_set output schema drifted\ngot=%s\nwant=%s", set.OutputSchema, mcpconfigwire.SetOutputSchema)
	}
	if set.Annotations["readOnlyHint"] != false || set.Annotations["idempotentHint"] != false ||
		set.Annotations["destructiveHint"] != false || set.Annotations["openWorldHint"] != false {
		t.Fatalf("config_set annotations=%#v", set.Annotations)
	}
}

type configReadFixture struct {
	settings []mcpconfigwire.Setting
	get      mcpconfigwire.Setting
	code     mcpconfigwire.ErrorCode
	reads    *int
}

func (fixture configReadFixture) List(context.Context, string) ([]mcpconfigwire.Setting, mcpconfigwire.ErrorCode) {
	if fixture.reads != nil {
		*fixture.reads++
	}
	return append([]mcpconfigwire.Setting(nil), fixture.settings...), fixture.code
}

func (fixture configReadFixture) Get(context.Context, string) (mcpconfigwire.Setting, mcpconfigwire.ErrorCode) {
	return fixture.get, fixture.code
}

func TestConfigListIsBoundedAndCursorIsPrefixBound(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	values := []mcpconfigwire.Setting{
		{Key: "server.enabled", Readable: true, Writable: true},
		{Key: "server.port", Readable: true, Writable: true},
		{Key: "server.expose", Readable: true, Writable: true},
	}
	runtime.SetConfigReadProvider(configReadFixture{settings: values})

	first, err := runtime.Call(context.Background(), mcpconfigwire.ListToolName, map[string]any{"prefix": "server", "limit": 2})
	if err != nil || first.IsError {
		t.Fatalf("first page result=%#v err=%v", first, err)
	}
	page, ok := first.StructuredContent.(mcpconfigwire.ListResult)
	if !ok || len(page.Settings) != 2 || page.NextCursor == "" {
		t.Fatalf("first page=%#v", first.StructuredContent)
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(page.NextCursor)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	for _, forbidden := range []string{"server.enabled", "server.port", "server.expose"} {
		if strings.Contains(string(decoded), forbidden) {
			t.Fatalf("cursor leaked setting key %q: %s", forbidden, decoded)
		}
	}

	second, err := runtime.Call(context.Background(), mcpconfigwire.ListToolName, map[string]any{"prefix": "server", "limit": 2, "cursor": page.NextCursor})
	if err != nil || second.IsError {
		t.Fatalf("second page result=%#v err=%v", second, err)
	}
	page2 := second.StructuredContent.(mcpconfigwire.ListResult)
	if len(page2.Settings) != 1 || page2.Settings[0].Key != "server.expose" || page2.NextCursor != "" {
		t.Fatalf("second page=%#v", page2)
	}

	for _, args := range []map[string]any{
		{"prefix": "admin", "limit": 2, "cursor": page.NextCursor},
		{"limit": mcpconfigwire.MaxListLimit + 1},
		{"limit": 1.5},
		{"unknown": true},
	} {
		result, err := runtime.Call(context.Background(), mcpconfigwire.ListToolName, args)
		if err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, string(mcpconfigwire.ErrorInvalidRequest)) {
			t.Fatalf("invalid args=%#v result=%#v err=%v", args, result, err)
		}
	}

	reads := 0
	runtime.SetConfigReadProvider(configReadFixture{settings: values, reads: &reads})
	result, err := runtime.Call(context.Background(), mcpconfigwire.ListToolName, map[string]any{"cursor": "not-base64!"})
	if err != nil || !result.IsError || reads != 0 {
		t.Fatalf("invalid cursor reached provider: result=%#v err=%v reads=%d", result, err, reads)
	}

	runtime.SetConfigReadProvider(configReadFixture{settings: []mcpconfigwire.Setting{}})
	empty, err := runtime.Call(context.Background(), mcpconfigwire.ListToolName, map[string]any{})
	if err != nil || empty.IsError {
		t.Fatalf("empty result=%#v err=%v", empty, err)
	}
	data, err := json.Marshal(empty.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"settings":[]`) {
		t.Fatalf("empty list serialized as non-array: %s", data)
	}
}

func TestConfigReadToolsFailClosedWithoutProvider(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntime()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{mcpconfigwire.ListToolName, map[string]any{}},
		{mcpconfigwire.GetToolName, map[string]any{"key": "server.port"}},
	} {
		result, err := runtime.Call(context.Background(), tc.name, tc.args)
		if err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, string(mcpconfigwire.ErrorAccessDenied)) {
			t.Fatalf("%s result=%#v err=%v", tc.name, result, err)
		}
	}
}

func TestConfigSetCallObservationUsesValueFreeArguments(t *testing.T) {
	args := map[string]any{
		"workspace_id": "ws_scope",
		"changes": []any{
			map[string]any{"key": "server.port", "value": "4000"},
			map[string]any{"key": "permissions.allow_dirs", "value": "/private/value"},
		},
	}
	ctx := WithCallDetails(context.Background(), "tools/call", map[string]any{
		"name": mcpconfigwire.SetToolName, "arguments": args,
	})
	ctx = WithCallRequest(ctx, map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params":  map[string]any{"name": mcpconfigwire.SetToolName, "arguments": args},
	})
	raw := callRaw(ctx, "http", mcpconfigwire.SetToolName, args)
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"4000", "/private/value"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("config_set observation leaked value %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{"server.port", "permissions.allow_dirs", "change_count"} {
		if !strings.Contains(text, required) {
			t.Fatalf("config_set observation lost safe summary %q: %s", required, text)
		}
	}
}

func TestConfigSetApprovalResultsUseValueFreeArguments(t *testing.T) {
	raw := json.RawMessage(`{"workspace_id":"ws_scope","changes":[{"key":"server.port","value":"4000"},{"key":"permissions.allow_dirs","value":"/private/value"}]}`)
	results := []Result{
		approvalRequiredResult(approval.Challenge{
			ID: "chg_1", WorkspaceID: "ws_scope", TargetTool: mcpconfigwire.SetToolName, Arguments: raw,
			GuardReason: "contains /private/value", Command: "set 4000",
		}),
		approvalResolutionResult(approval.Request{
			ID: "apr_1", Status: approval.StatusApproved, WorkspaceID: "ws_scope", TargetTool: mcpconfigwire.SetToolName, Arguments: raw,
		}),
		approvalMismatchResult(&approval.MismatchError{
			RequestID: "apr_1", TargetTool: mcpconfigwire.SetToolName, Expected: raw,
			Actual: json.RawMessage(`{"changes":[{"key":"server.port","value":"5000"}]}`),
		}),
	}
	for index, result := range results {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{"4000", "5000", "/private/value"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("approval result %d leaked value %q: %s", index, forbidden, text)
			}
		}
		if !strings.Contains(text, "server.port") || !strings.Contains(text, "change_count") {
			t.Fatalf("approval result %d lost value-free summary: %s", index, text)
		}
	}
}
