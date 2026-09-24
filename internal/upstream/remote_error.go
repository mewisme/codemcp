package upstream

import (
	"strings"
	"unicode/utf8"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const maxRemoteErrorBytes = 64 * 1024

type remoteError struct {
	cause   error
	message string
}

func (err *remoteError) Error() string {
	if err == nil {
		return ""
	}
	return err.message
}

func (err *remoteError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func sanitizeRemoteError(server Server, err error) error {
	if err == nil {
		return nil
	}
	text := tracepkg.SanitizeError(err)
	for key, value := range server.Headers {
		if SensitiveConfigKey(key) {
			text = redactConfiguredValue(text, value)
		}
	}
	for key, value := range server.Env {
		if SensitiveConfigKey(key) {
			text = redactConfiguredValue(text, value)
		}
	}
	text = boundRemoteErrorText(text)
	if text == err.Error() {
		return err
	}
	return &remoteError{cause: err, message: text}
}

func redactConfiguredValue(text, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return text
	}
	text = strings.ReplaceAll(text, value, "<redacted>")
	fields := strings.Fields(value)
	if len(fields) > 1 {
		for _, field := range fields[1:] {
			if len(field) >= 4 {
				text = strings.ReplaceAll(text, field, "<redacted>")
			}
		}
	}
	return text
}

func boundRemoteErrorText(text string) string {
	if len(text) <= maxRemoteErrorBytes {
		return text
	}
	const suffix = "... [remote error truncated]"
	limit := maxRemoteErrorBytes - len(suffix)
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit] + suffix
}
