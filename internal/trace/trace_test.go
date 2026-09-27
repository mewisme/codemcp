package trace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSpanSuccessIncludesDuration(t *testing.T) {
	events := []Event{}
	ctx := WithObserver(context.Background(), func(event Event) { events = append(events, event) })
	span := Start(ctx, "UPDATE", "update.release.request", "Fetching release metadata", String("method", "GET"))
	span.End(Int("status", 200))
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Name != "update.release.request.started" || events[1].Name != "update.release.request.completed" {
		t.Fatalf("unexpected event names: %q %q", events[0].Name, events[1].Name)
	}
	if _, ok := fieldValue(events[1], "duration_ms"); !ok {
		t.Fatal("completion event missing duration_ms")
	}
}

func TestSpanFailureIncludesDurationAndError(t *testing.T) {
	events := []Event{}
	span := StartObserver(func(event Event) { events = append(events, event) }, "UPDATE", "update.download", "Downloading release")
	span.Fail(errors.New("network failed"))
	if len(events) != 2 || events[1].Phase != PhaseError {
		t.Fatalf("unexpected events: %#v", events)
	}
	if value, ok := fieldValue(events[1], "error"); !ok || value != "network failed" {
		t.Fatalf("unexpected error field: %v %t", value, ok)
	}
	if _, ok := fieldValue(events[1], "duration_ms"); !ok {
		t.Fatal("failure event missing duration_ms")
	}
}

func TestNoopObserverIsSafe(t *testing.T) {
	span := Start(context.Background(), "TEST", "noop", "No-op")
	span.End()
	Emit(context.Background(), "TEST", "noop.info", "No-op")
}

func TestWithoutObserverShadowsParentObserver(t *testing.T) {
	events := []Event{}
	parent := WithObserver(context.Background(), func(event Event) { events = append(events, event) })
	quiet := WithoutObserver(parent)
	if ObserverFromContext(parent) == nil {
		t.Fatal("parent observer missing")
	}
	if ObserverFromContext(quiet) != nil {
		t.Fatal("observer was not suppressed")
	}
	Emit(quiet, "TEST", "quiet", "Quiet event")
	if len(events) != 0 {
		t.Fatalf("suppressed context emitted %d events", len(events))
	}
}

func TestSensitiveFieldsAreRedacted(t *testing.T) {
	events := []Event{}
	EmitObserver(func(event Event) { events = append(events, event) }, "AUTH", "auth.test", "test", String("access_token", "secret-value"), String("name", "safe"))
	if value, _ := fieldValue(events[0], "access_token"); value != "configured" {
		t.Fatalf("secret was not redacted: %v", value)
	}
	if value, _ := fieldValue(events[0], "name"); value != "safe" {
		t.Fatalf("safe value changed: %v", value)
	}
}

func TestSanitizeURL(t *testing.T) {
	value := SanitizeURL("https://user:pass@example.com/file?token=abc&x=1&X-Amz-Signature=secret")
	if value != "https://example.com/file?X-Amz-Signature=%3Credacted%3E&token=%3Credacted%3E&x=1" {
		t.Fatalf("unexpected sanitized URL: %s", value)
	}
}

func TestURLFieldSanitizesCredentialQuery(t *testing.T) {
	events := []Event{}
	EmitObserver(func(event Event) { events = append(events, event) }, "HTTP", "http.test", "test", URL("url", "https://example.com/callback?access_token=abc&state=def&safe=1"))
	value, _ := fieldValue(events[0], "url")
	if value == "https://example.com/callback?access_token=abc&state=def&safe=1" {
		t.Fatalf("URL was not sanitized: %v", value)
	}
}

func TestMaskSecretDoesNotRevealShortSecrets(t *testing.T) {
	for secret, want := range map[string]string{
		"a":               "…********…",
		"short123":        "s********3",
		"123456789012345": "1********5",
	} {
		if masked := MaskSecret(secret, true); masked != want || strings.Contains(masked, secret) {
			t.Fatalf("short secret %q masked as %q want %q", secret, masked, want)
		}
	}
	if got := MaskSecret("", false); got != "not configured" {
		t.Fatalf("unconfigured mask=%q", got)
	}
	if got := MaskSecret("abcdefghijklmnop", true); got != "ab********op" {
		t.Fatalf("medium mask=%q", got)
	}
}

func TestSanitizeArgsAndCommandRedactCredentialArguments(t *testing.T) {
	const secret = "super-secret-marker"
	args := []string{
		"config", "set", "tunnel.api_key", secret,
		"--header", "Authorization=Bearer " + secret,
		"--env", "API_TOKEN=" + secret,
		"https://example.test/mcp?token=" + secret + "&safe=1",
	}
	safe := SanitizeArgs(args)
	joined := strings.Join(safe, " ")
	if strings.Contains(joined, secret) {
		t.Fatalf("sanitized args leaked secret: %#v", safe)
	}
	for _, want := range []string{"tunnel.api_key", redactedValue, "Authorization=<redacted>", "API_TOKEN=<redacted>", "safe=1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sanitized args missing %q: %#v", want, safe)
		}
	}

	command := "cm config set tunnel.api_key \"super-secret-marker with spaces\""
	safeCommand := SanitizeCommand(command)
	if strings.Contains(safeCommand, "super-secret-marker") || !strings.Contains(safeCommand, redactedValue) {
		t.Fatalf("sanitized command=%q", safeCommand)
	}
	if got := SanitizeCommand("printf 'safe command'"); got != "printf 'safe command'" {
		t.Fatalf("non-sensitive command changed: %q", got)
	}
}

func TestTraceNormalizesCommandAndArgsFields(t *testing.T) {
	const secret = "trace-secret-marker"
	events := []Event{}
	EmitObserver(func(event Event) { events = append(events, event) }, "SHELL", "shell.test", "test",
		String("command", "cm config set tunnel.api_key "+secret),
		Any("args", []string{"--api-key", secret}),
	)
	data := fmt.Sprint(events)
	if strings.Contains(data, secret) {
		t.Fatalf("trace leaked secret: %s", data)
	}
}

func fieldValue(event Event, key string) (any, bool) {
	for _, field := range event.Fields {
		if field.Key == key {
			return field.Value, true
		}
	}
	return nil, false
}
