package tunnel

import (
	"strings"
	"testing"
)

func TestSecretPreviewUsesMaskedConfiguredRepresentation(t *testing.T) {
	const secret = "admin-secret-value"
	preview := SecretPreview(secret)
	if preview == "" || preview == "configured" || preview == "********" || strings.Contains(preview, secret) {
		t.Fatalf("preview=%q", preview)
	}
	if !strings.HasPrefix(preview, "ad") || !strings.HasSuffix(preview, "ue") {
		t.Fatalf("preview did not preserve bounded prefix/suffix: %q", preview)
	}
	if got := SecretPreview(""); got != "not configured" {
		t.Fatalf("empty preview=%q", got)
	}
}

func TestPublicStatusSanitizesAndBoundsDiagnostics(t *testing.T) {
	const secret = "status-secret-marker"
	long := strings.Repeat("diagnostic-", maxPublicDiagnosticRunes)
	status := PublicStatus(Status{
		ID:            "tunnel_1",
		LastError:     long,
		MetadataError: "token=" + secret,
	})
	if status.ID != "tunnel_1" {
		t.Fatalf("stable tunnel identity changed: %#v", status)
	}
	if strings.Contains(status.LastError, secret) || strings.Contains(status.MetadataError, secret) {
		t.Fatalf("public tunnel status leaked secret: %#v", status)
	}
	if len([]rune(status.LastError)) > maxPublicDiagnosticRunes {
		t.Fatalf("last error was not bounded: %d", len([]rune(status.LastError)))
	}
	if !strings.HasSuffix(status.LastError, "...") {
		t.Fatalf("bounded diagnostic missing truncation marker: %q", status.LastError)
	}
}
