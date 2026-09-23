package page

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type logFrameField struct {
	Label  string
	Values []string
}

func renderLogBlock(topLabel, bottomLabel, idLabel, id string, headerFields []logFrameField, content []string, footerFields []logFrameField, width int) string {
	if width <= 0 {
		return ""
	}
	if width < 4 {
		return strings.Repeat("─", width) + "\n"
	}
	innerWidth := max(1, width-4)
	topLabel = ansi.Truncate(strings.TrimSpace(topLabel), max(0, width-6), "…")
	bottomLabel = ansi.Truncate(strings.TrimSpace(bottomLabel), max(0, width-6), "…")
	var output strings.Builder
	topUsed := 4 + lipgloss.Width(topLabel)
	output.WriteString("╭─ " + topLabel + " " + strings.Repeat("─", max(0, width-topUsed-1)) + "╮\n")
	fields := headerFields
	if strings.TrimSpace(id) != "" {
		fields = append([]logFrameField{{Label: idLabel, Values: []string{id}}}, headerFields...)
	}
	for _, field := range fields {
		for _, line := range wrappedLogFrameLines(field.Label, field.Values, innerWidth) {
			output.WriteString("│ " + line + strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(line))) + " │\n")
		}
	}
	output.WriteString("├" + strings.Repeat("─", width-2) + "┤\n")
	for _, raw := range content {
		for _, line := range strings.Split(component.WrapContent(sanitizeLogContent(raw), innerWidth), "\n") {
			output.WriteString("│ " + line + strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(line))) + " │\n")
		}
	}
	output.WriteString("├" + strings.Repeat("─", width-2) + "┤\n")
	for _, field := range footerFields {
		for _, line := range wrappedLogFrameLines(field.Label, field.Values, innerWidth) {
			output.WriteString("│ " + line + strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(line))) + " │\n")
		}
	}
	bottomUsed := 4 + lipgloss.Width(bottomLabel)
	output.WriteString("╰─ " + bottomLabel + " " + strings.Repeat("─", max(0, width-bottomUsed-1)) + "╯\n")
	return output.String()
}

func wrappedLogFrameLines(label string, values []string, width int) []string {
	label = sanitizeExecutionInline(label)
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = sanitizeExecutionInline(value)
		if value != "" {
			clean = append(clean, value)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	if len(clean) == 1 {
		prefix := label + "  "
		if lipgloss.Width(prefix) >= width {
			bullet := "  • "
			available := max(1, width-lipgloss.Width(bullet))
			wrapped := strings.Split(component.WrapContent(clean[0], available), "\n")
			result := []string{ansi.Truncate(label, width, "")}
			for index, line := range wrapped {
				if index == 0 {
					result = append(result, bullet+line)
				} else {
					result = append(result, strings.Repeat(" ", lipgloss.Width(bullet))+line)
				}
			}
			return result
		}
		available := max(1, width-lipgloss.Width(prefix))
		wrapped := strings.Split(component.WrapContent(clean[0], available), "\n")
		result := make([]string, 0, len(wrapped))
		for index, line := range wrapped {
			if index == 0 {
				result = append(result, prefix+line)
			} else {
				result = append(result, strings.Repeat(" ", lipgloss.Width(prefix))+line)
			}
		}
		return result
	}
	result := []string{label}
	for _, value := range clean {
		prefix := "  • "
		available := max(1, width-lipgloss.Width(prefix))
		wrapped := strings.Split(component.WrapContent(value, available), "\n")
		for index, line := range wrapped {
			if index == 0 {
				result = append(result, prefix+line)
			} else {
				result = append(result, strings.Repeat(" ", lipgloss.Width(prefix))+line)
			}
		}
	}
	return result
}

func sanitizeLogContent(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\x1b' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}
