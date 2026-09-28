package tunnel

import (
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const maxPublicDiagnosticRunes = 1024

// PublicStatus returns the operator-visible tunnel status while preserving
// stable lifecycle, identity, scope, and metadata fields.
func PublicStatus(value Status) Status {
	value.LastError = publicDiagnostic(value.LastError)
	value.MetadataError = publicDiagnostic(value.MetadataError)
	return value
}

// SecretPreview returns the canonical operator-facing representation for a
// configured credential without exposing the credential itself.
func SecretPreview(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "not configured"
	}
	return tracepkg.MaskSecret(raw, true)
}

func publicDiagnostic(value string) string {
	value = strings.TrimSpace(tracepkg.SanitizeText(value))
	runes := []rune(value)
	if len(runes) <= maxPublicDiagnosticRunes {
		return value
	}
	return string(runes[:maxPublicDiagnosticRunes-3]) + "..."
}
