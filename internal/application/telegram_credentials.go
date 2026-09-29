package application

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/secretstore"
	telegramcredential "go.mewis.me/codemcp/internal/telegram/credential"
)

var telegramBotTokenSecretName = telegramcredential.BotTokenSecretName

func telegramTokenConfigured() (bool, error) {
	_, err := secretstore.New(config.RootPath()).Get(telegramBotTokenSecretName)
	if errors.Is(err, secretstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func readTelegramToken() (string, error) {
	return secretstore.New(config.RootPath()).Get(telegramBotTokenSecretName)
}

func writeTelegramToken(value string) error {
	return secretstore.New(config.RootPath()).Set(telegramBotTokenSecretName, strings.TrimSpace(value))
}
