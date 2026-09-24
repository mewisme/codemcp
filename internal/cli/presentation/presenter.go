package presentation

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"charm.land/glamour/v2"
)

type ResultMode uint8

const (
	ModeHuman ResultMode = iota
	ModePlain
	ModeJSON
)

type Presenter struct {
	out          io.Writer
	mode         ResultMode
	capabilities Capabilities
	theme        Theme
	glyphs       GlyphSet
	emitted      bool
	gap          bool
}

type Field struct {
	Label string
	Value any
}

type Row []string

type StatusKind uint8

const (
	StatusInfo StatusKind = iota
	StatusSuccess
	StatusWarning
	StatusError
	StatusInactive
)

func New(out io.Writer, mode ResultMode, capabilities Capabilities) *Presenter {
	if out == nil {
		out = io.Discard
	}
	return &Presenter{
		out:          out,
		mode:         mode,
		capabilities: capabilities,
		theme:        NewTheme(capabilities),
		glyphs:       Glyphs(capabilities),
	}
}

func (p *Presenter) Intro(title string) {
	p.Frame(title)
}

func (p *Presenter) Outro(message string) {
	p.FrameEnd(message)
}

func (p *Presenter) Frame(title string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	if p.mode == ModeHuman {
		p.line(p.theme.Render(RoleRail, p.glyphs.FrameStart) + "  " + p.theme.Render(RoleHeading, title))
		p.Spacer()
		return
	}
	p.line(p.theme.Render(RoleHeading, title))
	p.Spacer()
}

func (p *Presenter) FrameEnd(message string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	message = strings.TrimSpace(message)
	p.Spacer()
	if p.mode == ModeHuman {
		line := p.theme.Render(RoleRail, p.glyphs.FrameEnd)
		if message != "" {
			line += "  " + message
		}
		p.line(line)
		return
	}
	if message != "" {
		p.line(message)
	}
}

func (p *Presenter) Section(title string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	if strings.TrimSpace(title) == "" {
		return
	}
	p.beginBlock()
	if p.mode == ModeHuman {
		p.line(p.theme.Render(RoleStructure, p.glyphs.PhaseDone) + "  " + p.theme.Render(RoleHeading, strings.TrimSpace(title)))
		return
	}
	p.line(p.theme.Render(RoleHeading, strings.TrimSpace(title)))
}

func (p *Presenter) StateSection(kind StatusKind, title string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	p.beginBlock()
	if p.mode != ModeHuman {
		p.Status(kind, title)
		return
	}
	glyph, role := p.statusStyle(kind)
	p.line(p.theme.Render(role, glyph) + "  " + p.theme.Render(RoleHeading, title))
}

func (p *Presenter) Subsection(title string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	p.beginBlock()
	if p.mode == ModeHuman {
		p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleStructure, p.glyphs.PhaseDone) + " " + p.theme.Render(RoleHeading, title))
		return
	}
	p.line("  " + p.theme.Render(RoleHeading, title))
}

func (p *Presenter) Spacer() {
	if p == nil || p.mode == ModeJSON {
		return
	}
	if p.gap {
		return
	}
	if p.mode == ModeHuman {
		p.line(p.theme.Render(RoleRail, p.glyphs.Rail))
		p.gap = true
		return
	}
	p.line("")
	p.gap = true
}

func (p *Presenter) Separator() string {
	if p == nil {
		return ""
	}
	return p.glyphs.Separator
}

func (p *Presenter) Status(kind StatusKind, message string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	p.beginBlock()
	glyph, role := p.statusStyle(kind)
	if p.mode == ModeHuman {
		p.line(p.theme.Render(role, glyph) + "  " + message)
		return
	}
	p.line(p.theme.Render(role, glyph) + " " + message)
}

func (p *Presenter) ChildStatus(kind StatusKind, message string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	if p.mode != ModeHuman {
		p.Status(kind, message)
		return
	}
	glyph, role := p.statusStyle(kind)
	p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(role, glyph) + " " + message)
}

