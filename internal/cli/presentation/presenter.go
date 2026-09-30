package presentation

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
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
	session      *ProgressSession
	emitted      bool
	gap          bool
	contentGap   bool
}

// AlignedRows renders a compact table in human mode when it fits the terminal.
// When a three-column table does not fit, the final column becomes a clearly
// indented continuation line. Plain mode remains append-only and deterministic.
func (p *Presenter) AlignedRows(headers []string, rows ...Row) {
	p.alignedRows(headers, AlignedRowWidths(headers, rows...), false, rows...)
}

// AlignedNestedRowsWithWidths renders rows one list level below the current
// subsection while reusing a width set measured across a larger row set.
func (p *Presenter) AlignedNestedRowsWithWidths(headers []string, widths []int, rows ...Row) {
	p.alignedRows(headers, widths, true, rows...)
}

// AlignedRowWidths returns visible display widths for a table. Callers may
// measure once across multiple logical groups and reuse the result per group.
func AlignedRowWidths(headers []string, rows ...Row) []int {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = ansi.StringWidth(strings.TrimSpace(header))
	}
	for _, row := range rows {
		for index := 0; index < len(row) && index < len(widths); index++ {
			if width := ansi.StringWidth(row[index]); width > widths[index] {
				widths[index] = width
			}
		}
	}
	return widths
}

func (p *Presenter) alignedRows(headers []string, widths []int, nested bool, rows ...Row) {
	if p == nil || p.mode == ModeJSON || len(rows) == 0 {
		return
	}
	p.beginContent()
	if p.mode != ModeHuman {
		p.Rows(headers, rows...)
		return
	}
	columns := len(headers)
	if columns == 0 {
		return
	}
	if len(widths) != columns {
		widths = AlignedRowWidths(headers, rows...)
	}
	prefixWidth := ansi.StringWidth(p.alignedRowPrefix(nested))
	available := p.capabilities.Width - prefixWidth
	if available < 20 {
		available = 20
	}
	total := 0
	for _, width := range widths {
		total += width
	}
	total += max(0, columns-1) * 2
	if total <= available {
		p.alignedHumanLine(headers, headers, widths, true, nested)
		for _, row := range rows {
			p.alignedHumanLine(headers, []string(row), widths, false, nested)
		}
		return
	}
	if columns == 3 {
		const minimumAcceptWidth = 16
		fixed := widths[0] + 2 + widths[1] + 2
		if fixed+minimumAcceptWidth <= available {
			wrappedWidths := append([]int(nil), widths...)
			wrappedWidths[2] = min(widths[2], available-fixed)
			p.alignedHumanLine(headers, headers, wrappedWidths, true, nested)
			for _, row := range rows {
				p.alignedHumanWrappedRow(headers, row, wrappedWidths, nested)
			}
			return
		}
	}
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		key := row[0]
		value := ""
		if len(row) > 1 {
			value = row[1]
		}
		rowPrefixWidth := prefixWidth
		if rowPrefixWidth+ansi.StringWidth(key)+2+ansi.StringWidth(value) <= p.capabilities.Width {
			p.line(p.alignedRowPrefix(nested) + p.theme.Render(RoleLabel, key) + "  " + value)
		} else {
			p.line(p.alignedRowPrefix(nested) + p.theme.Render(RoleLabel, key))
			if strings.TrimSpace(value) != "" {
				p.alignedContinuation("value", value, nested, false)
			}
		}
		if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
			label := "accepts"
			if len(headers) > 2 && strings.TrimSpace(headers[2]) != "" {
				label = strings.ToLower(strings.TrimSpace(headers[2]))
			}
			p.alignedContinuation(label, row[2], nested, true)
		}
	}
}

func (p *Presenter) alignedHumanWrappedRow(headers []string, row Row, widths []int, nested bool) {
	if len(widths) != 3 {
		p.alignedHumanLine(headers, []string(row), widths, false, nested)
		return
	}
	values := []string{"", "", ""}
	for index := range values {
		if index < len(row) {
			values[index] = strings.TrimSpace(row[index])
		}
	}
	acceptParts := wrapDisplayWords(values[2], widths[2])
	if len(acceptParts) == 0 {
		acceptParts = []string{""}
	}
	values[2] = acceptParts[0]
	p.alignedHumanLine(headers, values, widths, false, nested)
	acceptOffset := widths[0] + 2 + widths[1] + 2
	for _, part := range acceptParts[1:] {
		p.line(p.alignedRowPrefix(nested) + strings.Repeat(" ", acceptOffset) + p.theme.Render(RoleMuted, part))
	}
}

func (p *Presenter) alignedContinuation(label, value string, nested, dimValue bool) {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	basePrefix := p.alignedRowPrefix(nested)
	plainPrefix := basePrefix + "  " + label + ": "
	continuationPrefix := basePrefix + strings.Repeat(" ", max(2, ansi.StringWidth(plainPrefix)-ansi.StringWidth(basePrefix)))
	contentWidth := p.capabilities.Width - ansi.StringWidth(plainPrefix)
	if contentWidth < 8 {
		contentWidth = 8
	}
	parts := wrapDisplayWords(value, contentWidth)
	for index, part := range parts {
		if dimValue {
			part = p.theme.Render(RoleMuted, part)
		}
		if index == 0 {
			p.line(basePrefix + "  " + p.theme.Render(RoleMuted, label+":") + " " + part)
			continue
		}
		p.line(continuationPrefix + part)
	}
}

