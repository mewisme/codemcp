package auth

import (
	"errors"
	"testing"

	"go.mewis.me/codemcp/internal/secretstore"
)

func TestAuthTokenSecretStoreLifecycle(t *testing.T) {
	restore := secretstore.UseMemoryForTesting()
	defer restore()

	root := t.TempDir()
	for _, kind := range []string{"mcp", "admin"} {
		configured, err := TokenConfigured(root, kind)
		if err != nil || configured {
			t.Fatalf("%s initial configured=%t err=%v", kind, configured, err)
		}
		token := kind + "_secret_token_value"
		if err := StoreToken(root, kind, token); err != nil {
			t.Fatal(err)
		}
		got, err := LoadToken(root, kind)
		if err != nil || got != token {
			t.Fatalf("%s token=%q err=%v", kind, got, err)
		}
		configured, err = TokenConfigured(root, kind)
		if err != nil || !configured {
			t.Fatalf("%s configured=%t err=%v", kind, configured, err)
		}
		if err := StoreToken(root, kind, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadToken(root, kind); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatalf("%s cleared err=%v", kind, err)
		}
	}
}

func TestAuthTokenSecretInventoryIsStable(t *testing.T) {
	got := SecretEntries()
	want := []string{MCPTokenSecretName, AdminTokenSecretName}
	if len(got) != len(want) {
		t.Fatalf("entries=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries=%v want=%v", got, want)
		}
	}
	for _, kind := range []string{"", "missing"} {
		if _, err := LoadToken(t.TempDir(), kind); err == nil {
			t.Fatalf("invalid kind %q accepted", kind)
		}
	}
}
