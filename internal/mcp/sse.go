package mcp

import (
	"net/http"

	"go.mewis.me/codemcp/internal/runtime/activity"
)

type SSEHandler struct{ Stream *activity.Stream }

func (s SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	activity.Handler(s.Stream).ServeHTTP(w, r)
}
