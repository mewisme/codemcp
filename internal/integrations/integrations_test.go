package integrations

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultActivatesFirstPartyIntegrations(t *testing.T) {
	value := Default()
	if !value.Ponytail.Active || value.Ponytail.Mode != "full" || !value.Caveman.Active || value.Caveman.Mode != "full" || !value.RTK.Enabled || value.RTK.Path != "" || !value.CodeGraph.Enabled || value.CodeGraph.Path != "" || !value.TypeSafe.Enabled || value.TypeSafe.Model != "jev-latest" || value.TypeSafe.TimeoutMS != 3000 {
		t.Fatalf("default integrations = %#v", value)
	}
}

func TestCanonicalIntegrationIdentityAndOwner(t *testing.T) {
	for _, test := range []struct {
		id   ID
		name string
	}{
		{PonytailID, "Ponytail"},
		{CavemanID, "Caveman"},
		{RTKID, "RTK"},
		{CodeGraphID, "CodeGraph"},
	} {
		identity, ok := IdentityFor(test.id)
		if !ok || identity.ID != test.id || identity.Name != test.name {
			t.Fatalf("identity %q = %#v, %v", test.id, identity, ok)
		}
		if got := Owner(test.id); got != "integration:"+string(test.id) {
			t.Fatalf("owner %q = %q", test.id, got)
		}
		parsed, err := ParseID(string(test.id))
		if err != nil || parsed != test.id {
			t.Fatalf("parse %q = %q, %v", test.id, parsed, err)
		}
	}
	if _, err := ParseID("plugin"); err == nil {
		t.Fatal("unknown integration id accepted")
	}
}

func TestFirstPartyIntegrationsHaveOneCanonicalOwnershipRoot(t *testing.T) {
	for _, legacy := range []string{"features", "builtins", "ponytail", "caveman"} {
		_, err := os.Stat(filepath.Join("..", legacy))
		if err == nil {
			t.Fatalf("legacy first-party ownership root still exists: internal/%s", legacy)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat internal/%s: %v", legacy, err)
		}
	}
}
