package mcp

import (
	"encoding/json"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCanonicalRequestContextMatchesDirectAndSDKModernEnvelopes(t *testing.T) {
	wire := []byte(`{
		"name":"probe",
		"arguments":{},
		"requestState":"opaque-state",
		"inputResponses":{"confirm":{"action":"accept","content":{"value":"yes"}}},
		"_meta":{
			"io.modelcontextprotocol/protocolVersion":"2026-07-28",
			"io.modelcontextprotocol/clientInfo":{"name":"context-test","title":"Context Test","version":"1.4.0"},
			"io.modelcontextprotocol/clientCapabilities":{"extensions":{"example/feature":{"enabled":true}}},
			"io.modelcontextprotocol/logLevel":"warning"
		}
	}`)
	var direct map[string]any
	if err := json.Unmarshal(wire, &direct); err != nil {
		t.Fatal(err)
	}
	directContext, err := requestContextFromParams(direct)
	if err != nil {
		t.Fatal(err)
	}

	var sdkParams sdkmcp.CallToolParamsRaw
	if err := json.Unmarshal(wire, &sdkParams); err != nil {
		t.Fatal(err)
	}
	sdkContext := RequestContextFromSDK(&sdkmcp.CallToolRequest{Params: &sdkParams})

	directJSON, err := json.Marshal(directContext)
	if err != nil {
		t.Fatal(err)
	}
	sdkJSON, err := json.Marshal(sdkContext)
	if err != nil {
		t.Fatal(err)
	}
	if string(directJSON) != string(sdkJSON) {
		t.Fatalf("canonical contexts differ:\ndirect=%s\nsdk=%s", directJSON, sdkJSON)
	}
	if !directContext.Modern() || directContext.ClientInfo == nil || directContext.ClientInfo.Name != "context-test" {
		t.Fatalf("canonical context=%#v", directContext)
	}
	if directContext.LogLevelHint != "warning" || directContext.RequestState != "opaque-state" {
		t.Fatalf("canonical request hints=%#v", directContext)
	}
	if directContext.NegotiatedExtensions["example/feature"] == nil || directContext.InputResponses["confirm"] == nil {
		t.Fatalf("canonical request extensions/input=%#v", directContext)
	}
}

func TestRequestContextIsDetachedFromCallerOwnedMaps(t *testing.T) {
	input := RequestContext{
		ProtocolVersion:      SupportedProtocolVersion,
		NegotiatedExtensions: map[string]any{"example/feature": map[string]any{"enabled": true}},
		InputResponses:       map[string]any{"confirm": map[string]any{"action": "accept"}},
	}
	ctx := WithRequestContext(t.Context(), input)
	input.NegotiatedExtensions["example/feature"].(map[string]any)["enabled"] = false
	input.InputResponses["confirm"].(map[string]any)["action"] = "decline"

	got := RequestContextFromContext(ctx)
	extension := got.NegotiatedExtensions["example/feature"].(map[string]any)
	response := got.InputResponses["confirm"].(map[string]any)
	if extension["enabled"] != true || response["action"] != "accept" {
		t.Fatalf("stored request context was mutated through caller-owned maps: %#v", got)
	}
}