func (p *Presenter) ChildState(kind StatusKind, label string, value any) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	label = strings.TrimSpace(label)
	if p.mode != ModeHuman {
		p.Status(kind, strings.TrimSpace(label+" is "+fmt.Sprint(value)))
		return
	}
	glyph, role := p.statusStyle(kind)
	p.richField(p.theme.Render(role, glyph), label, value, false)
}

func (p *Presenter) Fields(fields ...Field) {
	p.fields(2, fields...)
}

func (p *Presenter) NestedFields(fields ...Field) {
	p.fields(4, fields...)
}

func (p *Presenter) fields(indent int, fields ...Field) {
	if p == nil || p.mode == ModeJSON || len(fields) == 0 {
		return
	}
	if p.mode == ModeHuman {
		for _, field := range fields {
			if indent >= 4 {
				p.richField("", field.Label, field.Value, true)
				continue
			}
			p.richField(p.theme.Render(RoleStructure, p.glyphs.PhaseDone), field.Label, field.Value, false)
		}
		return
	}
	labelWidth := 0
	for _, field := range fields {
		if width := utf8.RuneCountInString(strings.TrimSpace(field.Label)); width > labelWidth {
			labelWidth = width
		}
	}
	stacked := labelWidth+4 >= p.capabilities.Width/2
	for _, field := range fields {
		label := strings.TrimSpace(field.Label)
		value := fmt.Sprint(field.Value)
		if stacked {
			p.line(strings.Repeat(" ", indent) + p.theme.Render(RoleLabel, label))
			for _, line := range strings.Split(value, "\n") {
				p.line(strings.Repeat(" ", indent+2) + line)
			}
			continue
		}
		padding := strings.Repeat(" ", max(1, labelWidth-utf8.RuneCountInString(label)+2))
		p.line(strings.Repeat(" ", indent) + p.theme.Render(RoleLabel, label) + padding + value)
	}
}

func (p *Presenter) List(items ...string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	for _, item := range items {
		if p.mode == ModeHuman {
			lines := strings.Split(strings.TrimSpace(item), "\n")
			if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
				continue
			}
			p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleStructure, p.glyphs.PhaseDone) + " " + lines[0])
			for _, line := range lines[1:] {
				p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + line)
			}
			continue
		}
		p.line("  " + p.glyphs.Info + " " + strings.TrimSpace(item))
	}
}

func (p *Presenter) Rows(headers []string, rows ...Row) {
	if p == nil || p.mode == ModeJSON || len(rows) == 0 {
		return
	}
	if p.mode == ModeHuman {
		for _, row := range rows {
			if len(row) == 0 {
				continue
			}
			if len(row) == 2 {
				p.richField(p.theme.Render(RoleStructure, p.glyphs.PhaseDone), row[0], row[1], false)
				continue
			}
			p.richTextChild(row[0])
			for i := 1; i < len(row); i++ {
				label := fmt.Sprintf("column %d", i+1)
				if i < len(headers) && strings.TrimSpace(headers[i]) != "" {
					label = headers[i]
				}
				p.richField("", label, row[i], true)
			}
		}
		return
	}
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = utf8.RuneCountInString(header)
	}
	for _, row := range rows {
		for i, value := range row {
			if i >= len(widths) {
				break
			}
			if width := utf8.RuneCountInString(value); width > widths[i] {
				widths[i] = width
			}
		}
	}
	total := 0
	for _, width := range widths {
		total += width + 2
	}
	if total > p.capabilities.Width {
		for _, row := range rows {
			fields := make([]Field, 0, len(row))
			for i, value := range row {
				label := fmt.Sprintf("column %d", i+1)
				if i < len(headers) && strings.TrimSpace(headers[i]) != "" {
					label = headers[i]
				}
				fields = append(fields, Field{Label: label, Value: value})
			}
			p.Fields(fields...)
		}
		return
	}
	if len(headers) > 0 {
		p.line(formatRow(headers, widths, true, p.theme))
	}
	for _, row := range rows {
		p.line(formatRow([]string(row), widths, false, p.theme))
	}
}

