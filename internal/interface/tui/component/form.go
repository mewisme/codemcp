package component

import (
	"fmt"
	"reflect"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type FormMouseMsg struct {
	Group  int
	Field  int
	Line   int
	Choice int
	Click  bool
}

type FormGroup struct {
	group  *huh.Group
	fields []huh.Field
	hide   func() bool
}

func Group(fields ...huh.Field) FormGroup {
	return FormGroup{group: huh.NewGroup(fields...), fields: append([]huh.Field(nil), fields...)}
}

func (group FormGroup) WithHideFunc(hide func() bool) FormGroup {
	group.hide = hide
	group.group.WithHideFunc(hide)
	return group
}

func (group FormGroup) Title(title string) FormGroup {
	group.group.Title(title)
	return group
}

func (group FormGroup) Description(description string) FormGroup {
	group.group.Description(description)
	return group
}

type Form struct {
	model   *huh.Form
	groups  []FormGroup
	initial string
	width   int
	height  int
}

func NewEditorForm(groups ...FormGroup) Form {
	huhGroups := make([]*huh.Group, 0, len(groups))
	for _, group := range groups {
		huhGroups = append(huhGroups, group.group)
	}
	model := huh.NewForm(huhGroups...).WithTheme(huh.ThemeFunc(func(isDark bool) *huh.Styles { return huh.ThemeCharm(isDark) })).WithShowHelp(false).WithKeyMap(editorFormKeyMap())
	form := Form{model: model, groups: append([]FormGroup(nil), groups...)}
	form.initial = form.snapshot()
	return form
}

func (form Form) Init() tea.Cmd {
	if form.model == nil {
		return nil
	}
	return form.model.Init()
}

func (form Form) Update(message tea.Msg) (Form, tea.Cmd) {
	if form.model == nil {
		return form, nil
	}
	if form.OnLastField() && reflect.TypeOf(message) == reflect.TypeOf(huh.NextField()) {
		return form, nil
	}
	if form.OnFirstField() && reflect.TypeOf(message) == reflect.TypeOf(huh.PrevField()) {
		return form, nil
	}
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "esc" {
		return form, nil
	}
	if msg, ok := message.(FormMouseMsg); ok {
		return form.updateMouse(msg)
	}
	updated, cmd := form.model.Update(message)
	if value, ok := updated.(*huh.Form); ok {
		form.model = value
	}
	return form, cmd
}

func (form Form) MouseTargets(originX, originY, z int) []MouseTarget {
	if form.model == nil {
		return nil
	}
	activeGroup := form.activeGroup()
	if activeGroup < 0 || activeGroup >= len(form.groups) {
		return nil
	}
	view := ansi.Strip(form.View())
	lines := strings.Split(view, "\n")
	group := form.groups[activeGroup]
	targets := make([]MouseTarget, 0, len(group.fields)+1)
	searchLine := 0
	for fieldIndex, field := range group.fields {
		if field == nil || field.Skip() {
			continue
		}
		fieldView := ansi.Strip(field.View())
		needle := firstNonEmptyLine(fieldView)
		if needle == "" {
			continue
		}
		lineIndex, column := findRenderedLine(lines, needle, searchLine)
		if lineIndex < 0 {
			continue
		}
		height := max(1, lipgloss.Height(fieldView))
		width := max(1, lipgloss.Width(fieldView))
		groupIndex, index := activeGroup, fieldIndex
		targets = append(targets, MouseTarget{
			ID:   "form.field",
			Rect: Rect{X: originX + column, Y: originY + lineIndex, Width: width, Height: height},
			Z:    z,
			Handle: func(event MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return FormMouseMsg{Group: groupIndex, Field: index, Line: event.Y, Click: true}
			},
		})
		if _, ok := field.(*huh.Confirm); ok {
			for choice, label := range map[int]string{1: "Yes", 2: "No"} {
				rect, ok := FindRenderedRect(fieldView, label)
				if !ok {
					continue
				}
				selectedChoice := choice
				targets = append(targets, MouseTarget{
					ID: "form.confirm", Rect: Rect{X: originX + column + rect.X, Y: originY + lineIndex + rect.Y, Width: rect.Width, Height: 1}, Z: z + 1,
					Handle: func(event MouseEvent) tea.Msg {
						if event.Button != tea.MouseLeft {
							return nil
						}
						return FormMouseMsg{Group: groupIndex, Field: index, Choice: selectedChoice, Click: true}
					},
				})
			}
		}
		searchLine = lineIndex
	}
	return targets
}

func (form Form) activeGroup() int {
	if form.model == nil {
		return -1
	}
	focused := form.model.GetFocusedField()
	for groupIndex, group := range form.groups {
		if group.hide != nil && group.hide() {
			continue
		}
		for _, field := range group.fields {
			if field == focused {
				return groupIndex
			}
		}
	}
	for groupIndex, group := range form.groups {
		if group.hide == nil || !group.hide() {
			return groupIndex
		}
	}
	return -1
}

func (form Form) updateMouse(msg FormMouseMsg) (Form, tea.Cmd) {
	if msg.Group < 0 || msg.Group >= len(form.groups) || msg.Field < 0 || msg.Field >= len(form.groups[msg.Group].fields) {
		return form, nil
	}
	target := form.groups[msg.Group].fields[msg.Field]
	for steps := 0; steps < 128 && form.model.GetFocusedField() != target; steps++ {
		updated, cmd := form.model.Update(huh.NextField())
		if value, ok := updated.(*huh.Form); ok {
			form.model = value
		}
		if cmd != nil {
			return form, cmd
		}
	}
	if !msg.Click {
		return form, nil
	}
	if confirmField, ok := target.(*huh.Confirm); ok && msg.Choice != 0 {
		want := msg.Choice == 1
		current, _ := confirmField.GetValue().(bool)
		if current == want {
			return form, nil
		}
		updated, cmd := form.model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		if value, ok := updated.(*huh.Form); ok {
			form.model = value
		}
		return form, cmd
	}
	if switchField, ok := target.(*SwitchField); ok {
		if switchField.value != nil {
			*switchField.value = !*switchField.value
		}
		return form, nil
	}
	if selectField, ok := target.(*huh.Select[string]); ok {
		return form, form.clickSelect(selectField, msg.Line, false)
	}
	if multiField, ok := target.(*huh.MultiSelect[string]); ok {
		return form, form.clickMultiSelect(multiField, msg.Line)
	}
	if _, ok := target.(*huh.Confirm); ok {
		updated, cmd := form.model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		if value, ok := updated.(*huh.Form); ok {
			form.model = value
		}
		return form, cmd
	}
	return form, nil
}

func (form *Form) clickSelect(field *huh.Select[string], line int, multi bool) tea.Cmd {
	current := hoveredLine(field.View())
	if current < 0 || line <= 0 {
		return nil
	}
	for step := current; step != line; {
		code := tea.KeyDown
		if line < step {
			code = tea.KeyUp
			step--
		} else {
			step++
		}
		updated, _ := form.model.Update(tea.KeyPressMsg{Code: code})
		if value, ok := updated.(*huh.Form); ok {
			form.model = value
		}
	}
	code := tea.KeyEnter
	if multi {
		code = tea.KeySpace
	}
	updated, cmd := form.model.Update(tea.KeyPressMsg{Code: code})
	if value, ok := updated.(*huh.Form); ok {
		form.model = value
	}
	return cmd
}

func (form *Form) clickMultiSelect(field *huh.MultiSelect[string], line int) tea.Cmd {
	current := hoveredLine(field.View())
	if current < 0 || line <= 0 {
		return nil
	}
	for step := current; step != line; {
		code := tea.KeyDown
		if line < step {
			code = tea.KeyUp
			step--
		} else {
			step++
		}
		updated, _ := form.model.Update(tea.KeyPressMsg{Code: code})
		if value, ok := updated.(*huh.Form); ok {
			form.model = value
		}
	}
	updated, cmd := form.model.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if value, ok := updated.(*huh.Form); ok {
		form.model = value
	}
	return cmd
}

func hoveredLine(value string) int {
	for index, line := range strings.Split(ansi.Strip(value), "\n") {
		if strings.Contains(line, "> ") {
			return index
		}
	}
	return -1
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func findRenderedLine(lines []string, needle string, start int) (int, int) {
	for index := max(0, start); index < len(lines); index++ {
		if column := strings.Index(lines[index], needle); column >= 0 {
			return index, lipgloss.Width(lines[index][:column])
		}
	}
	return -1, -1
}

func (form Form) View() string {
	if form.model == nil {
		return ""
	}
	return form.model.View()
}

func (form Form) Dirty() bool { return form.initial != form.snapshot() }

func (form *Form) Accept() {
	if form != nil {
		form.initial = form.snapshot()
	}
}

func (form Form) Validate() error {
	if form.model == nil {
		return nil
	}
	focused := form.model.GetFocusedField()
	for _, field := range form.visibleFields() {
		field.Blur()
		if err := field.Error(); err != nil {
			if focused != nil {
				focused.Focus()
			}
			return err
		}
	}
	if focused != nil {
		focused.Focus()
	}
	return nil
}

func (form *Form) Resize(width, height int) {
	if form == nil || form.model == nil {
		return
	}
	form.width, form.height = max(1, width), max(1, height)
	form.model.WithWidth(form.width).WithHeight(form.height)
}

func (form Form) FocusedFieldIndex() int {
	if form.model == nil {
		return -1
	}
	focused := form.model.GetFocusedField()
	for index, field := range form.visibleFields() {
		if field == focused {
			return index
		}
	}
	return -1
}

func (form Form) OnFirstField() bool { return form.FocusedFieldIndex() == 0 }

func (form Form) OnLastField() bool {
	fields := form.visibleFields()
	return len(fields) > 0 && form.FocusedFieldIndex() == len(fields)-1
}

func (form Form) CompletionSubmittable() bool {
	if form.model == nil || form.model.GetFocusedField() == nil {
		return false
	}
	return !form.FocusedMultiline()
}

func (form Form) FocusedMultiline() bool {
	if form.model == nil || form.model.GetFocusedField() == nil {
		return false
	}
	_, multiline := form.model.GetFocusedField().(*huh.Text)
	return multiline
}

func (form Form) FocusField(index int) (Form, tea.Cmd) {
	fields := form.visibleFields()
	if form.model == nil || index < 0 || index >= len(fields) {
		return form, nil
	}
	current := form.FocusedFieldIndex()
	if current < 0 || current == index {
		return form, nil
	}
	message := huh.NextField()
	if index < current {
		message = huh.PrevField()
	}
	distance := index - current
	if distance < 0 {
		distance = -distance
	}
	cmds := make([]tea.Cmd, 0, distance)
	for current != index {
		updated, cmd := form.model.Update(message)
		if value, ok := updated.(*huh.Form); ok {
			form.model = value
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if index < current {
			current--
		} else {
			current++
		}
	}
	return form, tea.Batch(cmds...)
}

func (form Form) FocusedKeyBinds() []key.Binding {
	if form.model == nil || form.model.GetFocusedField() == nil {
		return nil
	}
	bindings := append([]key.Binding(nil), form.model.GetFocusedField().KeyBinds()...)
	filtered := bindings[:0]
	for _, binding := range bindings {
		if strings.EqualFold(strings.TrimSpace(binding.Help().Desc), "submit") {
			continue
		}
		filtered = append(filtered, binding)
	}
	return filtered
}

func (form Form) visibleFields() []huh.Field {
	fields := make([]huh.Field, 0)
	for _, group := range form.groups {
		if group.hide != nil && group.hide() {
			continue
		}
		for _, field := range group.fields {
			if field != nil && !field.Skip() {
				fields = append(fields, field)
			}
		}
	}
	return fields
}

func (form Form) snapshot() string {
	values := make([]string, 0)
	for _, group := range form.groups {
		for _, field := range group.fields {
			if field == nil {
				continue
			}
			method := reflect.ValueOf(field).MethodByName("GetValue")
			if !method.IsValid() {
				values = append(values, fmt.Sprintf("%T", field))
				continue
			}
			result := method.Call(nil)
			if len(result) == 0 {
				values = append(values, fmt.Sprintf("%T", field))
				continue
			}
			values = append(values, fmt.Sprintf("%T:%#v", field, result[0].Interface()))
		}
	}
	return strings.Join(values, "\x00")
}

func (form Form) State() huh.FormState {
	if form.model == nil {
		return huh.StateAborted
	}
	return form.model.State
}

func editorFormKeyMap() *huh.KeyMap {
	keys := huh.NewDefaultKeyMap()
	keys.Input.Submit.SetEnabled(false)
	keys.Text.Next.SetKeys("tab")
	keys.Text.Next.SetHelp("tab", "next")
	keys.Text.NewLine.SetKeys("enter", "alt+enter", "ctrl+j")
	keys.Text.NewLine.SetHelp("enter", "new line")
	keys.Text.Submit.SetEnabled(false)
	keys.Select.Submit.SetEnabled(false)
	keys.MultiSelect.Submit.SetEnabled(false)
	keys.FilePicker.Submit.SetEnabled(false)
	keys.Note.Submit.SetEnabled(false)
	keys.Confirm.Submit.SetEnabled(false)
	return keys
}

func Input(title string, value *string) *huh.Input {
	return huh.NewInput().Title(strings.TrimSpace(title)).Value(value)
}

func PasswordInput(title string, value *string) *huh.Input {
	return Input(title, value).EchoMode(huh.EchoModePassword)
}

type HintedInputField struct {
	*huh.Input
	title   string
	hint    string
	focused bool
}

func PasswordInputWithHint(title, hint string, value *string) *HintedInputField {
	return &HintedInputField{Input: huh.NewInput().Value(value).EchoMode(huh.EchoModePassword), title: strings.TrimSpace(title), hint: strings.TrimSpace(hint)}
}

func (field *HintedInputField) Focus() tea.Cmd {
	field.focused = true
	return field.Input.Focus()
}

func (field *HintedInputField) Blur() tea.Cmd {
	field.focused = false
	return field.Input.Blur()
}

func (field *HintedInputField) Update(message tea.Msg) (huh.Model, tea.Cmd) {
	updated, cmd := field.Input.Update(message)
	if value, ok := updated.(*huh.Input); ok {
		field.Input = value
	}
	return field, cmd
}

func (field *HintedInputField) View() string {
	styles := huh.ThemeCharm(currentTheme.isDark).Blurred
	if field.focused {
		styles = huh.ThemeCharm(currentTheme.isDark).Focused
	}
	title := styles.Title.Render(field.title)
	if field.hint != "" {
		title += " " + currentTheme.muted.Render("· "+field.hint)
	}
	return title + "\n" + field.Input.View()
}

func Text(title string, value *string) *huh.Text {
	return TextLines(title, value, 4)
}

func TextLines(title string, value *string, lines int) *huh.Text {
	return huh.NewText().Title(strings.TrimSpace(title)).Value(value).Lines(max(1, lines))
}

func FilePicker(title string, value *string) *huh.FilePicker {
	return huh.NewFilePicker().Title(strings.TrimSpace(title)).Value(value)
}

func DirectoryPicker(title string, value *string) *huh.FilePicker {
	return FilePicker(title, value).DirAllowed(true).FileAllowed(false)
}

func Select[T comparable](title string, value *T, options ...huh.Option[T]) *huh.Select[T] {
	return huh.NewSelect[T]().Title(strings.TrimSpace(title)).Options(options...).Value(value)
}

type SwitchField struct {
	*huh.Confirm
	title      string
	value      *bool
	trueLabel  string
	falseLabel string
	focused    bool
}

func Switch(title string, value *bool, labels ...string) *SwitchField {
	trueLabel, falseLabel := "TRUE", "FALSE"
	if len(labels) > 0 && strings.TrimSpace(labels[0]) != "" {
		trueLabel = strings.TrimSpace(labels[0])
	}
	if len(labels) > 1 && strings.TrimSpace(labels[1]) != "" {
		falseLabel = strings.TrimSpace(labels[1])
	}
	return &SwitchField{Confirm: huh.NewConfirm().Value(value), title: strings.TrimSpace(title), value: value, trueLabel: trueLabel, falseLabel: falseLabel}
}

func (field *SwitchField) Focus() tea.Cmd {
	field.focused = true
	return field.Confirm.Focus()
}

func (field *SwitchField) Blur() tea.Cmd {
	field.focused = false
	return field.Confirm.Blur()
}

func (field *SwitchField) Update(message tea.Msg) (huh.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "space" {
		if field.value != nil {
			*field.value = !*field.value
		}
		return field, nil
	}
	updated, cmd := field.Confirm.Update(message)
	if value, ok := updated.(*huh.Confirm); ok {
		field.Confirm = value
	}
	return field, cmd
}

func (field *SwitchField) View() string {
	styles := huh.ThemeCharm(currentTheme.isDark).Blurred
	if field.focused {
		styles = huh.ThemeCharm(currentTheme.isDark).Focused
	}
	state := field.falseLabel
	if field.value != nil && *field.value {
		state = field.trueLabel
	}
	return styles.Base.Render(styles.Title.Render(field.title) + " " + currentTheme.accent.Render("[ "+state+" ]"))
}

func (field *SwitchField) KeyBinds() []key.Binding {
	bindings := field.Confirm.KeyBinds()
	toggle := key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "toggle"))
	if len(bindings) < 4 {
		return []key.Binding{toggle}
	}
	return append([]key.Binding{toggle}, bindings[1:4]...)
}

func MultiSelect[T comparable](title string, value *[]T, options ...huh.Option[T]) *huh.MultiSelect[T] {
	return huh.NewMultiSelect[T]().Title(strings.TrimSpace(title)).Options(options...).Value(value)
}
