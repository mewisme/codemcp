package event

import (
	"fmt"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/logger"
)

const Version = 1

type Field struct {
	Key        string            `json:"key"`
	Value      any               `json:"value,omitempty"`
	Visibility logger.Visibility `json:"visibility,omitempty"`
}

type Event struct {
	Version      int               `json:"version"`
	Sequence     uint64            `json:"sequence,omitempty"`
	Time         time.Time         `json:"time"`
	RunID        string            `json:"run_id,omitempty"`
	PID          int               `json:"pid,omitempty"`
	Level        string            `json:"level"`
	Visibility   logger.Visibility `json:"visibility,omitempty"`
	Kind         string            `json:"kind"`
	Name         string            `json:"event"`
	Component    string            `json:"component,omitempty"`
	Message      string            `json:"message"`
	Fields       []Field           `json:"fields,omitempty"`
	Error        string            `json:"error,omitempty"`
	WorkspaceID  string            `json:"workspace_id,omitempty"`
	Tool         string            `json:"tool,omitempty"`
	Method       string            `json:"method,omitempty"`
	Source       string            `json:"source,omitempty"`
	Status       string            `json:"status,omitempty"`
	DurationMS   int64             `json:"duration_ms,omitempty"`
	Managed      bool              `json:"managed,omitempty"`
	ServiceID    string            `json:"service_id,omitempty"`
	ServiceScope string            `json:"service_scope,omitempty"`
}

type Metadata struct {
	RunID        string
	PID          int
	Managed      bool
	ServiceID    string
	ServiceScope string
}

func fromLoggerEvent(event logger.Event, metadata Metadata) Event {
	result := Event{Version: Version, Time: event.Time.UTC(), RunID: metadata.RunID, PID: metadata.PID, Level: event.Level.String(), Visibility: event.Visibility, Kind: event.Kind.String(), Name: event.Name, Component: event.Component, Message: sanitizeString(event.Message), Managed: metadata.Managed, ServiceID: metadata.ServiceID, ServiceScope: metadata.ServiceScope}
	if event.Err != nil {
		result.Error = sanitizeString(event.Err.Error())
	}
	for _, field := range event.Fields {
		value := sanitizeValue(field.Key, field.Value)
		result.Fields = append(result.Fields, Field{Key: field.Key, Value: value, Visibility: field.Visibility})
		switch strings.ToLower(strings.TrimSpace(field.Key)) {
		case "workspace", "workspace_id":
			result.WorkspaceID = fmt.Sprint(value)
		case "tool":
			result.Tool = fmt.Sprint(value)
		case "method":
			result.Method = fmt.Sprint(value)
		case "source":
			result.Source = fmt.Sprint(value)
		case "status":
			result.Status = fmt.Sprint(value)
		case "duration_ms":
			switch typed := value.(type) {
			case int64:
				result.DurationMS = typed
			case int:
				result.DurationMS = int64(typed)
			case float64:
				result.DurationMS = int64(typed)
			}
		}
	}
	return result
}

func (event Event) LoggerEvent() logger.Event {
	level := logger.Info
	switch strings.ToLower(event.Level) {
	case "debug":
		level = logger.Debug
	case "warn":
		level = logger.Warn
	case "error":
		level = logger.Error
	}
	kind := logger.KindInfo
	switch strings.ToLower(event.Kind) {
	case "action":
		kind = logger.KindAction
	case "success":
		kind = logger.KindSuccess
	case "warning":
		kind = logger.KindWarning
	case "error":
		kind = logger.KindError
	}
	fields := make([]logger.Field, 0, len(event.Fields))
	for _, field := range event.Fields {
		fields = append(fields, logger.Field{Key: field.Key, Value: field.Value, Visibility: field.Visibility})
	}
	var eventErr error
	if event.Error != "" {
		eventErr = fmt.Errorf("%s", event.Error)
	}
	return logger.Event{Time: event.Time, Level: level, Visibility: event.Visibility, Kind: kind, Name: event.Name, Component: event.Component, Message: event.Message, Fields: fields, Err: eventErr, RunID: event.RunID, PID: event.PID, Managed: event.Managed, ServiceID: event.ServiceID, ServiceScope: event.ServiceScope}
}
