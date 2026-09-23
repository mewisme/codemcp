package component

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type PathKind uint8

const (
	PathKindFile PathKind = iota
	PathKindDirectory
)

type PathFieldMode uint8

const (
	PathFieldPicker PathFieldMode = iota
	PathFieldInput
)

type PathFieldOptions struct {
	Kind         PathKind
	Root         string
	RelativeTo   string
	AllowMissing bool
	InputFirst   bool
	Validate     func(string) error
}

type PathField struct {
	title    string
	key      string
	value    *string
	options  PathFieldOptions
	mode     PathFieldMode
	input    *huh.Input
	picker   *huh.FilePicker
	focused  bool
	width    int
	height   int
	position huh.FieldPosition
}

func NewPathField(title string, value *string, options PathFieldOptions) *PathField {
	if value == nil {
		value = new(string)
	}
	field := &PathField{title: strings.TrimSpace(title), value: value, options: options, width: defaultLayoutWidth, height: defaultLayoutHeight}
	if options.InputFirst {
		field.mode = PathFieldInput
	}
	field.input = huh.NewInput().Value(value).Validate(field.validatePath)
	field.picker = huh.NewFilePicker().Value(value).Validate(field.validatePath)
	if options.Kind == PathKindDirectory {
		field.picker.DirAllowed(true).FileAllowed(false)
	} else {
		field.picker.DirAllowed(false).FileAllowed(true)
	}
	if base := field.pickerBase(); base != "" {
		field.picker.CurrentDirectory(base)
	}
	return field
}

func (field *PathField) Key(value string) *PathField {
	field.key = strings.TrimSpace(value)
	field.input.Key(field.key)
	field.picker.Key(field.key)
	return field
}

func (field *PathField) Mode() PathFieldMode { return field.mode }

func (field *PathField) SetMode(mode PathFieldMode) tea.Cmd {
	if field == nil || field.mode == mode {
		return nil
	}
	blur := field.active().Blur()
	field.mode = mode
	field.syncValue()
	if !field.focused {
		return blur
	}
	return tea.Batch(blur, field.active().Focus())
}

func (field *PathField) Init() tea.Cmd {
	if field == nil {
		return nil
	}
	return field.active().Init()
}

func (field *PathField) Update(message tea.Msg) (huh.Model, tea.Cmd) {
	if field == nil {
		return field, nil
	}
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "ctrl+o" {
		next := PathFieldInput
		if field.mode == PathFieldInput {
			next = PathFieldPicker
		}
		return field, field.SetMode(next)
	}
	updated, cmd := field.active().Update(message)
	field.setActive(updated.(huh.Field))
	if field.mode == PathFieldPicker {
		field.normalizePickerValue()
	}
	return field, cmd
}

func (field *PathField) View() string {
	if field == nil {
		return ""
	}
	mode := "picker"
	if field.mode == PathFieldInput {
		mode = "input"
	}
	header := TwoColumn(Title(field.title), Muted(mode+" · ctrl+o switch"), max(1, field.width))
	return header + "\n" + field.active().View()
}

func (field *PathField) Focus() tea.Cmd {
	if field == nil {
		return nil
	}
	field.focused = true
	return field.active().Focus()
}

func (field *PathField) Blur() tea.Cmd {
	if field == nil {
		return nil
	}
	field.focused = false
	return field.active().Blur()
}

func (field *PathField) Error() error {
	if field == nil {
		return nil
	}
	return field.active().Error()
}

func (field *PathField) Run() error {
	if field == nil {
		return nil
	}
	return field.active().Run()
}

func (field *PathField) RunAccessible(writer io.Writer, reader io.Reader) error {
	if field == nil {
		return nil
	}
	return field.active().RunAccessible(writer, reader)
}

func (field *PathField) Skip() bool { return false }

func (field *PathField) Zoom() bool {
	return field != nil && field.mode == PathFieldPicker && field.picker.Zoom()
}

func (field *PathField) KeyBinds() []key.Binding {
	if field == nil {
		return nil
	}
	toggle := key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "picker/input"))
	return append([]key.Binding{toggle}, field.active().KeyBinds()...)
}