func wrapDisplayWords(value string, width int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if width <= 0 || ansi.StringWidth(value) <= width {
		return []string{value}
	}
	words := strings.Fields(value)
	lines := make([]string, 0, len(words))
	current := ""
	for _, word := range words {
		if ansi.StringWidth(word) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			for ansi.StringWidth(word) > width {
				cut := displayPrefix(word, width)
				lines = append(lines, cut)
				word = strings.TrimPrefix(word, cut)
			}
			current = word
			continue
		}
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if ansi.StringWidth(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func displayPrefix(value string, width int) string {
	if width <= 0 {
		return ""
	}
	var builder strings.Builder
	used := 0
	for _, r := range value {
		piece := string(r)
		pieceWidth := ansi.StringWidth(piece)
		if used+pieceWidth > width {
			break
		}
		builder.WriteRune(r)
		used += pieceWidth
	}
	if builder.Len() == 0 {
		_, size := utf8.DecodeRuneInString(value)
		return value[:size]
	}
	return builder.String()
}

func (p *Presenter) alignedHumanLine(headers, values []string, widths []int, header, nested bool) {
	parts := make([]string, len(widths))
	for index := range widths {
		value := ""
		if index < len(values) {
			value = strings.TrimSpace(values[index])
		}
		padding := widths[index] - ansi.StringWidth(value)
		if index < len(widths)-1 {
			padding += 2
		}
		if padding < 0 {
			padding = 0
		}
		if header {
			value = p.theme.Render(RoleHeading, value)
		} else if index == 0 {
			value = p.theme.Render(RoleLabel, value)
		} else if index < len(headers) && strings.EqualFold(strings.TrimSpace(headers[index]), "Accepts") {
			value = p.theme.Render(RoleMuted, value)
		}
		parts[index] = value + strings.Repeat(" ", padding)
	}
	p.line(p.alignedRowPrefix(nested) + strings.Join(parts, ""))
}

func (p *Presenter) alignedRowPrefix(nested bool) string {
	prefix := p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
	if nested {
		prefix += p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
	}
	return prefix
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

func newSessionPresenter(session *ProgressSession) *Presenter {
	if session == nil {
		return New(io.Discard, ModeJSON, Capabilities{})
	}
	return &Presenter{
		out:          session.out,
		mode:         session.mode,
		capabilities: session.capabilities,
		theme:        session.theme,
		glyphs:       session.glyphs,
		session:      session,
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
	if p.session != nil {
		p.session.Begin(title)
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

func (p *Presenter) Complete(message string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	message = strings.TrimSpace(message)
	if p.session != nil {
		p.session.SetCompletion(message)
		return
	}
	p.FrameEnd(message)
}

func (p *Presenter) FrameEnd(message string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	message = strings.TrimSpace(message)
	if p.session != nil {
		p.session.CloseWith(message)
		return
	}
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
		p.contentGap = true
		return
	}
	p.line(p.theme.Render(RoleHeading, strings.TrimSpace(title)))
	p.contentGap = true
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
	p.contentGap = true
}

func (p *Presenter) Subsection(title string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	p.beginContent()
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
	if p.session != nil {
		p.session.spacer()
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
		p.contentGap = true
		return
	}
	p.line(p.theme.Render(role, glyph) + " " + message)
	p.contentGap = true
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
	p.beginContent()
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
	p.beginContent()
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
	if indent < 4 {
		p.beginContent()
	}
	if p.mode == ModeHuman {
		for _, field := range fields {
			if indent >= 4 {
				p.richField("", field.Label, field.Value, true)
				continue
			}
			p.richField("", field.Label, field.Value, false)
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
	p.beginContent()
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
	p.beginContent()
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
	p.beginContent()
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

func (p *Presenter) Prompt(message string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	p.beginContent()
	p.beginBlock()
	if p.mode == ModeHuman {
		p.line(p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleActive, p.glyphs.PhasePending) + " " + message)
		return
	}
	p.line(message)
}

// ProtectedInput redraws one interactive secret-input line using only mask
// glyphs. Callers own terminal input and pass only the current secret length.
func (p *Presenter) ProtectedInput(label string, maskCount int, final bool) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Secret"
	}
	if maskCount < 0 {
		maskCount = 0
	}
	line := label + ": " + strings.Repeat("*", maskCount)
	if p.mode == ModeHuman {
		line = p.theme.Render(RoleRail, p.glyphs.Rail) + "  " + p.theme.Render(RoleActive, p.glyphs.PhasePending) + " " + line
	}
	if p.capabilities.CursorControl {
		_, _ = fmt.Fprint(p.out, "\r\x1b[2K", line)
	} else if maskCount == 0 {
		_, _ = fmt.Fprint(p.out, line)
	}
	if final {
		_, _ = fmt.Fprintln(p.out)
	}
}

func (p *Presenter) Markdown(source string) error {
	if p == nil || p.mode == ModeJSON {
		return nil
	}
	p.beginContent()
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
	} else if glyph != "" {
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
	if p.session != nil {
		p.session.line(value)
		return
	}
	_, _ = fmt.Fprintln(p.out, value)
	p.emitted = true
	p.gap = false
}

func (p *Presenter) beginBlock() {
	if p == nil {
		return
	}
	if p.contentGap {
		p.Spacer()
		p.contentGap = false
		return
	}
	if p.session != nil {
		p.session.beginBlock()
		return
	}
	if p == nil || !p.emitted || p.gap {
		return
	}
	p.Spacer()
}

func (p *Presenter) beginContent() {
	if p == nil || !p.contentGap {
		return
	}
	p.Spacer()
	p.contentGap = false
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
