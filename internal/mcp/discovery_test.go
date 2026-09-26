package mcp

import (
	"encoding/json"
	"reflect"
	"testing"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/version"
)

func TestServerInstructionsUseSharedInstructionGuidance(t *testing.T) {
	if ProjectServerInstructions(BaseProfile()) != instructioncontext.StaticServerInstructions() {
		t.Fatalf("server instructions drifted from shared guidance")
	}
}

func TestDiscoverResultMatchesModernProtocolShape(t *testing.T) {
	result := BuildDiscoverResult(BaseProfile())
	if len(result.Capabilities.Extensions) != 0 {
		t.Fatalf("discovery advertised transport extensions it does not implement: %#v", result.Capabilities.Extensions)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	wantKeys := map[string]bool{
		"resultType": true, "supportedVersions": true, "capabilities": true,
		"_meta": true, "instructions": true, "ttlMs": true, "cacheScope": true,
	}
	if len(wire) != len(wantKeys) {
		t.Fatalf("discover keys=%v", reflect.ValueOf(wire).MapKeys())
	}
	for key := range wire {
		if !wantKeys[key] {
			t.Fatalf("unexpected discover key %q", key)
		}
	}
	if _, exists := wire["serverInfo"]; exists {
		t.Fatal("serverInfo leaked outside result._meta")
	}
	meta, _ := wire["_meta"].(map[string]any)
	info, _ := meta[serverInfoMetaKey].(map[string]any)
	if info["name"] != "codemcp" || info["version"] != version.Version {
		t.Fatalf("serverInfo=%#v", info)
	}
	versions, _ := wire["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != SupportedProtocolVersion || wire["resultType"] != "complete" || wire["cacheScope"] != defaultCacheScope || wire["ttlMs"] != float64(defaultCacheTTLMS) {
		t.Fatalf("discover result=%#v", wire)
	}
}

func TestCanonicalVersionMatchesDiscoveryAndSDKServer(t *testing.T) {
	previous := version.Version
	defer func() { version.Version = previous }()
	version.Version = "7.8.9"

	result := BuildDiscoverResult(BaseProfile())
	info, ok := result.Meta[serverInfoMetaKey].(ServerDescriptor)
	if !ok || info.Version != version.Version {
		t.Fatalf("discover server info=%#v", result.Meta[serverInfoMetaKey])
	}
	descriptors := DescribeProtocol(nil)
	implementation, options := ProjectSDKServer(BaseProfile(), descriptors)
	if implementation.Version != version.Version || options.Instructions != result.Instructions {
		t.Fatalf("SDK projection=%#v instructions_match=%t", implementation, options.Instructions == result.Instructions)
	}
}
