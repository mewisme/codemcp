package chatgptweb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeTab struct {
	evidence AuthEvidence
	err      error
	url      string
}

func (tab *fakeTab) ID() string { return "fake-tab" }
func (tab *fakeTab) Navigate(_ context.Context, url string) error {
	tab.url = url
	return nil
}
func (tab *fakeTab) Evaluate(_ context.Context, expression string, result any) error {
	if tab.err != nil {
		return tab.err
	}
	if strings.Contains(expression, "payload.user") {
		value, ok := result.(*AuthEvidence)
		if !ok {
			return errors.New("unexpected result type")
		}
		*value = tab.evidence
	}
	return nil
}
func (tab *fakeTab) Done() <-chan struct{} {
	return make(chan struct{})
}
func (tab *fakeTab) Err() error                  { return nil }
func (tab *fakeTab) Close(context.Context) error { return nil }

func TestDOMAuthProbeReturnsBoundedAccountSummaryWithReadiness(t *testing.T) {
	want := AuthEvidence{
		OriginOK: true, TemporaryChat: true, Authenticated: true, Composer: true,
		Account: AccountSummary{Name: "Mew", Email: "mew@example.com"},
	}
	got, err := (DOMAuthProbe{}).Probe(context.Background(), &fakeTab{evidence: want})
	if err != nil || got != want || !got.Ready() {
		t.Fatalf("evidence=%#v err=%v", got, err)
	}
	for _, forbidden := range []string{
		"accessToken", "access_token", "localStorage", "sessionStorage", "document.cookie", "user.id", "user.image",
		"grecaptcha", "turnstile", "captcha", "challenge-platform",
	} {
		if strings.Contains(authEvidenceExpression, forbidden) {
			t.Fatalf("auth probe references sensitive material %q", forbidden)
		}
	}
	for _, required := range []string{
		`fetch("https://chatgpt.com/api/auth/session"`,
		`credentials: "include"`,
		`cache: "no-store"`,
		`redirect: "error"`,
		`headers: { "accept": "application/json" }`,
		`contentType.includes("application/json")`,
		`Object.keys(user).length > 0`,
		`!payload.error`,
		`expiresAt > Date.now()`,
		`typeof user.name === "string"`,
		`typeof user.email === "string"`,
	} {
		if !strings.Contains(authEvidenceExpression, required) {
			t.Fatalf("auth probe missing required session evidence %q", required)
		}
	}
}

func TestDOMAuthProbeDropsUnauthenticatedAccountAndBoundsFields(t *testing.T) {
	longName := strings.Repeat("n", 200)
	longEmail := strings.Repeat("e", 400)
	got, err := (DOMAuthProbe{}).Probe(context.Background(), &fakeTab{evidence: AuthEvidence{
		Account: AccountSummary{Name: longName, Email: longEmail},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Account.Empty() {
		t.Fatalf("unauthenticated probe retained account=%#v", got.Account)
	}

	got, err = (DOMAuthProbe{}).Probe(context.Background(), &fakeTab{evidence: AuthEvidence{
		Authenticated: true,
		Account:       AccountSummary{Name: "  " + longName + "  ", Email: "  " + longEmail + "  "},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(got.Account.Name)) != 160 || len([]rune(got.Account.Email)) != 320 {
		t.Fatalf("bounded account lengths name=%d email=%d", len([]rune(got.Account.Name)), len([]rune(got.Account.Email)))
	}
}

func TestAuthMarkerContainsNoAccountOrSessionMaterial(t *testing.T) {
	root := t.TempDir()
	when := time.Date(2026, 10, 4, 1, 2, 3, 0, time.UTC)
	if err := WriteAuthMarker(root, when); err != nil {
		t.Fatal(err)
	}
	path, _ := AuthMarkerPath(root)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("auth marker permissions=%o want=600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"email", "name", "cookie", "token", "session"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("auth marker leaked forbidden field %q: %s", forbidden, data)
		}
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	if len(generic) != 2 {
		t.Fatalf("auth marker fields=%v", generic)
	}
	marker, ok, err := LoadAuthMarker(root)
	if err != nil || !ok || marker.Version != AuthMarkerVersion || !marker.VerifiedAt.Equal(when) {
		t.Fatalf("marker=%#v ok=%t err=%v", marker, ok, err)
	}
}

func TestInvalidAuthMarkerFailsClosed(t *testing.T) {
	root := t.TempDir()
	path, _ := AuthMarkerPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":99,"verified_at":"2026-10-04T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadAuthMarker(root); err == nil || ok {
		t.Fatalf("invalid marker ok=%t err=%v", ok, err)
	}
}
