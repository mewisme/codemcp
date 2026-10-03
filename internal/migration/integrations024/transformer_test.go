package integrations024

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	currentformat "go.mewis.me/codemcp/internal/configformat"
)

func TestTransformReleased024FixturePreservesIntegrationIntent(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	fixture, err := os.ReadFile("testdata/config-0.2.24.json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "released-config.json")
	destination := filepath.Join(root, "staged", "config.json")
	if err := os.WriteFile(source, fixture, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := Transform(Input{SourcePath: source, DestinationPath: destination})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Migrated || result.AlreadyApplied || result.SourceFormat != "json" {
		t.Fatalf("result = %#v", result)
	}
	if sourceData, err := os.ReadFile(source); err != nil || !bytes.Equal(sourceData, fixture) {
		t.Fatalf("released source changed: %v", err)
	}
	rootValue := decodeJSONFile(t, destination)
	if _, exists := rootValue["features"]; exists {
		t.Fatalf("legacy features survived migration: %#v", rootValue)
	}
	integrationValues := rootValue["integrations"].(map[string]any)
	ponytail := integrationValues["ponytail"].(map[string]any)
	caveman := integrationValues["caveman"].(map[string]any)
	if ponytail["active"] != false || ponytail["mode"] != "ultra" {
		t.Fatalf("ponytail = %#v", ponytail)
	}
	if caveman["active"] != true || caveman["mode"] != "wenyan-ultra" {
		t.Fatalf("caveman = %#v", caveman)
	}
	if rootValue["fixture_marker"] != "released-0.2.24" {
		t.Fatalf("unrelated config was not preserved: %#v", rootValue)
	}

	first, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Transform(Input{SourcePath: source, DestinationPath: destination})
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyApplied || !again.Migrated {
		t.Fatalf("second result = %#v", again)
	}
	second, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("idempotent migration changed destination bytes")
	}
}

func TestTransformReleasedFormatsAndEnabledAlias(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	fixtures := []struct {
		name string
		ext  string
		data string
	}{
		{name: "json", ext: ".json", data: `{"features":{"ponytail":{"enabled":false,"mode":"lite"},"caveman":{"active":false,"mode":"wenyan-full"}}}`},
		{name: "yaml", ext: ".yaml", data: "features:\n  ponytail:\n    enabled: false\n    mode: lite\n  caveman:\n    active: false\n    mode: wenyan-full\n"},
		{name: "toml", ext: ".toml", data: "[features.ponytail]\nenabled = false\nmode = \"lite\"\n\n[features.caveman]\nactive = false\nmode = \"wenyan-full\"\n"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "config"+fixture.ext)
			destination := filepath.Join(root, "config.json")
			if err := os.WriteFile(source, []byte(fixture.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Transform(Input{SourcePath: source, DestinationPath: destination}); err != nil {
				t.Fatal(err)
			}
			value := decodeJSONFile(t, destination)["integrations"].(map[string]any)
			if value["ponytail"].(map[string]any)["active"] != false || value["ponytail"].(map[string]any)["mode"] != "lite" {
				t.Fatalf("ponytail = %#v", value["ponytail"])
			}
			if value["caveman"].(map[string]any)["active"] != false || value["caveman"].(map[string]any)["mode"] != "wenyan-full" {
				t.Fatalf("caveman = %#v", value["caveman"])
			}
		})
	}
}

func TestTransformPreservesExplicitDefaultOnFalseAndLeavesAbsentNewFields(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	source := filepath.Join(root, "released-config.json")
	destination := filepath.Join(root, "staged", "config.json")
	fixture := "{\n" +
		"  \"features\": {\n" +
		"    \"ponytail\": {\"active\": true, \"mode\": \"full\"},\n" +
		"    \"caveman\": {\"active\": true, \"mode\": \"full\"}\n" +
		"  },\n" +
		"  \"telegram\": {\"enabled\": false, \"topics_enabled\": false, \"logs_mini_app\": {\"enabled\": false}},\n" +
		"  \"approval\": {\"semantic\": {\"enabled\": false}},\n" +
		"  \"permissions\": {\"mcp_config_read\": false, \"mcp_config_write\": false},\n" +
		"  \"notifications\": {\n" +
		"    \"approval\": {\"enabled\": false, \"telegram_enabled\": false},\n" +
		"    \"completion\": {\"enabled\": false, \"telegram_enabled\": false}\n" +
		"  },\n" +
		"  \"tunnel\": {\"enabled\": false}\n" +
		"}\n"
	if err := os.WriteFile(source, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(Input{SourcePath: source, DestinationPath: destination}); err != nil {
		t.Fatal(err)
	}
	raw := decodeJSONFile(t, destination)
	integrations := raw["integrations"].(map[string]any)
	if _, exists := integrations["typesafe"]; exists {
		t.Fatalf("migration synthesized an absent TypeSafe preference: %#v", integrations)
	}
	if _, exists := integrations["browser"]; exists {
		t.Fatalf("migration synthesized an absent browser preference: %#v", integrations)
	}
	loaded, err := config.LoadAt(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range config.DefaultOnCapabilityContracts() {
		value, err := config.RawValue(loaded, contract.Key)
		if err != nil {
			t.Fatalf("%s: %v", contract.Key, err)
		}
		if contract.Key == "integrations.typesafe.enabled" || contract.Key == "integrations.browser.enabled" || contract.Key == "integrations.chatgpt_web.enabled" {
			if value != "true" {
				t.Fatalf("absent %s = %q, want new default true", contract.Key, value)
			}
			continue
		}
		if value != "false" {
			t.Fatalf("migrated explicit %s = %q, want false", contract.Key, value)
		}
	}
}

func TestTransformInPlaceJSONIsAtomicAndIdempotent(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"features":{"ponytail":{"active":true,"mode":"full"},"caveman":{"active":false,"mode":"ultra"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := Transform(Input{SourcePath: path, DestinationPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Migrated || first.AlreadyApplied {
		t.Fatalf("first result = %#v", first)
	}
	second, err := Transform(Input{SourcePath: path, DestinationPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if second.Migrated || !second.AlreadyApplied {
		t.Fatalf("second result = %#v", second)
	}
	value := decodeJSONFile(t, path)
	if _, exists := value["features"]; exists {
		t.Fatal("legacy features survived in-place migration")
	}
}

func TestTransformRejectsMalformedOrAmbiguousReleasedStateWithoutWritingDestination(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	fixtures := []string{
		`{"features":[]}`,
		`{"features":{"ponytail":{"active":"yes","mode":"full"}}}`,
		`{"features":{"ponytail":{"active":true,"enabled":"no","mode":"full"}}}`,
		`{"features":{"ponytail":{"active":true,"mode":"review"}}}`,
		`{"features":{"caveman":{"active":true,"mode":"wenyan"}}}`,
		`{"features":{"ponytail":{"active":true,"mode":"full","mystery":true}}}`,
		`{"features":{"ponytail":{"active":true,"mode":"full"}},"integrations":{"ponytail":{"active":true,"mode":"full"}}}`,
	}
	for index, fixture := range fixtures {
		root := t.TempDir()
		source := filepath.Join(root, "source.json")
		destination := filepath.Join(root, "destination.json")
		if err := os.WriteFile(source, []byte(fixture), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Transform(Input{SourcePath: source, DestinationPath: destination}); err == nil {
			t.Fatalf("fixture %d accepted", index)
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("fixture %d wrote destination on failure: %v", index, err)
		}
	}
}

func TestTransformRefusesDifferentExistingDestination(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	source := filepath.Join(root, "source.json")
	destination := filepath.Join(root, "destination.json")
	if err := os.WriteFile(source, []byte(`{"features":{"ponytail":{"active":true,"mode":"full"},"caveman":{"active":true,"mode":"full"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"owned\":true}\n")
	if err := os.WriteFile(destination, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Transform(Input{SourcePath: source, DestinationPath: destination}); err == nil {
		t.Fatal("existing destination conflict accepted")
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("destination conflict was overwritten")
	}
}

func decodeJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := currentformat.DecodeGeneric(currentformat.JSON, data)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("config root = %#v", raw)
	}
	return root
}
