package presentation

import (
	"fmt"
	"strings"
)

type TableBorderStyle uint8

const (
	TableBare TableBorderStyle = iota
	TableOutline
	TableGrid
)

type TableLayoutPolicy uint8

const (
	TableAdaptive TableLayoutPolicy = iota
	TableFitContent
)

type TableOptions struct {
	Border TableBorderStyle
	Layout TableLayoutPolicy
	Depth  int
	Widths []int
}

func (p *Presenter) Table(headers []string, rows []Row, options TableOptions) {
	if p == nil || p.mode == ModeJSON || len(rows) == 0 {
		return
	}
	if p.mode != ModeHuman {
		p.renderPlainTable(headers, rows)
		return
	}
	p.beginContent()
	depth := options.Depth
	if depth < 1 {
		depth = 1
	}
	widths := append([]int(nil), options.Widths...)
	if len(widths) != len(headers) {
		widths = AlignedRowWidths(headers, rows...)
	}
	if len(widths) == 0 {
		maxColumns := 0
		for _, row := range rows {
			maxColumns = max(maxColumns, len(row))
		}
		widths = make([]int, maxColumns)
		for _, row := range rows {
			for index, value := range row {
				widths[index] = max(widths[index], displayWidth(strings.TrimSpace(value)))
			}
		}
	}
	prefix := p.railPrefix(depth)
	available := effectiveLayoutWidth(p.capabilities.Width) - displayWidth(prefix)
	if available < minimumLayoutContentWidth {
		available = minimumLayoutContentWidth
	}
	intrinsic := tableIntrinsicWidth(widths, options.Border)
	if intrinsic > available {
		if options.Layout == TableAdaptive && options.Border == TableBare && len(widths) == 3 {
			const minimumLastColumnWidth = 16
			fixed := widths[0] + 2 + widths[1] + 2
			if fixed+minimumLastColumnWidth <= available {
				adaptive := append([]int(nil), widths...)
				adaptive[2] = max(minimumLastColumnWidth, available-fixed)
				p.renderAdaptiveBareTable(headers, rows, adaptive, depth)
				return
			}
		}
		p.renderStackedTable(headers, rows, depth)
		return
	}
	switch options.Border {
	case TableOutline:
		p.renderBoxTable(headers, rows, widths, depth, false)
	case TableGrid:
		p.renderBoxTable(headers, rows, widths, depth, true)
	default:
		p.renderBareTable(headers, rows, widths, depth)
	}
}

func (p *Presenter) renderPlainTable(headers []string, rows []Row) {
	widths := AlignedRowWidths(headers, rows...)
	if len(widths) == 0 {
		return
	}
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		fields := make([]Field, 0, len(row))
		for index, value := range row {
			label := fmt.Sprintf("column %d", index+1)
			if index < len(headers) && strings.TrimSpace(headers[index]) != "" {
				label = strings.TrimSpace(headers[index])
			}
			fields = append(fields, Field{Label: label, Value: value})
		}
		p.Fields(fields...)
	}
}

func (p *Presenter) renderBareTable(headers []string, rows []Row, widths []int, depth int) {
	if len(headers) > 0 {
		p.line(p.railPrefix(depth) + p.formatTableRow(headers, headers, widths, true, TableBare))
	}
	for _, row := range rows {
		p.line(p.railPrefix(depth) + p.formatTableRow(headers, []string(row), widths, false, TableBare))
	}
}

func (p *Presenter) renderAdaptiveBareTable(headers []string, rows []Row, widths []int, depth int) {
	prefix := p.railPrefix(depth)
	p.line(prefix + p.formatTableRow(headers, headers, widths, true, TableBare))
	offset := widths[0] + 2 + widths[1] + 2
	continuation := prefix + strings.Repeat(" ", offset)
	for _, row := range rows {
		values := make([]string, 3)
		for index := range values {
			if index < len(row) {
				values[index] = strings.TrimSpace(row[index])
			}
		}
		parts := wrapDisplayWords(values[2], widths[2])
		if len(parts) == 0 {
			parts = []string{""}
		}
		values[2] = parts[0]
		p.line(prefix + p.formatTableRow(headers, values, widths, false, TableBare))
		for _, part := range parts[1:] {
			p.line(continuation + p.theme.Render(RoleMuted, part))
		}
	}
}

