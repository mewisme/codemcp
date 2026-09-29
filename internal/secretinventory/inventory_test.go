package secretinventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/auth"
	"go.mewis.me/codemcp/internal/configformat"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/oauth"
	"go.mewis.me/codemcp/internal/secretstore"
	telegramcredential "go.mewis.me/codemcp/internal/telegram/credential"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestInventoryCoversManagedSecretFamiliesWithoutValues(t *testing.T) {
	root := t.TempDir()
	if err := auth.StoreTokens(root, "mcp-secret-value", "admin-secret-value"); err != nil {
		t.Fatal(err)
	}
	tunnelRuntime := secretstore.AccountName(secretstore.DomainTunnel, "runtime-key")
	tunnelAdmin := secretstore.AccountName(secretstore.DomainTunnel, "admin-key")
	if err := secretstore.New(root).Apply([]secretstore.Change{
		{Name: tunnelRuntime, Value: "tunnel-runtime-secret"},
		{Name: tunnelAdmin, Value: "tunnel-admin-secret"},
		{Name: secretstore.AccountName(secretstore.DomainCluster, "relay-token"), Value: "relay-secret"},
		{Name: telegramcredential.BotTokenSecretName, Value: "telegram-secret"},
		{Name: typesafeintegration.APIKeySecretName, Value: "typesafe-secret"},
		{Name: mustLLMCredentialAccount(t, "openrouter"), Value: "llm-secret"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tunnel.json"), []byte("{\"version\":1,\"runtime_key_configured\":true,\"admin\":{\"key_configured\":true}}"), 0600); err != nil {
		t.Fatal(err)
	}

	oauthStore := oauth.NewStore(configformat.StructuredPath(root, "oauth"))
	if err := oauthStore.Put(oauth.Credential{ServerID: "oauth_one", ClientID: "client", ClientSecret: "oauth-client-secret", AccessToken: "oauth-access-secret", RefreshToken: "oauth-refresh-secret"}); err != nil {
		t.Fatal(err)
	}
	upstreamStore := upstream.NewStore(configformat.StructuredPath(root, "upstreams"))
	if err := upstreamStore.Save([]upstream.Server{{ID: "up_one", Name: "one", Transport: "stdio", Command: "echo", Enabled: true, Env: map[string]string{"API_TOKEN": "upstream-secret"}}}); err != nil {
		t.Fatal(err)
	}

	inventory, err := Inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	byDomain := map[secretstore.Domain]int{}
	for _, descriptor := range inventory {
		byDomain[descriptor.Domain]++
		if descriptor.PortablePolicy != PortableExcluded {
			t.Fatalf("unexpected portable policy: %#v", descriptor)
		}
	}
	for _, domain := range []secretstore.Domain{secretstore.DomainAuth, secretstore.DomainTunnel, secretstore.DomainOAuth, secretstore.DomainUpstream, secretstore.DomainCluster, secretstore.DomainTelegram, secretstore.DomainTypeSafe, secretstore.DomainLLM} {
		if byDomain[domain] == 0 {
			t.Errorf("managed secret domain missing from inventory: %s", domain)
		}
	}
	encoded, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"mcp-secret-value", "admin-secret-value", "tunnel-runtime-secret", "tunnel-admin-secret", "oauth-client-secret", "oauth-access-secret", "oauth-refresh-secret", "upstream-secret", "relay-secret", "telegram-secret", "typesafe-secret", "llm-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("inventory leaked raw secret %q: %s", secret, encoded)
		}
	}
}

func TestRecognizedAccountUsesCanonicalRegistrationBoundaries(t *testing.T) {
	known := []string{
		auth.SecretEntries()[0],
		secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"),
		secretstore.AccountName(secretstore.DomainOAuth, "server", "access-token"),
		secretstore.AccountName(secretstore.DomainUpstream, "server", "header", "Authorization"),
		secretstore.AccountName(secretstore.DomainCluster, "relay-token"),
		telegramcredential.BotTokenSecretName,
		typesafeintegration.APIKeySecretName,
		mustLLMCredentialAccount(t, "openrouter"),
	}
	for _, account := range known {
		if !RecognizedAccount(account) {
			t.Errorf("known managed account was not recognized: %s", account)
		}
	}
	for _, account := range []string{secretstore.AccountName(secretstore.DomainCluster, "unknown"), secretstore.AccountName(secretstore.DomainAuth, "unknown"), secretstore.Name("other", "credential")} {
		if RecognizedAccount(account) {
			t.Errorf("unknown account was recognized: %s", account)
		}
	}
}

func mustLLMCredentialAccount(t *testing.T, id string) string {
	t.Helper()
	account, err := llm.CredentialAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	return account
}
