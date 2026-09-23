package event

import (
	"regexp"
	"strings"
)

var (
	secretTokenPattern = regexp.MustCompile(`(?i)\b(?:mcp|admin|runtime)_[A-Za-z0-9_-]+\b`)
	bearerPattern      = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;]+`)
)

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, part := range []string{"authorization", "api_key", "apikey", "password", "secret", "token", "hash"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func sanitizeValue(key string, value any) any {
	if sensitiveKey(key) {
		return "<redacted>"
	}
	switch typed := value.(type) {
	case string:
		return sanitizeString(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = sanitizeString(item)
		}
		return out
	case error:
		return sanitizeString(typed.Error())
	default:
		return value
	}
}

func sanitizeString(value string) string {
	value = bearerPattern.ReplaceAllString(value, `${1}<redacted>`)
	return secretTokenPattern.ReplaceAllString(value, `<redacted>`)
}
