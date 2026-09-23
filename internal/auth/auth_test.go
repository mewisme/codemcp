package auth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestTokenHashRoundTrip(t *testing.T) {
	token := GenerateToken("test")
	first := HashToken(token)
	second := HashToken(token)
	if first != second {
		t.Fatal("random bearer token hashes should be deterministic")
	}
	if !VerifyToken(token, first) {
		t.Fatal("expected token verification to succeed")
	}
	if VerifyToken(token+"x", first) {
		t.Fatal("expected invalid token verification to fail")
	}
}

func TestHashedMiddlewareRequiresConfiguredToken(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	HashedMiddleware(true, "", next).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}

	token := GenerateToken("test")
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	HashedMiddleware(true, HashToken(token), next).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
}

func TestHashedMiddlewareBypassesAuthenticationWhenDisabled(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	HashedMiddleware(false, "configured-but-unused", next).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected auth-disabled request to pass, got %d", recorder.Code)
	}
}

func TestDynamicHashedMiddlewareReadsCurrentSettingsPerRequest(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	enabled := true
	token := GenerateToken("test")
	tokenHash := HashToken(token)
	handler := DynamicHashedMiddleware(func() (bool, string) { return enabled, tokenHash }, next)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("enabled auth status = %d", recorder.Code)
	}

	enabled = false
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("updated auth status = %d", recorder.Code)
	}
}

func TestMiddlewareAndTokenSources(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, test := range []struct {
		name, configured, header, fallback string
		want                               int
	}{
		{name: "disabled", want: http.StatusNoContent},
		{name: "authorization", configured: "secret", header: "secret", want: http.StatusNoContent},
		{name: "fallback", configured: "secret", fallback: "secret", want: http.StatusNoContent},
		{name: "invalid", configured: "secret", header: "wrong", want: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.header != "" {
				request.Header.Set("Authorization", "Bearer "+test.header)
			}
			if test.fallback != "" {
				request.Header.Set("X-MCP-Token", test.fallback)
			}
			recorder := httptest.NewRecorder()
			Middleware(test.configured, next).ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
		})
	}
}

func TestVerifyTokenArgon2ID(t *testing.T) {
	token := "secret-token"
	if VerifyToken(token, base64.RawStdEncoding.EncodeToString([]byte("legacy-hash"))) {
		t.Fatal("unversioned legacy token hash was accepted")
	}
	salt := []byte("12345678")
	hash := argon2.IDKey([]byte(token), salt, 2, 64*1024, 2, 32)
	encoded := "argon2id$v=19$m=65536,t=2,p=2$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)
	if !VerifyToken(token, encoded) || VerifyToken(token+"x", encoded) {
		t.Fatal("argon2id verification mismatch")
	}
	for _, malformed := range []string{
		"sha256$bad",
		"argon2id$v=18$m=65536,t=2,p=2$MTIzNDU2Nzg$YWJj",
		"argon2id$v=19$m=0,t=2,p=2$MTIzNDU2Nzg$YWJj",
		"argon2id$v=19$m=65536,t=0,p=2$MTIzNDU2Nzg$YWJj",
		"argon2id$v=19$m=65536,t=2,p=0$MTIzNDU2Nzg$YWJj",
		"argon2id$v=19$m=65536,t=2,p=2$bad$YWJj",
		"argon2id$v=19$m=65536,t=2,p=2$MTIzNDU2Nzg$",
	} {
		if VerifyToken(token, malformed) {
			t.Fatalf("accepted malformed hash %q", malformed)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "bearer lower")
	request.Header.Set("X-MCP-Token", "fallback")
	if got := TokenFromRequest(request); got != "fallback" {
		t.Fatalf("fallback token = %q", got)
	}
	recorder := httptest.NewRecorder()
	unauthorized(recorder)
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("unauthorized response = %d %#v", recorder.Code, recorder.Header())
	}
}
