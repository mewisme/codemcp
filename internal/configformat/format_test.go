package configformat

import (
	"os"
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

func TestRoundTripFormatsHonorJSONTags(t *testing.T) {
	value := testConfig{}
	value.Server.Port = 37421
	value.Auth.MCPTokenHash = "secret"
	for _, format := range []Format{JSON, YAML, TOML} {
		data, err := Marshal(format, value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded testConfig
		if err := Unmarshal(format, data, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, decoded) {
			t.Fatalf("%s round trip = %#v, want %#v", format, decoded, value)
		}
	}
}

func TestDiscoverMainConfig(t *testing.T) {
	root := t.TempDir()
	source, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if source.Exists || source.Format != JSON || filepath.Base(source.Path) != "config.json" {
		t.Fatalf("source = %#v", source)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[server]\nport=37421\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err = Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !source.Exists || source.Format != TOML || source.Ext != ".toml" {
		t.Fatalf("source = %#v", source)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("server: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("expected ambiguous config discovery error")
	}
}

func TestStructuredPathUsesCanonicalJSONExtension(t *testing.T) {
	for _, name := range []string{"config.json", "config.yaml", "config.yml", "config.toml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, name), []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := filepath.Ext(StructuredPath(root, "workspaces")); got != ".json" {
				t.Fatalf("extension = %q, want .json", got)
			}
			if got := ExtensionForRoot(root); got != ".json" {
				t.Fatalf("root extension = %q, want .json", got)
			}
		})
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
