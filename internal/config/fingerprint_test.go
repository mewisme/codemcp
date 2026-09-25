package config

import "testing"

func TestRuntimeFingerprintIgnoresSecretsButTracksConfig(t *testing.T) {
	cfg := Default()
	base, err := RuntimeFingerprint(cfg)
	if err != nil {
		t.Fatal(err)
	}
	secret := cfg
	secret.Auth.MCPTokenHash = "mcp-secret"
	secret.Auth.AdminTokenHash = "admin-secret"
	secret.Tunnel.APIKey = "runtime-secret"
	secret.Tunnel.Admin.Key = "tunnel-admin-secret"
	withSecrets, err := RuntimeFingerprint(secret)
	if err != nil {
		t.Fatal(err)
	}
	if withSecrets != base {
		t.Fatalf("secret-only change altered fingerprint: %q != %q", withSecrets, base)
	}
	changed := cfg
	changed.Server.Port++
	different, err := RuntimeFingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if different == base {
		t.Fatal("config mutation did not alter fingerprint")
	}
}
