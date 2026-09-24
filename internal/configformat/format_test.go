package configformat

import (
	"path/filepath"
	"reflect"
	"testing"
)

type testConfig struct {
	Server struct {
		Port int `json:"port"`
	} `json:"server"`
	Auth struct {
		MCPTokenHash string `json:"mcp_token_hash"`
	} `json:"auth"`
}

func TestJSONRoundTripHonorsJSONTags(t *testing.T) {
	value := testConfig{}
	value.Server.Port = 37421
	value.Auth.MCPTokenHash = "secret"
	data, err := Marshal(JSON, value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded testConfig
	if err := Unmarshal(JSON, data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, decoded) {
		t.Fatalf("JSON round trip = %#v, want %#v", decoded, value)
	}
}

func TestCurrentFormatRejectsNonJSON(t *testing.T) {
	for _, path := range []string{"config.yaml", "config.yml", "config.toml"} {
		if _, err := Detect(path); err == nil {
			t.Fatalf("Detect(%q) unexpectedly succeeded", path)
		}
	}
	if _, err := Marshal(Format("yaml"), map[string]any{}); err == nil {
		t.Fatal("legacy format marshal unexpectedly succeeded")
	}
}

func TestStructuredPathsUseCanonicalJSONExtension(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"config", "workspaces", "oauth", "upstream", "tunnel"} {
		if got := filepath.Ext(StructuredPath(root, name)); got != ".json" {
			t.Fatalf("%s extension = %q, want .json", name, got)
		}
	}
	if got := ExtensionForRoot(root); got != ".json" {
		t.Fatalf("root extension = %q, want .json", got)
	}
}

func TestMergeGenericPreservesUnknownNestedKeysAndReplacesLeaves(t *testing.T) {
	base := map[string]any{"server": map[string]any{"port": int64(3000), "legacy": true}, "unknown": "keep", "list": []any{"old"}}
	overlay := map[string]any{"server": map[string]any{"port": int64(4000), "enabled": true}, "list": []any{"new"}}
	merged := MergeGeneric(base, overlay).(map[string]any)
	server := merged["server"].(map[string]any)
	if server["port"] != int64(4000) || server["legacy"] != true || server["enabled"] != true || merged["unknown"] != "keep" {
		t.Fatalf("merged = %#v", merged)
	}
	list := merged["list"].([]any)
	if len(list) != 1 || list[0] != "new" {
		t.Fatalf("list = %#v", list)
	}
}
