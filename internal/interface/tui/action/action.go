package action

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/productadapter"
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
	Operation    capability.ID
	Capabilities []capability.ID
	Shortcut     key.Binding
	Scope        Scope
	Available    func(Context) bool
	Unavailable  func(Context) string
	Run          func(context.Context, Context) tea.Cmd
}

func (action Action) IsAvailable(ctx Context) bool {
	return action.Available == nil || action.Available(ctx)
}

func (action Action) Availability(ctx Context) (bool, string) {
	if action.IsAvailable(ctx) {
		return true, ""
	}
	reason := "Not available in the current context."
	if action.Unavailable != nil {
		if value := strings.TrimSpace(action.Unavailable(ctx)); value != "" {
			reason = value
		}
	}
	return false, reason
}

func (action Action) CanonicalSpec() (capability.Spec, bool) {
	if action.Operation == "" {
		return capability.Spec{}, false
	}
	return capability.Lookup(action.Operation)
}

func (action Action) ConfirmationMode() capability.ConfirmationMode {
	spec, ok := action.CanonicalSpec()
	if !ok {
		return capability.ConfirmationNone
	}
	return spec.Confirmation.Mode
}

func (action Action) Destructive() bool {
	presentation, ok := action.Presentation()
	return ok && presentation.Danger == productadapter.DangerDestructive
}

func (action Action) Presentation() (productadapter.OperationPresentation, bool) {
	if action.Operation == "" {
		return productadapter.OperationPresentation{}, false
	}
	return productadapter.PresentationFor(action.Operation, "")
}
