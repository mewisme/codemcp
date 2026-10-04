package presentation

import (
	"fmt"
	"io"
	"strings"

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
	sink         *terminalSink
	mode         ResultMode
	capabilities Capabilities
	theme        Theme
	glyphs       GlyphSet
	session      *ProgressSession
	emitted      bool
	gap          bool
	contentGap   bool
}

// AlignedRows preserves the legacy compact-table call surface while delegating
// sizing, nesting, and fallback behavior to the canonical table engine.
func (p *Presenter) AlignedRows(headers []string, rows ...Row) {
	p.Table(headers, rows, TableOptions{
		Border: TableBare,
		Layout: TableAdaptive,
		Depth:  1,
		Widths: AlignedRowWidths(headers, rows...),
	})
}

// AlignedNestedRowsWithWidths renders rows one list level below the current
// subsection while reusing a width set measured across a larger row set.
func (p *Presenter) AlignedNestedRowsWithWidths(headers []string, widths []int, rows ...Row) {
	p.Table(headers, rows, TableOptions{
		Border: TableBare,
		Layout: TableAdaptive,
		Depth:  2,
		Widths: widths,
	})
}

// AlignedRowWidths returns visible display widths for a table. Callers may
// measure once across multiple logical groups and reuse the result per group.
func AlignedRowWidths(headers []string, rows ...Row) []int {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = displayWidth(strings.TrimSpace(header))
	}
	for _, row := range rows {
		for index := 0; index < len(row) && index < len(widths); index++ {
			if width := displayWidth(strings.TrimSpace(row[index])); width > widths[index] {
				widths[index] = width
			}
		}
	}
	return widths
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
	sink := newTerminalSink(out)
	return &Presenter{
		out:          out,
		sink:         sink,
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
		out:          session.sink.out,
		sink:         session.sink,
		mode:         session.mode,
		capabilities: session.capabilities,
		theme:        session.theme,
		glyphs:       session.glyphs,
		session:      session,
	}
}

func (p *Presenter) Err() error {
	if p == nil || p.sink == nil {
		return nil
	}
	return p.sink.err()
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
		p.session.EnsureBegun()
		return
	}
	if p.mode == ModeHuman {
		p.emitWrapped(
			p.theme.Render(RoleRail, p.glyphs.FrameStart)+"  ",
			p.theme.Render(RoleRail, p.glyphs.Rail)+"  ",
			title,
			func(value string) string { return p.theme.Render(RoleHeading, value) },
		)
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
		return
	}
	p.Spacer()
	if p.mode == ModeHuman {
		if message == "" {
			p.line(p.theme.Render(RoleRail, p.glyphs.FrameEnd))
			return
		}
		p.emitWrapped(
			p.theme.Render(RoleRail, p.glyphs.FrameEnd)+"  ",
			"   ",
			message,
			nil,
		)
		return
	}
	if message != "" {
		p.line(message)
	}
}

func (p *Presenter) Section(title string) {
	p.section(title, true)
}

func (p *Presenter) Scope(title string) {
	p.section(title, false)
}

func (p *Presenter) section(title string, contentGap bool) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	p.beginBlock()
	if p.mode == ModeHuman {
		p.emitWrapped(
			p.railPrefix(1)+p.theme.Render(RoleStructure, p.glyphs.Section)+" ",
			p.railPrefix(1)+"  ",
			title,
			func(value string) string { return p.theme.Render(RoleHeading, value) },
		)
		p.contentGap = contentGap
		return
	}
	p.line(p.theme.Render(RoleHeading, title))
	p.contentGap = contentGap
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
	p.emitWrapped(
		p.theme.Render(role, glyph)+"  ",
		strings.Repeat(" ", displayWidth(glyph)+2),
		title,
		func(value string) string { return p.theme.Render(RoleHeading, value) },
	)
	p.contentGap = true
}

func (p *Presenter) Subsection(title string) {
	p.SubsectionItem(title, true)
}

