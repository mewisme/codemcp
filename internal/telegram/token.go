package telegram

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/secretstore"
)

var botTokenSecretName = secretstore.Name("telegram", "bot-token")

func SetToken(root, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("telegram bot token is required")
	}
	return secretstore.New(root).Set(botTokenSecretName, token)
}

func ClearToken(root string) error {
	return secretstore.New(root).Set(botTokenSecretName, "")
}

func TokenConfigured(root string) (bool, error) {
	_, err := secretstore.New(root).Get(botTokenSecretName)
	if errors.Is(err, secretstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func loadToken(root string) (string, error) {
	return secretstore.New(root).Get(botTokenSecretName)
}
