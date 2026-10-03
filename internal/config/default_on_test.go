package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

func TestDefaultOnCapabilityContractsMatchFreshDefaults(t *testing.T) {
	contracts := DefaultOnCapabilityContracts()
	if len(contracts) != 13 {
		t.Fatalf("default-on contracts = %d, want 13", len(contracts))
	}
	cfg := Default()
	seen := map[string]bool{}
	for _, contract := range contracts {
		if seen[contract.Key] {
			t.Fatalf("duplicate default-on contract %q", contract.Key)
		}
		seen[contract.Key] = true
		if !contract.FreshDefault || contract.Prerequisite == "" || contract.ConfiguredState == "" || contract.EffectiveState == "" || contract.MissingPrerequisiteBehavior == "" || contract.ExplicitDisableBehavior == "" {
			t.Fatalf("incomplete default-on contract for %q: %#v", contract.Key, contract)
		}
		value, err := RawValue(cfg, contract.Key)
		if err != nil {
			t.Fatalf("%s: %v", contract.Key, err)
		}
		if value != "true" {
			t.Fatalf("fresh default %s = %q, want true", contract.Key, value)
		}
		spec, ok := FieldByKey(contract.Key)
		if !ok {
			t.Fatalf("missing field metadata for %q", contract.Key)
		}
		if !strings.Contains(spec.Details, "Enabled by default.") {
			t.Fatalf("%s details do not describe the canonical default: %q", contract.Key, spec.Details)
		}
		state, err := State(cfg, spec)
		if err != nil || state != FieldStateDefault {
			t.Fatalf("%s state = %q, err=%v", contract.Key, state, err)
		}
	}
	codeGraph, ok := FieldByKey("integrations.codegraph.enabled")
	if !ok || !strings.Contains(codeGraph.Details, "Enabled by default.") {
		t.Fatalf("CodeGraph default metadata is stale: %#v", codeGraph)
	}
}

func TestDefaultOnFreshPersistenceWritesTrue(t *testing.T) {
	root := t.TempDir()
	if err := SaveAt(root, Default()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		t.Fatal(err)
	}
	document := raw.(map[string]any)
	for _, contract := range DefaultOnCapabilityContracts() {
		parts := strings.Split(contract.Key, ".")
		parent := genericParent(t, document, parts[:len(parts)-1])
		value, ok := parent[parts[len(parts)-1]].(bool)
		if !ok || !value {
			t.Fatalf("persisted fresh %s = %#v, want true", contract.Key, parent[parts[len(parts)-1]])
		}
	}
}

func TestDefaultOnExplicitFalseSurvivesSaveLoadReload(t *testing.T) {
	cfg := Default()
	for _, contract := range DefaultOnCapabilityContracts() {
		if err := SetValue(&cfg, contract.Key, "false"); err != nil {
			t.Fatalf("disable %s: %v", contract.Key, err)
		}
	}
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	secretPath := filepath.Join(root, "tunnel.json")
	if err := saveAt(configPath, secretPath, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	assertDefaultOnValues(t, loaded, "false")
	if err := saveAt(configPath, secretPath, loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadAt(configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	assertDefaultOnValues(t, reloaded, "false")
}

func TestDefaultOnAbsentFieldUsesNewDefaultButExplicitFalseWins(t *testing.T) {
	base, err := configformat.Marshal(configformat.JSON, Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range DefaultOnCapabilityContracts() {
		t.Run(strings.ReplaceAll(contract.Key, ".", "_"), func(t *testing.T) {
			for _, test := range []struct {
				name   string
				absent bool
				want   string
			}{
				{name: "absent", absent: true, want: "true"},
				{name: "explicit-false", want: "false"},
			} {
				t.Run(test.name, func(t *testing.T) {
					raw, err := configformat.DecodeGeneric(configformat.JSON, base)
					if err != nil {
						t.Fatal(err)
					}
					root := raw.(map[string]any)
					if test.absent {
						deleteDefaultOnGenericPath(t, root, contract.Key)
					} else {
						setGenericPath(t, root, contract.Key, false)
					}
					data, err := configformat.EncodeGeneric(configformat.JSON, root)
					if err != nil {
						t.Fatal(err)
					}
					dir := t.TempDir()
					configPath := filepath.Join(dir, "config.json")
					if err := os.WriteFile(configPath, data, 0600); err != nil {
						t.Fatal(err)
					}
					loaded, err := loadAt(configPath, filepath.Join(dir, "tunnel.json"))
					if err != nil {
						t.Fatal(err)
					}
					value, err := RawValue(loaded, contract.Key)
					if err != nil {
						t.Fatal(err)
					}
					if value != test.want {
						t.Fatalf("%s = %q, want %q", contract.Key, value, test.want)
					}
				})
			}
		})
	}
}

func assertDefaultOnValues(t *testing.T, cfg Config, want string) {
	t.Helper()
	for _, contract := range DefaultOnCapabilityContracts() {
		value, err := RawValue(cfg, contract.Key)
		if err != nil {
			t.Fatalf("%s: %v", contract.Key, err)
		}
		if value != want {
			t.Fatalf("%s = %q, want %q", contract.Key, value, want)
		}
	}
}

func deleteDefaultOnGenericPath(t *testing.T, root map[string]any, key string) {
	t.Helper()
	parts := strings.Split(key, ".")
	parent := genericParent(t, root, parts[:len(parts)-1])
	delete(parent, parts[len(parts)-1])
}

func setGenericPath(t *testing.T, root map[string]any, key string, value any) {
	t.Helper()
	parts := strings.Split(key, ".")
	parent := genericParent(t, root, parts[:len(parts)-1])
	parent[parts[len(parts)-1]] = value
}

func genericParent(t *testing.T, root map[string]any, parts []string) map[string]any {
	t.Helper()
	current := root
	for _, part := range parts {
		next, ok := current[part].(map[string]any)
		if !ok {
			t.Fatalf("missing generic object %q in %#v", part, current)
		}
		current = next
	}
	return current
}
