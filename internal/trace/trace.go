package trace

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Phase string

const (
	PhaseStart Phase = "start"
	PhaseEnd   Phase = "end"
	PhaseInfo  Phase = "info"
	PhaseError Phase = "error"
)

type Field struct {
	Key   string
	Value any
}

type Event struct {
	Component string
	Name      string
	Message   string
	Phase     Phase
	Fields    []Field
	Time      time.Time
}

type Observer func(Event)

type observerContextKey struct{}

type Span struct {
	observer  Observer
	component string
	name      string
	message   string
	started   time.Time
	ended     bool
}

func WithObserver(ctx context.Context, observer Observer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, observerContextKey{}, observer)
}

func WithoutObserver(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, observerContextKey{}, Observer(nil))
}

func ObserverFromContext(ctx context.Context) Observer {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(observerContextKey{}).(Observer)
	return observer
}

func Emit(ctx context.Context, component, name, message string, fields ...Field) {
	EmitObserver(ObserverFromContext(ctx), component, name, message, fields...)
}

func EmitObserver(observer Observer, component, name, message string, fields ...Field) {
	if observer == nil {
		return
	}
	observer(normalizeEvent(Event{Component: component, Name: name, Message: message, Phase: PhaseInfo, Fields: fields, Time: time.Now()}))
}

func Start(ctx context.Context, component, name, message string, fields ...Field) *Span {
	return StartObserver(ObserverFromContext(ctx), component, name, message, fields...)
}

func StartObserver(observer Observer, component, name, message string, fields ...Field) *Span {
	started := time.Now()
	span := &Span{observer: observer, component: component, name: strings.TrimSuffix(strings.TrimSpace(name), ".started"), message: strings.TrimSpace(message), started: started}
	if observer != nil {
		observer(normalizeEvent(Event{Component: component, Name: phaseName(span.name, PhaseStart), Message: message, Phase: PhaseStart, Fields: fields, Time: started}))
	}
	return span
}

func (span *Span) End(fields ...Field) { span.finish("", nil, fields...) }

func (span *Span) EndMessage(message string, fields ...Field) { span.finish(message, nil, fields...) }

func (span *Span) Fail(err error, fields ...Field) { span.finish("", err, fields...) }

func (span *Span) FailMessage(message string, err error, fields ...Field) {
	span.finish(message, err, fields...)
}

func (span *Span) Finish(err error, fields ...Field) { span.finish("", err, fields...) }

func (span *Span) finish(message string, err error, fields ...Field) {
	if span == nil || span.ended {
		return
	}
	span.ended = true
	if span.observer == nil {
		return
	}
	phase := PhaseEnd
	if err != nil {
		phase = PhaseError
		fields = append(fields, String("error", sanitizeError(err)))
	}
	fields = append(fields, Int64("duration_ms", time.Since(span.started).Milliseconds()))
	if strings.TrimSpace(message) == "" {
		message = span.message
	}
	span.observer(normalizeEvent(Event{Component: span.component, Name: phaseName(span.name, phase), Message: message, Phase: phase, Fields: fields, Time: time.Now()}))
}

func String(key, value string) Field                   { return Field{Key: key, Value: value} }
func Bool(key string, value bool) Field                { return Field{Key: key, Value: value} }
func Int(key string, value int) Field                  { return Field{Key: key, Value: value} }
func Int64(key string, value int64) Field              { return Field{Key: key, Value: value} }
func Uint64(key string, value uint64) Field            { return Field{Key: key, Value: value} }
func DurationMS(key string, value time.Duration) Field { return Int64(key, value.Milliseconds()) }
func Any(key string, value any) Field                  { return Field{Key: key, Value: value} }
func Sensitive(key string, value any) Field            { return Field{Key: key, Value: configuredState(value)} }
func URL(key, value string) Field                      { return Field{Key: key, Value: SanitizeURL(value)} }

const redactedValue = "<redacted>"

