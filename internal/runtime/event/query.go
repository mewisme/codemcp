package event

import (
	"fmt"
	"path"
	"strings"
	"time"
)

type Query struct {
	Tail        int
	Since       *time.Time
	Until       *time.Time
	RunID       string
	MinLevel    string
	Components  []string
	WorkspaceID string
	Tool        string
	Status      string
	Source      string
	EventGlob   string
	Grep        string
}

func (query Query) Match(event Event) bool {
	if query.Since != nil && event.Time.Before(query.Since.UTC()) {
		return false
	}
	if query.Until != nil && event.Time.After(query.Until.UTC()) {
		return false
	}
	if query.RunID != "" && !strings.HasPrefix(strings.ToLower(event.RunID), strings.ToLower(strings.TrimSpace(query.RunID))) {
		return false
	}
	if query.MinLevel != "" && levelRank(event.Level) < levelRank(query.MinLevel) {
		return false
	}
	if len(query.Components) > 0 && !containsFold(query.Components, event.Component) {
		return false
	}
	if query.WorkspaceID != "" && !strings.EqualFold(query.WorkspaceID, event.WorkspaceID) {
		return false
	}
	if query.Tool != "" && !strings.EqualFold(query.Tool, event.Tool) {
		return false
	}
	if query.Status != "" && !strings.EqualFold(query.Status, event.Status) {
		return false
	}
	if query.Source != "" && !strings.EqualFold(query.Source, event.Source) {
		return false
	}
	if query.EventGlob != "" {
		matched, err := path.Match(query.EventGlob, event.Name)
		if err != nil || !matched {
			return false
		}
	}
	if query.Grep != "" {
		needle := strings.ToLower(query.Grep)
		if !strings.Contains(strings.ToLower(searchText(event)), needle) {
			return false
		}
	}
	return true
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func levelRank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return 0
	case "warn", "warning":
		return 2
	case "error":
		return 3
	default:
		return 1
	}
}

func searchText(event Event) string {
	var builder strings.Builder
	for _, value := range []string{event.RunID, event.Name, event.Component, event.Message, event.Error, event.WorkspaceID, event.Tool, event.Method, event.Source, event.Status} {
		builder.WriteString(value)
		builder.WriteByte(' ')
	}
	for _, field := range event.Fields {
		builder.WriteString(field.Key)
		builder.WriteByte(' ')
		builder.WriteString(fmt.Sprint(field.Value))
		builder.WriteByte(' ')
	}
	return builder.String()
}
