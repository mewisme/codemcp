package quickopen

import (
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/interface/tui/action"
)

type Resource struct {
	ID          string
	Title       string
	Kind        string
	Description string
	Keywords    []string
	Path        []string
}

func Actions(resources []Resource) ([]action.Action, map[string]Resource) {
	actions := make([]action.Action, 0, len(resources))
	index := make(map[string]Resource, len(resources))
	for _, resource := range resources {
		base := "quickopen." + sanitize(resource.Kind) + "." + sanitize(resource.ID)
		if strings.TrimSpace(resource.ID) == "" {
			base = "quickopen." + sanitize(resource.Kind) + "." + sanitize(resource.Title)
		}
		id := base
		for suffix := 2; ; suffix++ {
			if _, exists := index[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s.%d", base, suffix)
		}
		index[id] = resource
		actions = append(actions, action.Action{ID: id, Title: resource.Title, Category: resource.Kind, Description: resource.Description, Keywords: resource.Keywords, CommandPath: resource.Path, Scope: action.ScopeGlobal})
	}
	return actions, index
}

func sanitize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-").Replace(value)
	if value == "" {
		return "item"
	}
	return value
}