var (
	secretTokenPattern      = regexp.MustCompile(`(?i)\b(?:mcp|admin|runtime)_[A-Za-z0-9_-]{20,}\b`)
	bearerPattern           = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;]+`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)(\b(?:authorization|api[-_.]?key|token|password|passwd|secret|credential)\b\s*(?:=|:)\s*)([^\s,;]+)`)
)

func MaskSecret(raw string, configured bool) string {
	if !configured {
		return "not configured"
	}
	runes := []rune(strings.TrimSpace(raw))
	if len(runes) < 6 {
		return "…********…"
	}
	if len(runes) < 16 {
		return string(runes[:1]) + "********" + string(runes[len(runes)-1:])
	}
	if len(runes) < 24 {
		return string(runes[:2]) + "********" + string(runes[len(runes)-2:])
	}
	return string(runes[:4]) + "********" + string(runes[len(runes)-4:])
}

func SensitiveName(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer("-", "_", ".", "_", " ", "_", ":", "_").Replace(key)
	key = strings.Trim(key, "_")
	if key == "authorization" || key == "cookie" || key == "set_cookie" || key == "token" || key == "key" || key == "oauth_code" || key == "oauth_state" {
		return true
	}
	for _, fragment := range []string{"access_token", "refresh_token", "bearer_token", "client_secret", "secret", "admin_key", "runtime_api_key", "api_key", "apikey", "token_hash", "password", "passwd", "signature", "credential"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return strings.HasSuffix(key, "_secret") || strings.HasSuffix(key, "_key") || strings.HasSuffix(key, "_token")
}

func SanitizeText(value string) string {
	value = bearerPattern.ReplaceAllString(value, "${1}<redacted>")
	value = secretAssignmentPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := secretAssignmentPattern.FindStringSubmatch(match)
		if len(parts) == 3 {
			masked := strings.TrimSpace(parts[2])
			if masked == redactedValue || masked == "[redacted]" {
				return match
			}
			return parts[1] + redactedValue
		}
		return redactedValue
	})
	return secretTokenPattern.ReplaceAllString(value, redactedValue)
}

func SanitizeValue(key string, value any) any {
	key = strings.TrimSpace(key)
	if SensitiveName(key) {
		if text, ok := value.(string); ok {
			trimmed := strings.TrimSpace(text)
			if trimmed == redactedValue || trimmed == "[redacted]" || trimmed == "configured" || trimmed == "not configured" {
				return text
			}
		}
		return redactedValue
	}
	switch typed := value.(type) {
	case map[string]any:
		return SanitizeMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = SanitizeValue("", item)
		}
		return result
	case []string:
		if strings.EqualFold(key, "args") {
			return SanitizeArgs(typed)
		}
		result := make([]string, len(typed))
		for index, item := range typed {
			result[index] = SanitizeText(item)
		}
		return result
	case string:
		if strings.EqualFold(key, "command") {
			return SanitizeCommand(typed)
		}
		if looksLikeURLKey(key) {
			return SanitizeURL(typed)
		}
		return SanitizeText(typed)
	case error:
		return SanitizeText(typed.Error())
	default:
		return value
	}
}

func SanitizeMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	settingKey, _ := value["key"].(string)
	redactValue := SensitiveName(settingKey)
	for key, item := range value {
		if redactValue && strings.EqualFold(strings.TrimSpace(key), "value") {
			result[key] = redactedValue
			continue
		}
		result[key] = SanitizeValue(key, item)
	}
	return result
}

