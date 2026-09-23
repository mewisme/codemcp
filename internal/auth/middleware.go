package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

func Middleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || ValidateRequestToken(r, token) {
			next.ServeHTTP(w, r)
			return
		}
		unauthorized(w)
	})
}

func HashedMiddleware(enabled bool, tokenHash string, next http.Handler) http.Handler {
	return DynamicHashedMiddleware(func() (bool, string) { return enabled, tokenHash }, next)
}

func DynamicHashedMiddleware(settings func() (bool, string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enabled, tokenHash := settings()
		if !enabled {
			next.ServeHTTP(w, r)
			return
		}
		if tokenHash != "" && ValidateRequestHash(r, tokenHash) {
			next.ServeHTTP(w, r)
			return
		}
		unauthorized(w)
	})
}

func ValidateRequestToken(r *http.Request, expected string) bool {
	actualHash := sha256.Sum256([]byte(TokenFromRequest(r)))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(actualHash[:], expectedHash[:]) == 1
}

func ValidateRequestHash(r *http.Request, expectedHash string) bool {
	token := TokenFromRequest(r)
	return token != "" && VerifyToken(token, expectedHash)
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="codemcp"`)
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}
