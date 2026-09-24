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
	if p == nil || p.mode == ModeJSON {
		return
	}
	p.line(p.theme.Render(RoleHeading, strings.TrimSpace(title)))
}

func (p *Presenter) Outro(message string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	p.line(strings.TrimSpace(message))
}

func (p *Presenter) Section(title string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	if strings.TrimSpace(title) == "" {
		return
	}
	p.line(p.theme.Render(RoleHeading, strings.TrimSpace(title)))
}

func (p *Presenter) Subsection(title string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	p.line("  " + p.theme.Render(RoleHeading, title))
}

func (p *Presenter) Spacer() {
	if p == nil || p.mode == ModeJSON {
		return
	}
	p.line("")
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
	glyph, role := p.statusStyle(kind)
	p.line(p.theme.Render(role, glyph) + " " + message)
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
		p.line("  " + p.glyphs.Info + " " + strings.TrimSpace(item))
	}
}

func (p *Presenter) Rows(headers []string, rows ...Row) {
	if p == nil || p.mode == ModeJSON || len(rows) == 0 {
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
	options := []glamour.TermRendererOption{glamour.WithWordWrap(p.capabilities.Width)}
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
	_, err = io.WriteString(p.out, output)
	return err
}

func (p *Presenter) statusStyle(kind StatusKind) (string, Role) {
	switch kind {
	case StatusSuccess:
		return p.glyphs.Success, RoleSuccess
	case StatusWarning:
		return p.glyphs.Warning, RoleWarning
	case StatusError:
		return p.glyphs.Error, RoleDanger
	default:
		return p.glyphs.Info, RoleAccent
	}
}

func (p *Presenter) line(value string) {
	_, _ = fmt.Fprintln(p.out, value)
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
