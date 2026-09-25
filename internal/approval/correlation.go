package approval

import (
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/idgen"
)

type CallerRegistry struct {
	mu      sync.Mutex
	callers map[string]string
}

func NewCallerRegistry() *CallerRegistry {
	return &CallerRegistry{callers: map[string]string{}}
}

func (r *CallerRegistry) Caller(transportKey string) string {
	if r == nil {
		return ""
	}
	transportKey = strings.TrimSpace(transportKey)
	if transportKey == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if callerID := r.callers[transportKey]; callerID != "" {
		return callerID
	}
	callerID := idgen.Must("apc", 8)
	r.callers[transportKey] = callerID
	return callerID
}
