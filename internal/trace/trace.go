package trace

import (
	"context"
	"fmt"
	"net/url"
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
			values[index] = "<redacted>"
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
		if sensitiveKey(key) {
			value = configuredState(value)
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

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	if key == "authorization" || key == "cookie" || key == "set_cookie" || key == "token" || key == "oauth_code" || key == "oauth_state" {
		return true
	}
	for _, fragment := range []string{"access_token", "refresh_token", "bearer_token", "client_secret", "admin_key", "runtime_api_key", "api_key", "apikey", "token_hash", "password", "signature", "credential"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func sensitiveQueryKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	if sensitiveKey(key) {
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
