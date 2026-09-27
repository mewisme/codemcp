package auth

import (
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/secretstore"
)

var (
	MCPTokenSecretName   = secretstore.AccountName(secretstore.DomainAuth, "mcp-token")
	AdminTokenSecretName = secretstore.AccountName(secretstore.DomainAuth, "admin-token")
)

func SecretEntries() []string {
	return []string{MCPTokenSecretName, AdminTokenSecretName}
}

func LoadToken(root, kind string) (string, error) {
	name, err := tokenSecretName(kind)
	if err != nil {
		return "", err
	}
	return secretstore.New(root).Get(name)
}

func StoreToken(root, kind, token string) error {
	name, err := tokenSecretName(kind)
	if err != nil {
		return err
	}
	return secretstore.New(root).Set(name, strings.TrimSpace(token))
}

func StoreTokens(root, mcpToken, adminToken string) error {
	return secretstore.New(root).Apply([]secretstore.Change{
		{Name: MCPTokenSecretName, Value: strings.TrimSpace(mcpToken)},
		{Name: AdminTokenSecretName, Value: strings.TrimSpace(adminToken)},
	})
}

func TokenConfigured(root, kind string) (bool, error) {
	_, err := LoadToken(root, kind)
	if errors.Is(err, secretstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func tokenSecretName(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "mcp":
		return MCPTokenSecretName, nil
	case "admin":
		return AdminTokenSecretName, nil
	default:
		return "", fmt.Errorf("unsupported auth kind: %s", kind)
	}
}
