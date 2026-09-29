package credential

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/secretstore"
)

var BotTokenSecretName = secretstore.AccountName(secretstore.DomainTelegram, "bot-token")

func SecretEntries() []string {
	return []string{BotTokenSecretName}
}

func Set(root, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("telegram bot token is required")
	}
	return secretstore.New(root).Set(BotTokenSecretName, token)
}

func Clear(root string) error {
	return secretstore.New(root).Set(BotTokenSecretName, "")
}

func Configured(root string) (bool, error) {
	_, err := secretstore.New(root).Get(BotTokenSecretName)
	if errors.Is(err, secretstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func Load(root string) (string, error) {
	return secretstore.New(root).Get(BotTokenSecretName)
}
