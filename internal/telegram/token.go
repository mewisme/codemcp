package telegram

import (
	telegramcredential "go.mewis.me/codemcp/internal/telegram/credential"
)

var BotTokenSecretName = telegramcredential.BotTokenSecretName

func SecretEntries() []string {
	return telegramcredential.SecretEntries()
}

func SetToken(root, token string) error {
	return telegramcredential.Set(root, token)
}

func ClearToken(root string) error {
	return telegramcredential.Clear(root)
}

func TokenConfigured(root string) (bool, error) {
	return telegramcredential.Configured(root)
}

func loadToken(root string) (string, error) {
	return telegramcredential.Load(root)
}
