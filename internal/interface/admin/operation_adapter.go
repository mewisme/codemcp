package admin

import (
	"context"
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

const CanonicalOperationHeader = "X-CM-Operation-ID"

type operationContextKey struct{}

func withCanonicalOperation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		operation, ok := capability.ForAdminRequest(r.Method, r.URL.Path)
		declared := capability.ID(strings.TrimSpace(r.Header.Get(CanonicalOperationHeader)))
		if declared != "" {
			if !ok || declared != operation {
				http.Error(w, "browser operation does not match canonical Admin route", http.StatusBadRequest)
				return
			}
		}
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set(CanonicalOperationHeader, string(operation))
		ctx := context.WithValue(r.Context(), operationContextKey{}, operation)
		ctx = application.WithOperationInterface(ctx, application.OperationInterfaceAdmin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func CanonicalOperation(r *http.Request) (capability.ID, bool) {
	if r == nil {
		return "", false
	}
	id, ok := r.Context().Value(operationContextKey{}).(capability.ID)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}
