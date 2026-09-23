package component

import (
	"reflect"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type EditorSubmitMsg struct{}
type EditorCancelMsg struct{}
type EditorSectionMsg struct{ Index int }

type EditorSection struct {
	ID          string
	Title       string
	Description string
	Form        Form
}

type Editor struct {
	sections     []EditorSection
	active       int
	primaryLabel string
	notice       string
	err          string
	submitting   bool
	help         HelpFooter
	width        int
	height       int
	formY        int
	formHeight   int
}

func NewEditor(primaryLabel string, sections ...EditorSection) Editor {
	editor := Editor{sections: append([]EditorSection(nil), sections...), primaryLabel: strings.TrimSpace(primaryLabel), help: NewHelpFooter(), width: defaultLayoutWidth, height: defaultLayoutHeight}
	if editor.primaryLabel == "" {
		editor.primaryLabel = "save"
	}
	editor.syncHelp()
	editor.resizeForms()
	return editor
}

func (editor Editor) Init() tea.Cmd {
	if len(editor.sections) == 0 {
		return nil
	}
	return editor.sections[editor.active].Form.Init()
}

func (editor Editor) Update(message tea.Msg) (Editor, tea.Cmd) {
	if len(editor.sections) == 0 {
		return editor, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		editor.Resize(msg.Width, msg.Height)
		return editor, nil
	case tea.BackgroundColorMsg:
		for index := range editor.sections {
			updated, _ := editor.sections[index].Form.Update(msg)
			editor.sections[index].Form = updated
		}
		editor.help.Update(msg)
		editor.syncHelp()
		return editor, nil
	case EditorSectionMsg:
		return editor.switchSection(msg.Index, false)
	case tea.KeyPressMsg:
		if editor.help.Update(msg) {
			editor.resizeForms()
			return editor, nil
		}
		form := editor.sections[editor.active].Form
		if msg.String() == "tab" && form.OnLastField() && editor.active < len(editor.sections)-1 {
			return editor.switchSectionValidated(editor.active+1, false)
		}
		if msg.String() == "shift+tab" && form.OnFirstField() && editor.active > 0 {
			return editor.switchSectionValidated(editor.active-1, true)
		}
		switch msg.String() {
		case "ctrl+enter":
			if !form.FocusedMultiline() {
				break
			}
			if editor.submitting {
				return editor, nil
			}
			return editor, func() tea.Msg { return EditorSubmitMsg{} }
		case "esc":
			if editor.submitting {
				return editor, nil
			}
			return editor, func() tea.Msg { return EditorCancelMsg{} }
		}
	}
	form := editor.sections[editor.active].Form
	if reflect.TypeOf(message) == reflect.TypeOf(huh.NextField()) && form.OnLastField() {
		if editor.active < len(editor.sections)-1 {
			return editor.switchSection(editor.active+1, false)
		}
		if !editor.submitting && form.CompletionSubmittable() {
			return editor, func() tea.Msg { return EditorSubmitMsg{} }
		}
		return editor, nil
	}
	if reflect.TypeOf(message) == reflect.TypeOf(huh.PrevField()) && form.OnFirstField() {
		if editor.active > 0 {
			return editor.switchSection(editor.active-1, true)
		}
		return editor, nil
	}
	updated, cmd := form.Update(message)
	editor.sections[editor.active].Form = updated
	editor.syncHelp()
	return editor, cmd
}

func (editor *Editor) Resize(width, height int) {
	if editor == nil {
		return
	}
	editor.width, editor.height = max(1, width), max(1, height)
	editor.resizeForms()
}

func (editor *Editor) SetFeedback(notice string, err error) {
	if editor == nil {
		return
	}
	editor.notice, editor.err = strings.TrimSpace(notice), ""
	if err != nil {
		editor.err = strings.TrimSpace(err.Error())
	}
	editor.resizeForms()
}

func (editor *Editor) SetSubmitting(value bool) {
	if editor == nil || editor.submitting == value {
		return
	}
	editor.submitting = value
	editor.syncHelp()
	editor.resizeForms()
}

func (editor Editor) Submitting() bool { return editor.submitting }

func (editor Editor) Dirty() bool {
	for _, section := range editor.sections {
		if section.Form.Dirty() {
			return true
		}
	}
	return false
}

func (editor *Editor) Accept() {
	if editor == nil {
		return
	}
	for index := range editor.sections {
		editor.sections[index].Form.Accept()
	}
}

func (editor *Editor) Validate() error {
	if editor == nil {
		return nil
	}
	for index := range editor.sections {
		if err := editor.sections[index].Form.Validate(); err != nil {
			editor.active = index
			editor.syncHelp()
			editor.resizeForms()
			return err
		}
	}
	return nil
}

func (editor Editor) ActiveSection() int { return editor.active }

func (editor Editor) ActiveSectionID() string {
	if editor.active < 0 || editor.active >= len(editor.sections) {
		return ""
	}
	return editor.sections[editor.active].ID
}

func (editor Editor) SectionForm(index int) (Form, bool) {
	if index < 0 || index >= len(editor.sections) {
		return Form{}, false
	}
	return editor.sections[index].Form, true
}

func (editor *Editor) SetSectionForm(index int, form Form) bool {
	if editor == nil || index < 0 || index >= len(editor.sections) {
		return false
	}
	editor.sections[index].Form = form
	editor.resizeForms()
	return true
}

func (editor *Editor) View() string {
	if editor == nil || len(editor.sections) == 0 {
		return StateView(PageEmpty, "No editor fields", "")
	}
	layout := editor.layout()
	editor.formY, editor.formHeight = layout.formY, layout.formHeight
	editor.sections[editor.active].Form.Resize(editor.width, layout.formHeight)
	parts := make([]string, 0, 4)
	if layout.tabs != "" {
		parts = append(parts, layout.tabs)
	}
	if layout.feedback != "" {
		parts = append(parts, layout.feedback)
	}
	if layout.description != "" {
		parts = append(parts, layout.description)
	}
	parts = append(parts, editor.sections[editor.active].Form.View())
	return BottomHelp(strings.Join(parts, "\n"), layout.footer, editor.width, editor.height)
}

func (editor Editor) MouseTargets(originX, originY, z int) []MouseTarget {
	if len(editor.sections) == 0 {
		return nil
	}
	layout := editor.layout()
	targets := make([]MouseTarget, 0, len(layout.spans)+4)
	for _, span := range layout.spans {
		index := span.Index
		targets = append(targets, MouseTarget{
			ID: "editor.section", Rect: Rect{X: originX + span.X, Y: originY, Width: span.Width, Height: 1}, Z: z + 1,
			Handle: func(event MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return EditorSectionMsg{Index: index}
			},
		})
	}
	targets = append(targets, editor.sections[editor.active].Form.MouseTargets(originX, originY+layout.formY, z)...)
	return targets
}

type editorLayout struct {
	tabs        string
	spans       []TabSpan
	feedback    string
	description string
	footer      string
	formY       int
	formHeight  int
}

func (editor Editor) layout() editorLayout {
	width, height := max(1, editor.width), max(1, editor.height)
	result := editorLayout{footer: editor.help.View(width)}
	if len(editor.sections) > 1 {
		labels := make([]string, 0, len(editor.sections))
		for _, section := range editor.sections {
			labels = append(labels, section.Title)
		}
		result.tabs, result.spans = PageTabsLayout(labels, editor.active, "", width)
	}
	feedback := make([]string, 0, 2)
	if editor.err != "" {
		feedback = append(feedback, BannerWidth(editor.err, ToneDanger, width))
	}
	if editor.notice != "" {
		feedback = append(feedback, BannerWidth(editor.notice, ToneSuccess, width))
	}
	if editor.submitting {
		feedback = append(feedback, WrapContent(Muted("Saving..."), width))
	}
	result.feedback = strings.Join(feedback, "\n")
	result.description = WrapContent(Muted(strings.TrimSpace(editor.sections[editor.active].Description)), width)
	prefix := make([]string, 0, 3)
	for _, value := range []string{result.tabs, result.feedback, result.description} {
		if strings.TrimSpace(value) != "" {
			prefix = append(prefix, value)
		}
	}
	result.formY = 0
	if len(prefix) > 0 {
		result.formY = lipgloss.Height(strings.Join(prefix, "\n")) + 1
	}
	footerHeight := lipgloss.Height(result.footer)
	result.formHeight = max(1, height-result.formY-footerHeight-1)
	return result
}

func (editor *Editor) resizeForms() {
	if editor == nil || len(editor.sections) == 0 {
		return
	}
	layout := editor.layout()
	editor.formY, editor.formHeight = layout.formY, layout.formHeight
	for index := range editor.sections {
		editor.sections[index].Form.Resize(max(1, editor.width), layout.formHeight)
	}
}

func (editor *Editor) syncHelp() {
	if editor == nil {
		return
	}
	bindings := make([]key.Binding, 0, 10)
	if editor.active >= 0 && editor.active < len(editor.sections) {
		form := editor.sections[editor.active].Form
		var primary key.Binding
		switch {
		case form.FocusedMultiline():
			primary = Binding([]string{"ctrl+enter"}, "ctrl+enter", editor.primaryLabel)
		case editor.active == len(editor.sections)-1 && form.OnLastField() && form.CompletionSubmittable():
			primary = Binding([]string{"enter"}, "enter", editor.primaryLabel)
		}
		if primary.Help().Key != "" {
			if editor.submitting {
				primary.SetEnabled(false)
			}
			bindings = append(bindings, primary)
		}
	}
	if editor.active >= 0 && editor.active < len(editor.sections) {
		bindings = append(bindings, editor.sections[editor.active].Form.FocusedKeyBinds()...)
	}
	bindings = append(bindings, Binding([]string{"tab"}, "tab", "next"), Binding([]string{"shift+tab"}, "shift+tab", "back"), Binding([]string{"esc"}, "esc", "cancel"))
	editor.help.SetBindings(uniqueHelpBindings(bindings)...)
}

func (editor Editor) switchSection(index int, focusLast bool) (Editor, tea.Cmd) {
	if index < 0 || index >= len(editor.sections) || index == editor.active {
		return editor, nil
	}
	current := editor.sections[editor.active].Form
	blur := tea.Cmd(nil)
	if current.model != nil && current.model.GetFocusedField() != nil {
		blur = current.model.GetFocusedField().Blur()
	}
	editor.sections[editor.active].Form = current
	editor.active = index
	next := editor.sections[index].Form
	init := next.Init()
	if focusLast {
		var focus tea.Cmd
		next, focus = next.FocusField(len(next.visibleFields()) - 1)
		init = tea.Batch(init, focus)
	}
	editor.sections[index].Form = next
	editor.syncHelp()
	editor.resizeForms()
	return editor, tea.Batch(blur, init)
}

func (editor Editor) switchSectionValidated(index int, focusLast bool) (Editor, tea.Cmd) {
	if editor.active < 0 || editor.active >= len(editor.sections) {
		return editor, nil
	}
	form := editor.sections[editor.active].Form
	if form.model == nil || form.model.GetFocusedField() == nil {
		return editor.switchSection(index, focusLast)
	}
	field := form.model.GetFocusedField()
	blur := field.Blur()
	if field.Error() != nil {
		return editor, tea.Batch(blur, field.Focus())
	}
	updated, cmd := editor.switchSection(index, focusLast)
	return updated, tea.Batch(blur, cmd)
}

func uniqueHelpBindings(values []key.Binding) []key.Binding {
	result := make([]key.Binding, 0, len(values))
	seen := map[string]bool{}
	for _, binding := range values {
		help := binding.Help()
		keyValue := strings.TrimSpace(help.Key)
		if keyValue == "" || seen[keyValue] {
			continue
		}
		seen[keyValue] = true
		result = append(result, binding)
	}
	return result
}
