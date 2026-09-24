package event

import tracepkg "go.mewis.me/codemcp/internal/trace"

func sanitizeValue(key string, value any) any {
	return tracepkg.SanitizeValue(key, value)
}

func sanitizeString(value string) string {
	return tracepkg.SanitizeText(value)
}
