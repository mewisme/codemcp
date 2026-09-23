package component

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

const defaultLayoutWidth = 80
const defaultLayoutHeight = 20

func NewDefaultList(title string, items []list.Item, width, height int, singular, plural string) list.Model {
	delegate := list.NewDefaultDelegate()
	model := list.New(items, delegate, width, height)
	ApplyDefaultListTheme(&model, true)
	model.Title = title
	model.DisableQuitKeybindings()
	model.SetStatusBarItemName(singular, plural)
	return model
}

func ApplyDefaultTextInputTheme(model *textinput.Model, isDark bool) {
	if model != nil {
		model.SetStyles(textinput.DefaultStyles(isDark))
	}
}

func ApplyDefaultListTheme(model *list.Model, isDark bool) {
	SetDarkBackground(isDark)
	model.Styles = list.DefaultStyles(isDark)
	delegate := list.NewDefaultDelegate()
	delegate.Styles = list.NewDefaultItemStyles(isDark)
	model.SetDelegate(delegate)
}

func ResizeDefaultList(model *list.Model, width, height int) {
	if model == nil {
		return
	}
	layoutWidth, layoutHeight := DefaultLayoutSize(width, height)
	model.SetSize(layoutWidth, layoutHeight)
}

func DefaultLayoutSize(width, height int) (int, int) {
	layoutWidth, layoutHeight := defaultLayoutWidth, defaultLayoutHeight
	if width > 0 {
		layoutWidth = width
	}
	if height > 0 {
		layoutHeight = height
	}
	return layoutWidth, layoutHeight
}

func Binding(keys []string, helpKey, description string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey, description))
}

func DefaultHelp(width int, bindings ...key.Binding) string {
	return NewHelpFooter(bindings...).View(width)
}

func BottomHelp(content, footer string, width, height int) string {
	content, footer = strings.TrimRight(content, "\n"), strings.TrimRight(footer, "\n")
	if footer == "" {
		return content
	}
	if height <= 0 {
		return content + "\n" + footer
	}
	footerHeight := lipgloss.Height(footer)
	bodyHeight := max(0, height-footerHeight)
	if bodyHeight == 0 {
		return footer
	}
	body := lipgloss.NewStyle().Width(max(1, width)).Height(bodyHeight).MaxHeight(bodyHeight).Render(content)
	return body + "\n" + footer
}
