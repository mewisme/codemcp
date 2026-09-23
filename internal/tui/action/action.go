package action

import (
	"context"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/capability"
)

type Scope string

const (
	ScopeGlobal   Scope = "global"
	ScopeRoute    Scope = "route"
	ScopeResource Scope = "resource"
)

type Context struct {
	Route      string
	Mode       string
	ResourceID string
	Section    string
	Action     string
}

type Action struct {
	ID           string
	Title        string
	Category     string
	Description  string
	Keywords     []string
	CommandPath  []string
	Capabilities []capability.ID
	Shortcut     key.Binding
	Scope        Scope
	Available    func(Context) bool
	Run          func(context.Context, Context) tea.Cmd
}

func (action Action) IsAvailable(ctx Context) bool {
	return action.Available == nil || action.Available(ctx)
}
