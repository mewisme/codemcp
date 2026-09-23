package component

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type BreadcrumbSpan struct {
	Index int
	X     int
	Width int
}

type breadcrumbItem struct {
	index int
	label string
}

func BreadcrumbLayout(labels []string, width int) (string, []BreadcrumbSpan) {
	items := make([]breadcrumbItem, 0, len(labels))
	for index, raw := range labels {
		if label := strings.TrimSpace(raw); label != "" {
			items = append(items, breadcrumbItem{index: index, label: label})
		}
	}
	if len(items) == 0 || width == 0 {
		return "", nil
	}
	if width > 0 && breadcrumbDesiredWidth(items) > width && len(items) > 2 {
		items = []breadcrumbItem{items[0], {index: -1, label: "…"}, items[len(items)-1]}
	}
	widths := breadcrumbCellWidths(items, width)
	parts := make([]string, 0, len(items)*2-1)
	spans := make([]BreadcrumbSpan, 0, len(items))
	x := 0
	active := labelsLastIndex(labels)
	rendered := 0
	for index, item := range items {
		cellWidth := widths[index]
		if cellWidth <= 0 {
			continue
		}
		if rendered > 0 {
			separator := Muted(" / ")
			parts = append(parts, separator)
			x += lipgloss.Width(separator)
		}
		rendered++
		if item.index < 0 {
			cell := ansi.Truncate(Muted(item.label), cellWidth, "")
			parts = append(parts, cell)
			x += lipgloss.Width(cell)
			continue
		}
		cell := breadcrumbCell(item.label, item.index == active, cellWidth)
		parts = append(parts, cell)
		spans = append(spans, BreadcrumbSpan{Index: item.index, X: x, Width: lipgloss.Width(cell)})
		x += lipgloss.Width(cell)
	}
	view := strings.Join(parts, "")
	if width > 0 {
		view = ansi.Truncate(view, width, "")
	}
	return view, spans
}

func breadcrumbDesiredWidth(items []breadcrumbItem) int {
	width := max(0, len(items)-1) * 3
	for _, item := range items {
		if item.index < 0 {
			width++
			continue
		}
		width += lipgloss.Width(item.label) + 2
	}
	return width
}

func breadcrumbCellWidths(items []breadcrumbItem, width int) []int {
	result := make([]int, len(items))
	for index, item := range items {
		if item.index < 0 {
			result[index] = 1
		} else {
			result[index] = lipgloss.Width(item.label) + 2
		}
	}
	if width <= 0 || breadcrumbDesiredWidth(items) <= width {
		return result
	}
	separatorWidth := max(0, len(items)-1) * 3
	available := max(0, width-separatorWidth)
	if len(items) == 1 {
		result[0] = available
		return result
	}
	if len(items) == 3 && items[1].index < 0 {
		if available <= 1 {
			return []int{0, 0, available}
		}
		result[1] = 1
		remaining := available - 1
		if remaining <= 1 {
			return []int{0, 0, available}
		}
		root := min(result[0], max(1, remaining/3))
		result[0], result[2] = root, remaining-root
		return result
	}
	root := min(result[0], max(1, available/3))
	result[0], result[len(result)-1] = root, max(0, available-root)
	for index := 1; index < len(result)-1; index++ {
		result[index] = 0
	}
	return result
}

func breadcrumbCell(label string, active bool, width int) string {
	if width <= 0 {
		return ""
	}
	style := NavItemStyle(active).Padding(0)
	if width == 1 {
		return style.Render(ansi.Truncate(label, 1, ""))
	}
	visible := ansi.Truncate(label, max(1, width-2), "")
	return style.Width(width).Align(lipgloss.Center).Render(visible)
}

func labelsLastIndex(labels []string) int {
	for index := len(labels) - 1; index >= 0; index-- {
		if strings.TrimSpace(labels[index]) != "" {
			return index
		}
	}
	return -1
}