func (p *Presenter) SubsectionItem(title string, last bool) {
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
		branch := p.glyphs.Branch
		continuation := p.railPrefix(1) + p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
		if last {
			branch = p.glyphs.LastBranch
			continuation = p.railPrefix(1) + "   "
		}
		p.emitWrapped(
			p.railPrefix(1)+p.theme.Render(RoleStructure, branch),
			continuation,
			title,
			func(value string) string { return p.theme.Render(RoleHeading, value) },
		)
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
		p.emitWrapped(
			p.theme.Render(role, glyph)+"  ",
			strings.Repeat(" ", displayWidth(glyph)+2),
			message,
			nil,
		)
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
	p.emitWrapped(
		p.railPrefix(1)+p.theme.Render(role, glyph)+" ",
		p.railPrefix(1)+"  ",
		message,
		nil,
	)
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
	p.renderFieldDepth(p.theme.Render(role, glyph), label, value, 1)
}

func (p *Presenter) Fields(fields ...Field) {
	p.fields(2, fields...)
}

func (p *Presenter) NestedFields(fields ...Field) {
	p.fields(4, fields...)
}

func (p *Presenter) NestedFieldGroup(label string, fields ...Field) {
	if p == nil || p.mode == ModeJSON || len(fields) == 0 {
		return
	}
	label = strings.TrimSpace(label)
	if label == "" {
		p.NestedFields(fields...)
		return
	}
	if p.mode == ModeHuman {
		p.renderFieldDepth("", label, "", 2)
		for _, field := range fields {
			p.renderFieldDepth("", field.Label, field.Value, 3)
		}
		return
	}
	p.line(strings.Repeat(" ", 4) + p.theme.Render(RoleLabel, label))
	p.fields(6, fields...)
}

func (p *Presenter) fields(indent int, fields ...Field) {
	if p == nil || p.mode == ModeJSON || len(fields) == 0 {
		return
	}
	if indent < 4 {
		p.beginContent()
	}
	if p.mode == ModeHuman {
		depth := 1
		if indent >= 4 {
			depth = 2
		}
		for _, field := range fields {
			p.renderFieldDepth("", field.Label, field.Value, depth)
		}
		return
	}

	labelWidth := 0
	for _, field := range fields {
		labelWidth = max(labelWidth, displayWidth(strings.TrimSpace(field.Label)))
	}
	stacked := labelWidth+4 >= effectiveLayoutWidth(p.capabilities.Width)/2
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
		padding := strings.Repeat(" ", max(1, labelWidth-displayWidth(label)+2))
		p.line(strings.Repeat(" ", indent) + p.theme.Render(RoleLabel, label) + padding + value)
	}
}

func (p *Presenter) List(items ...string) {
	if p == nil || p.mode == ModeJSON {
		return
	}
	p.beginContent()
	if p.mode != ModeHuman {
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item != "" {
				p.line("  " + p.glyphs.Info + " " + item)
			}
		}
		return
	}
	visible := make([]string, 0, len(items))
	for _, item := range items {
		if value := strings.TrimSpace(item); value != "" {
			visible = append(visible, value)
		}
	}
	for index, item := range visible {
		last := index == len(visible)-1
		branch := p.glyphs.Branch
		continuation := p.railPrefix(1) + p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
		if last {
			branch = p.glyphs.LastBranch
			continuation = p.railPrefix(1) + "   "
		}
		p.emitWrapped(
			p.railPrefix(1)+p.theme.Render(RoleStructure, branch),
			continuation,
			item,
			nil,
		)
	}
}

