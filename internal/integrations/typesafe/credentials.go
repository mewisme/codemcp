package typesafe

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/secretstore"
)

var APIKeySecretName = secretstore.AccountName(secretstore.DomainTypeSafe, "api-key")

func SecretEntries() []string {
	return []string{APIKeySecretName}
}

type CredentialStatus struct {
	Configured bool `json:"configured"`
}

func Credential(root string) (CredentialStatus, error) {
	_, err := secretstore.New(root).Get(APIKeySecretName)
	switch {
	case err == nil:
		return CredentialStatus{Configured: true}, nil
	case errors.Is(err, secretstore.ErrNotFound):
		return CredentialStatus{}, nil
	default:
		return CredentialStatus{}, err
	}
}

func LoadAPIKey(root string) (string, error) {
	return secretstore.New(root).Get(APIKeySecretName)
}

func UpdateAPIKey(root, value string) error {
	return secretstore.New(root).Set(APIKeySecretName, strings.TrimSpace(value))
}
