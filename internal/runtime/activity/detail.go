package activity

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	DiagnosticMaxDepth       = 6
	DiagnosticMaxMapKeys     = 64
	DiagnosticMaxListItems   = 64
	DiagnosticMaxStringBytes = 4096
	DiagnosticMaxTotalBytes  = 64 << 10
)

var (
	diagnosticBearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s",}]+`)
	diagnosticSecretPattern = regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|password|authorization|cookie)\s*[=:]\s*)[^\s&,;"'}]+`)
)

type DiagnosticMeta struct {
	Redacted  bool `json:"redacted,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
}

// ToolCallDetail is the bounded, redacted diagnostic projection for one tool
// call. Live activity feeds intentionally omit provider payloads; callers that
// need request/response detail must resolve it through the owning Stream.
type ToolCallDetail struct {
	Event
	Request    any            `json:"request,omitempty"`
	Response   any            `json:"response,omitempty"`
	Error      any            `json:"error,omitempty"`
	Diagnostic DiagnosticMeta `json:"diagnostic,omitempty"`
}

func SanitizeDiagnostic(value any) (any, DiagnosticMeta) {
	if value == nil {
		return nil, DiagnosticMeta{}
	}
	var normalized any
	data, err := json.Marshal(value)
	if err != nil || json.Unmarshal(data, &normalized) != nil {
		normalized = fmt.Sprint(value)
	}
	meta := DiagnosticMeta{}
	budget := DiagnosticMaxTotalBytes
	result := sanitizeDiagnosticValue("", normalized, 0, &budget, &meta)
	if encoded, err := json.Marshal(result); err == nil && len(encoded) > DiagnosticMaxTotalBytes {
		meta.Truncated = true
		result = map[string]any{"truncated": true, "encoded_bytes": len(encoded)}
	}
	return result, meta
}

func sanitizeDiagnosticValue(key string, value any, depth int, budget *int, meta *DiagnosticMeta) any {
	if budget == nil || *budget <= 0 {
		meta.Truncated = true
		return "<truncated>"
	}
	if depth > DiagnosticMaxDepth {
		meta.Truncated = true
		return "<truncated>"
	}
	if diagnosticSecretKey(key) {
		meta.Redacted = true
		consumeDiagnosticBudget(budget, len("<redacted>"), meta)
		return "<redacted>"
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for child := range typed {
			keys = append(keys, child)
		}
		sort.Strings(keys)
		if len(keys) > DiagnosticMaxMapKeys {
			keys = keys[:DiagnosticMaxMapKeys]
			meta.Truncated = true
		}
		result := make(map[string]any, len(keys))
		for _, child := range keys {
			result[child] = sanitizeDiagnosticValue(child, typed[child], depth+1, budget, meta)
		}
		return result
	case []any:
		limit := len(typed)
		if limit > DiagnosticMaxListItems {
			limit = DiagnosticMaxListItems
			meta.Truncated = true
		}
		result := make([]any, 0, limit)
		for index := 0; index < limit; index++ {
			result = append(result, sanitizeDiagnosticValue(key, typed[index], depth+1, budget, meta))
		}
		return result
	case string:
		value := fmt.Sprint(tracepkg.SanitizeValue(key, typed))
		if value != typed {
			meta.Redacted = true
		}
		value = sanitizeDiagnosticString(value, meta)
		if len(value) > DiagnosticMaxStringBytes {
			value = value[:DiagnosticMaxStringBytes] + "<truncated>"
			meta.Truncated = true
		}
		consumeDiagnosticBudget(budget, len(value), meta)
		if *budget < 0 {
			return "<truncated>"
		}
		return value
	case nil, bool, float64:
		consumeDiagnosticBudget(budget, len(fmt.Sprint(typed)), meta)
		return typed
	default:
		valueOf := reflect.ValueOf(value)
		if valueOf.IsValid() {
			if data, err := json.Marshal(value); err == nil {
				var decoded any
				if json.Unmarshal(data, &decoded) == nil {
					return sanitizeDiagnosticValue(key, decoded, depth, budget, meta)
				}
			}
		}
		return sanitizeDiagnosticValue(key, fmt.Sprint(value), depth, budget, meta)
	}
}

func consumeDiagnosticBudget(budget *int, size int, meta *DiagnosticMeta) {
	*budget -= size
	if *budget < 0 {
		meta.Truncated = true
	}
}

func diagnosticSecretKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return false
	}
	for _, needle := range []string{
		"password", "passwd", "secret", "token", "api_key", "apikey",
		"authorization", "cookie", "credential", "session_hash",
		"private_key", "encryption_key", "access_key", "refresh_token",
	} {
		if strings.Contains(key, needle) {
			return true
		}
	}
	return false
}

func sanitizeDiagnosticString(value string, meta *DiagnosticMeta) string {
	original := value
	value = diagnosticBearerPattern.ReplaceAllString(value, "$1<redacted>")
	value = diagnosticSecretPattern.ReplaceAllString(value, "$1<redacted>")
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.User != nil {
		parsed.User = url.User("<redacted>")
		value = parsed.String()
	}
	if value != original {
		meta.Redacted = true
	}
	return value
}

func mergeDiagnosticMeta(left, right DiagnosticMeta) DiagnosticMeta {
	return DiagnosticMeta{
		Redacted:  left.Redacted || right.Redacted,
		Truncated: left.Truncated || right.Truncated,
	}
}