func (p *Presenter) renderBoxTable(headers []string, rows []Row, widths []int, depth int, grid bool) {
	prefix := p.railPrefix(depth)
	top, middle, bottom, vertical := p.tableBorderGlyphs()
	p.line(prefix + buildTableBorder(widths, top[0], top[1], top[2]))
	if len(headers) > 0 {
		p.line(prefix + vertical + " " + p.formatTableRow(headers, headers, widths, true, TableOutline) + " " + vertical)
		p.line(prefix + buildTableBorder(widths, middle[0], middle[1], middle[2]))
	}
	for index, row := range rows {
		p.line(prefix + vertical + " " + p.formatTableRow(headers, []string(row), widths, false, TableOutline) + " " + vertical)
		if grid && index < len(rows)-1 {
			p.line(prefix + buildTableBorder(widths, middle[0], middle[1], middle[2]))
		}
	}
	p.line(prefix + buildTableBorder(widths, bottom[0], bottom[1], bottom[2]))
}

func (p *Presenter) renderStackedTable(headers []string, rows []Row, depth int) {
	for rowIndex, row := range rows {
		if len(row) == 0 {
			continue
		}
		last := rowIndex == len(rows)-1
		branch := p.glyphs.Branch
		continuation := p.railPrefix(depth) + p.theme.Render(RoleRail, p.glyphs.Rail) + "  "
		if last {
			branch = p.glyphs.LastBranch
			continuation = p.railPrefix(depth) + "   "
		}
		first := strings.TrimSpace(row[0])
		p.emitWrapped(
			p.railPrefix(depth)+p.theme.Render(RoleStructure, branch),
			continuation,
			first,
			func(value string) string { return p.theme.Render(RoleLabel, value) },
		)
		for column := 1; column < len(row); column++ {
			label := fmt.Sprintf("column %d", column+1)
			if column < len(headers) && strings.TrimSpace(headers[column]) != "" {
				label = strings.TrimSpace(headers[column])
			}
			p.renderFieldDepth("", label, row[column], depth+1)
		}
	}
}

func tableIntrinsicWidth(widths []int, border TableBorderStyle) int {
	total := 0
	for _, width := range widths {
		total += width
	}
	if border == TableBare {
		return total + max(0, len(widths)-1)*2
	}
	if len(widths) == 0 {
		return 0
	}
	return total + 3*len(widths) + 1
}

func (p *Presenter) formatTableRow(headers, values []string, widths []int, header bool, border TableBorderStyle) string {
	parts := make([]string, len(widths))
	separator := "  "
	if border != TableBare {
		separator = " │ "
		if !p.capabilities.Unicode {
			separator = " | "
		}
	}
	for index, width := range widths {
		value := ""
		if index < len(values) {
			value = strings.TrimSpace(values[index])
		}
		padding := max(0, width-displayWidth(value))
		rendered := value
		if header {
			rendered = p.theme.Render(RoleHeading, value)
		} else if index == 0 {
			rendered = p.theme.Render(RoleLabel, value)
		} else if index < len(headers) && strings.EqualFold(strings.TrimSpace(headers[index]), "Accepts") {
			rendered = p.theme.Render(RoleMuted, value)
		}
		parts[index] = rendered + strings.Repeat(" ", padding)
	}
	return strings.Join(parts, separator)
}

func buildTableBorder(widths []int, left, join, right string) string {
	parts := make([]string, len(widths))
	for index, width := range widths {
		parts[index] = strings.Repeat("─", width+2)
	}
	if left == "+" {
		for index, width := range widths {
			parts[index] = strings.Repeat("-", width+2)
		}
	}
	return left + strings.Join(parts, join) + right
}

func (p *Presenter) tableBorderGlyphs() ([3]string, [3]string, [3]string, string) {
	if !p.capabilities.Unicode {
		return [3]string{"+", "+", "+"}, [3]string{"+", "+", "+"}, [3]string{"+", "+", "+"}, "|"
	}
	return [3]string{"┌", "┬", "┐"}, [3]string{"├", "┼", "┤"}, [3]string{"└", "┴", "┘"}, "│"
}