func (field *PathField) WithTheme(theme huh.Theme) huh.Field {
	if field == nil {
		return field
	}
	field.input.WithTheme(theme)
	field.picker.WithTheme(theme)
	return field
}

func (field *PathField) WithKeyMap(keys *huh.KeyMap) huh.Field {
	if field == nil {
		return field
	}
	field.input.WithKeyMap(keys)
	field.picker.WithKeyMap(keys)
	return field
}

func (field *PathField) WithWidth(width int) huh.Field {
	if field == nil || width <= 0 {
		return field
	}
	field.width = width
	field.input.WithWidth(width)
	field.picker.WithWidth(width)
	return field
}

func (field *PathField) WithHeight(height int) huh.Field {
	if field == nil || height <= 0 {
		return field
	}
	field.height = height
	field.input.WithHeight(height)
	field.picker.WithHeight(height)
	return field
}

func (field *PathField) WithPosition(position huh.FieldPosition) huh.Field {
	if field == nil {
		return field
	}
	field.position = position
	field.input.WithPosition(position)
	field.picker.WithPosition(position)
	return field
}

func (field *PathField) GetKey() string { return field.key }

func (field *PathField) GetValue() any {
	if field == nil || field.value == nil {
		return ""
	}
	return *field.value
}

func (field *PathField) active() huh.Field {
	if field.mode == PathFieldInput {
		return field.input
	}
	return field.picker
}

func (field *PathField) setActive(value huh.Field) {
	if field.mode == PathFieldInput {
		field.input = value.(*huh.Input)
		return
	}
	field.picker = value.(*huh.FilePicker)
}

func (field *PathField) syncValue() {
	if field == nil || field.value == nil {
		return
	}
	field.input.Value(field.value)
	field.picker.Value(field.value)
}

func (field *PathField) pickerBase() string {
	for _, value := range []string{field.options.Root, field.options.RelativeTo} {
		if value = strings.TrimSpace(value); value != "" {
			if absolute, err := filepath.Abs(value); err == nil {
				return absolute
			}
			return filepath.Clean(value)
		}
	}
	return ""
}

func (field *PathField) resolvedPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	base := strings.TrimSpace(field.options.RelativeTo)
	if base == "" {
		base = strings.TrimSpace(field.options.Root)
	}
	if base != "" {
		return filepath.Abs(filepath.Join(base, value))
	}
	return filepath.Abs(value)
}

func (field *PathField) validatePath(value string) error {
	resolved, err := field.resolvedPath(value)
	if err != nil {
		return err
	}
	if resolved == "" {
		if field.options.Validate != nil {
			return field.options.Validate(value)
		}
		return nil
	}
	if root := strings.TrimSpace(field.options.Root); root != "" {
		inside, err := pathWithinRoot(root, resolved)
		if err != nil {
			return err
		}
		if !inside {
			return fmt.Errorf("path must stay within %s", filepath.Clean(root))
		}
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		if !field.options.AllowMissing || !os.IsNotExist(statErr) {
			return statErr
		}
	} else if field.options.Kind == PathKindDirectory && !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	} else if field.options.Kind == PathKindFile && !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file")
	}
	if field.options.Validate != nil {
		return field.options.Validate(value)
	}
	return nil
}

func (field *PathField) normalizePickerValue() {
	if field == nil || field.value == nil || strings.TrimSpace(*field.value) == "" || strings.TrimSpace(field.options.RelativeTo) == "" {
		return
	}
	resolved, err := field.resolvedPath(*field.value)
	if err != nil {
		return
	}
	base, err := filepath.Abs(field.options.RelativeTo)
	if err != nil {
		return
	}
	relative, err := filepath.Rel(base, resolved)
	if err == nil && relative != "." && relative != "" && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		*field.value = relative
		field.syncValue()
	}
}

func pathWithinRoot(root, target string) (bool, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false, err
	}
	rootAbs = evalSymlinksAllowMissing(rootAbs)
	targetAbs = evalSymlinksAllowMissing(targetAbs)
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false, err
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func evalSymlinksAllowMissing(path string) string {
	original := filepath.Clean(path)
	current := original
	parts := []string{}
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return original
		}
		parts = append(parts, filepath.Base(current))
		current = parent
	}
}