func (p *Presenter) Rows(headers []string, rows ...Row) {
	p.Table(headers, rows, TableOptions{Border: TableBare, Layout: TableAdaptive, Depth: 1})
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
			p.emitWrapped(
				p.railPrefix(1)+p.theme.Render(RoleMuted, p.glyphs.Info)+" ",
				p.railPrefix(1)+"  ",
				title,
				func(value string) string { return p.theme.Render(RoleHeading, value) },
			)
		}
		for _, logical := range strings.Split(strings.TrimSpace(body), "\n") {
			p.emitWrapped(p.railPrefix(1)+"  ", p.railPrefix(1)+"  ", logical, nil)
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
		p.emitWrapped(
			p.railPrefix(1)+p.theme.Render(RoleActive, p.glyphs.PhasePending)+" ",
			p.railPrefix(1)+"  ",
			message,
			nil,
		)
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
	if p.session != nil {
		p.session.protectedInput(label, maskCount, final)
		return
	}
	line := label + ": " + strings.Repeat("*", maskCount)
	if p.mode == ModeHuman {
		line = p.railPrefix(1) + p.theme.Render(RoleActive, p.glyphs.PhasePending) + " " + line
	}
	if p.capabilities.CursorControl {
		_, _ = p.sink.writeString("\r\x1b[2K" + line)
	} else if maskCount == 0 {
		_, _ = p.sink.writeString(line)
	}
	if final {
		_, _ = p.sink.writeString("\n")
		p.emitted = true
		p.gap = false
	}
}

func (p *Presenter) Markdown(source string) error {
	if p == nil || p.mode == ModeJSON {
		return nil
	}
	p.beginContent()
	p.beginBlock()
	width := effectiveLayoutWidth(p.capabilities.Width)
	if p.mode == ModeHuman {
		width = max(10, width-displayWidth(p.railPrefix(1)+"  "))
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
	output = strings.TrimRight(output, "\n")
	if p.mode != ModeHuman {
		for _, line := range strings.Split(output, "\n") {
			p.line(line)
		}
		return p.Err()
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			p.line(p.theme.Render(RoleRail, p.glyphs.Rail))
			continue
		}
		for _, physical := range hardWrapDisplay(line, width) {
			p.line(p.railPrefix(1) + "  " + physical)
		}
	}
	return p.Err()
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
		return p.glyphs.Info, RoleMuted
	default:
		return p.glyphs.Info, RoleMuted
	}
}

func (p *Presenter) renderFieldDepth(glyph, label string, value any, depth int) {
	if depth < 1 {
		depth = 1
	}
	label = strings.TrimSpace(label)
	labelPrefix := p.railPrefix(depth)
	labelContinuation := labelPrefix + "  "
	if glyph != "" {
		labelPrefix += glyph + " "
		labelContinuation = p.railPrefix(depth) + strings.Repeat(" ", displayWidth(glyph)+1)
	}
	valueText := strings.TrimSpace(fmt.Sprint(value))
	if label != "" && valueText != "" && !strings.Contains(valueText, "\n") {
		separator := " — "
		if !p.capabilities.Unicode {
			separator = " - "
		}
		if displayWidth(labelPrefix)+displayWidth(label)+displayWidth(separator)+displayWidth(valueText) <= effectiveLayoutWidth(p.capabilities.Width) {
			p.line(labelPrefix + p.theme.Render(RoleLabel, label) + p.theme.Render(RoleMuted, separator) + valueText)
			return
		}
	}
	if label != "" {
		p.emitWrapped(
			labelPrefix,
			labelContinuation,
			label,
			func(value string) string { return p.theme.Render(RoleLabel, value) },
		)
	}
	if valueText == "" {
		return
	}
	valuePrefix := p.railPrefix(depth) + "  "
	for _, logical := range strings.Split(valueText, "\n") {
		p.emitWrapped(valuePrefix, valuePrefix, logical, nil)
	}
}

func (p *Presenter) railPrefix(depth int) string {
	if depth < 1 {
		depth = 1
	}
	return strings.Repeat(p.theme.Render(RoleRail, p.glyphs.Rail)+"  ", depth)
}

func (p *Presenter) emitWrapped(prefix, continuation, content string, decorate func(string) string) {
	layout := newLayoutContext(effectiveLayoutWidth(p.capabilities.Width), prefix, continuation)
	for _, line := range layout.render(content, decorate) {
		p.line(line)
	}
}

func (p *Presenter) line(value string) {
	if p.session != nil {
		p.session.line(value)
		return
	}
	if p.sink == nil {
		p.sink = newTerminalSink(p.out)
	}
	p.sink.line(value)
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
	if !p.emitted || p.gap {
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
