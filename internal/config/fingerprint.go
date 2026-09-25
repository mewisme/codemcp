package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func RuntimeFingerprint(cfg Config) (string, error) {
	safe := cfg
	safe.Auth.MCPTokenHash = ""
	safe.Auth.AdminTokenHash = ""
	safe.Tunnel.APIKey = ""
	safe.Tunnel.Admin.Key = ""
	data, err := json.Marshal(safe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
