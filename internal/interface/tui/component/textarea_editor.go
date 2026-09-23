package component

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type TextAreaSavedMsg struct{ Value string }
type TextAreaCancelledMsg struct{}

type TextAreaEditor struct {
	input   textarea.Model
	help    HelpFooter
	title   string
	initial string
	width   int
	height  int
}

func NewTextAreaEditor(title, value string) TextAreaEditor {
	return NewTextAreaEditorAction(title, value, "save")
}

func NewTextAreaEditorAction(title, value, action string) TextAreaEditor {
	input := textarea.New()
	input.SetValue(value)
	input.SetStyles(textarea.DefaultStyles(currentTheme.isDark))
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.Focus()
	action = strings.TrimSpace(action)
	if action == "" {
		action = "save"
	}
	return TextAreaEditor{
		input: input, title: strings.TrimSpace(title), initial: value,
		help: NewHelpFooter(Binding([]string{"ctrl+enter"}, "ctrl+enter", action), Binding([]string{"esc"}, "esc", "cancel")),
	}
}

func (editor TextAreaEditor) Init() tea.Cmd { return editor.input.Focus() }

func (editor TextAreaEditor) Update(message tea.Msg) (TextAreaEditor, tea.Cmd) {
	switch msg := message.(type) {
	case tea.BackgroundColorMsg:
		editor.input.SetStyles(textarea.DefaultStyles(msg.IsDark()))
		editor.help.Update(msg)
		return editor, nil
	case tea.WindowSizeMsg:
		editor.Resize(msg.Width, msg.Height)
		return editor, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+enter":
			value := editor.input.Value()
			return editor, func() tea.Msg { return TextAreaSavedMsg{Value: value} }
		case "esc":
			return editor, func() tea.Msg { return TextAreaCancelledMsg{} }
		}
	}
	updated, cmd := editor.input.Update(message)
	editor.input = updated
	return editor, cmd
}

func (editor *TextAreaEditor) Resize(width, height int) {
	if editor == nil {
		return
	}
	editor.width, editor.height = max(1, width), max(1, height)
	headerHeight := 0
	if editor.title != "" {
		headerHeight = 2
	}
	footerHeight := lipgloss.Height(editor.help.View(editor.width))
	editor.input.SetWidth(editor.width)
	editor.input.SetHeight(max(3, editor.height-headerHeight-footerHeight))
}

func (editor *TextAreaEditor) SetValue(value string) {
	if editor == nil {
		return
	}
	editor.input.SetValue(value)
	editor.initial = value
}

func (editor TextAreaEditor) Value() string { return editor.input.Value() }
func (editor TextAreaEditor) Dirty() bool   { return editor.input.Value() != editor.initial }
func (editor TextAreaEditor) Focused() bool { return editor.input.Focused() }

func (editor TextAreaEditor) View() string {
	width, height := editor.width, editor.height
	if width <= 0 {
		width = defaultLayoutWidth
	}
	if height <= 0 {
		height = defaultLayoutHeight
	}
	input := editor.input
	header := ""
	if editor.title != "" {
		header = PageTitle(editor.title, width) + "\n"
	}
	footer := editor.help.View(width)
	return BottomHelp(header+input.View(), footer, width, height)
}
