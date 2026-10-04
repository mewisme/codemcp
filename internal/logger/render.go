package logger

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fatih/color"
)

type jsonEvent struct {
	Time         string         `json:"time"`
	Level        string         `json:"level"`
	Event        string         `json:"event"`
	Kind         string         `json:"kind"`
	Message      string         `json:"message"`
	Component    string         `json:"component,omitempty"`
	RunID        string         `json:"run_id,omitempty"`
	PID          int            `json:"pid,omitempty"`
	Managed      bool           `json:"managed,omitempty"`
	ServiceID    string         `json:"service_id,omitempty"`
	ServiceScope string         `json:"service_scope,omitempty"`
	Fields       map[string]any `json:"fields,omitempty"`
	Error        string         `json:"error,omitempty"`
}

func (l *Logger) renderText(event Event) {
	if l.mode == ModeDebug {
		l.renderMu.Lock()
		defer l.renderMu.Unlock()
		l.renderDebugText(event)
		return
	}
	l.renderMu.Lock()
	defer l.renderMu.Unlock()
	l.renderTextEvent(event)
}

func (l *Logger) renderTextEvent(event Event) {
	if event.Name == "cli.detail" {
		value := any("")
		if len(event.Fields) > 0 {
			value = event.Fields[0].Value
		}
		fmt.Fprintf(l.out, "    %s %v\n", l.styled(color.Faint).Sprint(event.Message+":"), value)
		return
	}
	if l.showTime() {
		fmt.Fprint(l.out, l.styled(color.Faint).Sprint(l.eventTime(event).Format("15:04:05")), " ")
	}
	fmt.Fprint(l.out, l.symbolStyle(event.Kind).Sprint(l.symbol(event.Kind)), " ", capitalizeIconMessage(event.Message))
	if event.Err != nil {
		fmt.Fprint(l.out, ": ", event.Err)
	}
	fmt.Fprintln(l.out)
	for _, field := range event.Fields {
		if field.Visibility > l.visibility() {
			continue
		}
		l.renderField(field.Key, field.Value)
	}
}

func capitalizeIconMessage(message string) string {
	if message == "" {
		return message
	}
	r, size := utf8.DecodeRuneInString(message)
	if r == utf8.RuneError && size == 0 {
		return message
	}
	upper := unicode.ToUpper(r)
	if upper == r {
		return message
	}
	return string(upper) + message[size:]
}

func (l *Logger) renderDebugText(event Event) {
	level := levelCode(event.Level)
	if l.showTime() {
		fmt.Fprint(l.out, l.styled(color.Faint).Sprint(l.eventTime(event).Format("15:04:05")), " ")
	}
	fmt.Fprintf(l.out, "%s %-10s %s %s", l.levelStyle(level).Sprintf("%-3s", level), l.styled(color.FgHiBlue, color.Bold).Sprintf("%-10s", strings.ToUpper(event.Component)), l.styled(color.Faint).Sprint(event.Name), event.Message)
	for _, field := range event.Fields {
		fmt.Fprintf(l.out, " %s=%v", l.styled(color.Faint).Sprint(field.Key), field.Value)
	}
	if event.Err != nil {
		fmt.Fprintf(l.out, " %s=%v", l.styled(color.Faint).Sprint("error"), event.Err)
	}
	fmt.Fprintln(l.out)
}

func (l *Logger) renderJSON(event Event) {
	fields := make(map[string]any, len(event.Fields))
	for _, field := range event.Fields {
		if field.Visibility > l.visibility() || strings.TrimSpace(field.Key) == "" {
			continue
		}
		fields[field.Key] = jsonValue(field.Value)
	}
	value := jsonEvent{Time: l.eventTime(event).UTC().Format(time.RFC3339Nano), Level: event.Level.String(), Event: event.Name, Kind: event.Kind.String(), Message: event.Message, Component: event.Component, RunID: event.RunID, PID: event.PID, Managed: event.Managed, ServiceID: event.ServiceID, ServiceScope: event.ServiceScope, Fields: fields}
	if event.Err != nil {
		value.Error = event.Err.Error()
	}
	_ = json.NewEncoder(l.out).Encode(value)
}

func (l *Logger) renderField(key string, value any) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "value"
	}
	label := l.styled(color.Faint).Sprint(key + ":")
	if values, ok := stringSlice(value); ok && len(values) > 1 {
		fmt.Fprintf(l.out, "    %s\n", label)
		for _, item := range values {
			fmt.Fprintf(l.out, "      %s %s\n", l.styled(color.Faint).Sprint("-"), item)
		}
		return
	}
	if values, ok := stringSlice(value); ok && len(values) == 1 {
		value = values[0]
	}
	fmt.Fprintf(l.out, "    %s %v\n", label, value)
}

func stringSlice(value any) ([]string, bool) {
	if value == nil {
		return nil, false
	}
	if typed, ok := value.([]string); ok {
		return typed, true
	}
	ref := reflect.ValueOf(value)
	if ref.Kind() != reflect.Slice && ref.Kind() != reflect.Array {
		return nil, false
	}
	values := make([]string, 0, ref.Len())
	for i := 0; i < ref.Len(); i++ {
		values = append(values, fmt.Sprint(ref.Index(i).Interface()))
	}
	return values, true
}

func jsonValue(value any) any {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	return value
}

func (l *Logger) symbol(kind Kind) string {
	if !l.unicodeEnabled() {
		switch kind {
		case KindAction:
			return "*"
		case KindSuccess:
			return "[OK]"
		case KindWarning:
			return "[!]"
		case KindError:
			return "[ERR]"
		default:
			return "[i]"
		}
	}
	switch kind {
	case KindAction:
		return "·"
	case KindSuccess:
		return "✓"
	case KindWarning:
		return "!"
	case KindError:
		return "×"
	default:
		return "·"
	}
}

func (l *Logger) symbolStyle(kind Kind) *color.Color {
	switch kind {
	case KindAction:
		return l.styled(color.FgHiCyan, color.Bold)
	case KindSuccess:
		return l.styled(color.FgHiGreen, color.Bold)
	case KindWarning:
		return l.styled(color.FgHiYellow, color.Bold)
	case KindError:
		return l.styled(color.FgHiRed, color.Bold)
	default:
		return l.styled(color.FgHiBlack, color.Bold)
	}
}

func levelCode(level Level) string {
	switch level {
	case Debug:
		return "DBG"
	case Warn:
		return "WRN"
	case Error:
		return "ERR"
	default:
		return "INF"
	}
}

func (l *Logger) levelStyle(level string) *color.Color {
	switch level {
	case "DBG":
		return l.styled(color.FgHiBlack)
	case "WRN":
		return l.styled(color.FgHiYellow, color.Bold)
	case "ERR":
		return l.styled(color.FgHiRed, color.Bold)
	default:
		return l.styled(color.FgHiCyan, color.Bold)
	}
}
