package component

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

const defaultHelpShortLimit = 5

type HelpFooter struct {
	model      help.Model
	bindings   []key.Binding
	shortLimit int
}

func NewHelpFooter(bindings ...key.Binding) HelpFooter {
	model := help.New()
	model.Styles = help.DefaultStyles(currentTheme.isDark)
	footer := HelpFooter{model: model, shortLimit: defaultHelpShortLimit}
	footer.SetBindings(bindings...)
	return footer
}

func (footer *HelpFooter) SetBindings(bindings ...key.Binding) {
	if footer == nil {
		return
	}
	footer.bindings = append(footer.bindings[:0], bindings...)
	if footer.shortLimit <= 0 {
		footer.shortLimit = defaultHelpShortLimit
	}
	if len(footer.enabledBindings()) <= footer.shortLimit {
		footer.model.ShowAll = false
	}
}

func (footer HelpFooter) Expanded() bool { return footer.model.ShowAll }

func (footer *HelpFooter) SetExpanded(expanded bool) {
	if footer == nil {
		return
	}
	footer.model.ShowAll = expanded && len(footer.enabledBindings()) > footer.shortLimit
}

func (footer *HelpFooter) Update(message tea.Msg) bool {
	if footer == nil {
		return false
	}
	if msg, ok := message.(tea.BackgroundColorMsg); ok {
		footer.model.Styles = help.DefaultStyles(msg.IsDark())
		return false
	}
	if len(footer.enabledBindings()) > footer.shortLimit {
		if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "?" {
			footer.model.ShowAll = !footer.model.ShowAll
			return true
		}
	}
	updated, _ := footer.model.Update(message)
	footer.model = updated
	return false
}

func (footer HelpFooter) View(width int) string {
	bindings := footer.enabledBindings()
	if len(bindings) == 0 {
		return ""
	}
	model := footer.model
	model.SetWidth(width)
	if len(bindings) <= footer.shortLimit {
		return WrapContent(model.ShortHelpView(bindings), width)
	}
	toggle := Binding([]string{"?"}, "?", "more")
	if model.ShowAll {
		toggle = Binding([]string{"?"}, "?", "less")
		return WrapContent(model.FullHelpView([][]key.Binding{append(bindings, toggle)}), width)
	}
	visible := max(0, footer.shortLimit-1)
	short := append([]key.Binding(nil), bindings[:min(visible, len(bindings))]...)
	short = append(short, toggle)
	return WrapContent(model.ShortHelpView(short), width)
}

func (footer HelpFooter) enabledBindings() []key.Binding {
	bindings := make([]key.Binding, 0, len(footer.bindings))
	for _, binding := range footer.bindings {
		help := binding.Help()
		if binding.Enabled() && strings.TrimSpace(help.Key+help.Desc) != "" {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}
