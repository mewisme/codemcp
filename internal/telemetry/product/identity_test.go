package product

import (
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

const testEndpoint = "https://telemetry.mewis.me/v1/products/codemcp/events"

func TestIdentityStoreCreatesOnlyForEligibleTelemetry(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	store := NewIdentityStore()
	store.Root = func() string { return root }
	store.NewID = func() (string, error) { return "123e4567-e89b-42d3-a456-426614174000", nil }

	for _, test := range []struct {
		enabled  bool
		endpoint string
	}{
		{false, testEndpoint},
		{true, ""},
	} {
		id, created, err := store.Ensure(test.enabled, test.endpoint)
		if err != nil || id != "" || created {
			t.Fatalf("ineligible ensure=(%q,%t,%v)", id, created, err)
		}
		if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
			t.Fatalf("ineligible ensure created identity: %v", err)
		}
	}

	id, created, err := store.Ensure(true, testEndpoint)
	if err != nil || id == "" || !created {
		t.Fatalf("eligible ensure=(%q,%t,%v)", id, created, err)
	}
	info, err := os.Stat(store.Path())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("identity state mode=%v err=%v", info, err)
	}
	again, created, err := store.Ensure(true, testEndpoint)
	if err != nil || again != id || created {
		t.Fatalf("existing ensure=(%q,%t,%v)", again, created, err)
	}
}

func TestIdentityStoreRecoversMalformedStateOnlyWhenEligible(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	store := NewIdentityStore()
	store.Root = func() string { return root }
	store.NewID = func() (string, error) { return "123e4567-e89b-42d3-a456-426614174001", nil }
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(store.Path())
	if _, created, err := store.Ensure(false, testEndpoint); err != nil || created {
		t.Fatalf("disabled malformed ensure created=%t err=%v", created, err)
	}
	after, _ := os.ReadFile(store.Path())
	if string(before) != string(after) {
		t.Fatal("disabled path rewrote malformed identity state")
	}
	id, created, err := store.Ensure(true, testEndpoint)
	if err != nil || !created || id == "" {
		t.Fatalf("eligible recovery=(%q,%t,%v)", id, created, err)
	}
	if got, ok := store.Read(); !ok || got != id {
		t.Fatalf("recovered identity=(%q,%t)", got, ok)
	}
}

func TestEndpointMetadataIsSanitizedAndSourceDefaultIsEmpty(t *testing.T) {
	if Endpoint != "" {
		t.Fatalf("source endpoint must be empty, got %q", Endpoint)
	}
	meta, err := ParseEndpoint(testEndpoint)
	if err != nil || !meta.Available || meta.Host != "telemetry.mewis.me" || meta.Product != "codemcp" {
		t.Fatalf("endpoint metadata=%#v err=%v", meta, err)
	}
	for _, invalid := range []string{
		"http://telemetry.mewis.me/v1/products/codemcp/events",
		"https://user:pass@telemetry.mewis.me/v1/products/codemcp/events",
		"https://telemetry.mewis.me/v1/products/other/events",
		"https://telemetry.mewis.me/v1/products/codemcp/events?secret=x",
	} {
		if _, err := ParseEndpoint(invalid); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", invalid)
		}
	}
}