func SanitizeArgs(args []string) []string {
	result := append([]string(nil), args...)
	redactNext := false
	for index, arg := range result {
		trimmed := strings.TrimSpace(arg)
		if redactNext {
			result[index] = redactedValue
			redactNext = false
			continue
		}
		if equal := strings.IndexByte(trimmed, '='); equal > 0 {
			name := strings.TrimLeft(strings.TrimSpace(trimmed[:equal]), "-")
			if SensitiveName(name) {
				prefix := arg[:strings.Index(arg, "=")+1]
				result[index] = prefix + redactedValue
				continue
			}
		}
		if colon := strings.IndexByte(trimmed, ':'); colon > 0 {
			name := strings.TrimLeft(strings.TrimSpace(trimmed[:colon]), "-")
			if SensitiveName(name) {
				prefix := arg[:strings.Index(arg, ":")+1]
				result[index] = prefix + " " + redactedValue
				continue
			}
		}
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			result[index] = SanitizeURL(strings.Trim(trimmed, "'\""))
			continue
		}
		name := strings.TrimLeft(strings.Trim(trimmed, "'\""), "-")
		if SensitiveName(name) {
			redactNext = true
			continue
		}
	}
	return result
}

func SanitizeCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	tokens := commandTokens(command)
	if len(tokens) == 0 {
		return command
	}
	sanitized := SanitizeArgs(tokens)
	changed := false
	for index := range tokens {
		if tokens[index] != sanitized[index] {
			changed = true
			break
		}
	}
	if !changed {
		return command
	}
	return strings.Join(sanitized, " ")
}

func commandTokens(command string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	for _, char := range command {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			current.WriteRune(char)
			escaped = true
			continue
		}
		if quote != 0 {
			current.WriteRune(char)
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			current.WriteRune(char)
			continue
		}
		if char == ' ' || char == '\t' || char == '\r' || char == '\n' {
			flush()
			continue
		}
		current.WriteRune(char)
	}
	flush()
	return tokens
}

func SanitizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	query := parsed.Query()
	for key, values := range query {
		if !sensitiveQueryKey(key) {
			continue
		}
		for index := range values {
			values[index] = redactedValue
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func normalizeEvent(event Event) Event {
	event.Component = strings.TrimSpace(event.Component)
	if event.Component == "" {
		event.Component = "TRACE"
	}
	event.Name = strings.TrimSpace(event.Name)
	if event.Name == "" {
		event.Name = "trace.event"
	}
	event.Message = strings.TrimSpace(event.Message)
	if event.Message == "" {
		event.Message = event.Name
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	for index, field := range event.Fields {
		key := strings.TrimSpace(field.Key)
		value := field.Value
		if SensitiveName(key) {
			value = configuredState(value)
		} else if strings.EqualFold(key, "command") {
			value = SanitizeCommand(fmt.Sprint(value))
		} else if strings.EqualFold(key, "args") {
			if args, ok := value.([]string); ok {
				value = SanitizeArgs(args)
			}
		} else if looksLikeURLKey(key) {
			value = SanitizeURL(fmt.Sprint(value))
		}
		event.Fields[index] = Field{Key: key, Value: value}
	}
	return event
}

func phaseName(name string, phase Phase) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "trace.event"
	}
	switch phase {
	case PhaseStart:
		return name + ".started"
	case PhaseError:
		return name + ".failed"
	case PhaseEnd:
		return name + ".completed"
	default:
		return name
	}
}

func configuredState(value any) string {
	if value == nil {
		return "empty"
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return "empty"
	}
	return "configured"
}

func sensitiveQueryKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	if SensitiveName(key) {
		return true
	}
	return key == "token" || key == "code" || key == "state" || key == "sig" || strings.HasSuffix(key, "_token") || strings.HasSuffix(key, "_signature")
}

func looksLikeURLKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "url" || key == "endpoint" || strings.HasSuffix(key, "_url") || strings.HasSuffix(key, "_endpoint")
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, marker := range []string{"http://", "https://"} {
		start := 0
		for {
			index := strings.Index(text[start:], marker)
			if index < 0 {
				break
			}
			index += start
			end := index
			for end < len(text) && !strings.ContainsRune(" \t\r\n)]}\"'", rune(text[end])) {
				end++
			}
			raw := text[index:end]
			clean := SanitizeURL(raw)
			text = text[:index] + clean + text[end:]
			start = index + len(clean)
		}
	}
	return text
}

func SanitizeError(err error) string {
	return sanitizeError(err)
}