func (p *Presenter) Note(title, body string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	p.beginBlock()
	if p.mode == ModeHuman {
		if title != "" {
			p.line(p.theme.Render(RoleMuted, p.glyphs.Info) + "  " + p.theme.Render(RoleHeading, title))
		}
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + line)
		}
		return
	}
	if title != "" {
		p.line(p.theme.Render(RoleHeading, title))
	}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		p.line("  " + line)
	}
}

func (p *Presenter) Markdown(source string) error {
	if p == nil || p.mode == ModeJSON {
		return nil
	}
	p.beginBlock()
	width := p.capabilities.Width
	if p.mode == ModeHuman {
		width = max(20, width-3)
	}
	options := []glamour.TermRendererOption{glamour.WithWordWrap(width)}
	if !p.capabilities.Color {
		options = append(options, glamour.WithStandardStyle("ascii"))
	} else {
		options = append(options, glamour.WithStandardStyle("dark"))
	}
	renderer, err := glamour.NewTermRenderer(options...)
	if err != nil {
		return err
	}
	output, err := renderer.Render(strings.TrimSpace(source) + "\n")
	if err != nil {
		return err
	}
	if p.mode != ModeHuman {
		_, err = io.WriteString(p.out, output)
		return err
	}
	output = strings.TrimRight(output, "\n")
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			p.line(p.theme.Render(RoleRail, p.glyphs.Rail))
			continue
		}
		p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + line)
	}
	return nil
}

func (p *Presenter) statusStyle(kind StatusKind) (string, Role) {
	switch kind {
	case StatusSuccess:
		return p.glyphs.Success, RoleSuccess
	case StatusWarning:
		return p.glyphs.Warning, RoleWarning
	case StatusError:
		return p.glyphs.Error, RoleDanger
	case StatusInactive:
		return p.glyphs.PhasePending, RoleMuted
	default:
		return p.glyphs.Info, RoleMuted
	}
}

func (p *Presenter) richField(glyph, label string, value any, continuation bool) {
	label = strings.TrimSpace(label)
	lines := strings.Split(fmt.Sprint(value), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	prefix := p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
	if continuation {
		prefix += p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
	} else {
		prefix += glyph + " "
	}
	line := prefix
	if label != "" {
		line += p.theme.Render(RoleLabel, label)
		if lines[0] != "" {
			line += " " + p.fieldSeparator() + " " + lines[0]
		}
	} else {
		line += lines[0]
	}
	p.line(line)
	continuationPrefix := p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
	for _, continuationLine := range lines[1:] {
		p.line(continuationPrefix + continuationLine)
	}
}

func (p *Presenter) richTextChild(value string) {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return
	}
	p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleStructure, p.glyphs.PhaseDone) + " " + lines[0])
	for _, line := range lines[1:] {
		p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + line)
	}
}

func (p *Presenter) fieldSeparator() string {
	if p.capabilities.Unicode {
		return "—"
	}
	return "-"
}

func (p *Presenter) line(value string) {
	_, _ = fmt.Fprintln(p.out, value)
	p.emitted = true
	p.gap = false
}

func (p *Presenter) beginBlock() {
	if p == nil || !p.emitted || p.gap {
		return
	}
	p.Spacer()
}

func formatRow(values []string, widths []int, heading bool, theme Theme) string {
	parts := make([]string, 0, len(widths))
	for i, width := range widths {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		padding := width - utf8.RuneCountInString(value)
		if padding < 0 {
			padding = 0
		}
		if heading {
			value = theme.Render(RoleLabel, value)
		}
		parts = append(parts, value+strings.Repeat(" ", padding))
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}
