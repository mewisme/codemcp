package secretstore

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInspectReportsSecretInventoryWithoutExposingValuesOrMutating(t *testing.T) {
	cleanup := UseMemoryForTesting()
	defer cleanup()

	store := New(t.TempDir())
	configured := AccountName(DomainTunnel, "runtime-key")
	missing := AccountName(DomainAuth, "mcp-token")
	const secret = "raw-secret-value"
	if err := store.Set(configured, secret); err != nil {
		t.Fatal(err)
	}

	diagnostics := store.Inspect([]string{configured, missing, configured, "  "})
	if !diagnostics.Available || diagnostics.Checked != 2 || diagnostics.Configured != 1 || diagnostics.Missing != 1 || diagnostics.Failed != 0 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	data, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("secret value leaked from diagnostics: %s", data)
	}
	if value, err := store.Get(configured); err != nil || value != secret {
		t.Fatalf("inspection mutated secret value=%q err=%v", value, err)
	}
}

func TestInspectUnavailableStoreIsSafe(t *testing.T) {
	var store *Store
	diagnostics := store.Inspect([]string{"one", "two"})
	if diagnostics.Available || diagnostics.Failed != 2 || diagnostics.Configured != 0 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
}
