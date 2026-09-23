package component

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type TabSpan struct {
	Index int
	X     int
	Width int
}

func TabDelta(msg tea.KeyPressMsg) (int, bool) {
	switch msg.String() {
	case "h", "left":
		return -1, true
	case "l", "right":
		return 1, true
	default:
		return 0, false
	}
}

func MoveTab(current, count, delta int) int {
	if count <= 0 {
		return 0
	}
	current = ((current % count) + count) % count
	if delta == 0 {
		return current
	}
	return ((current+delta)%count + count) % count
}

func PageTabsNotice(labels []string, active int, notice string, width int) string {
	view, _ := PageTabsLayout(labels, active, notice, width)
	if view == "" {
		return ""
	}
	return view + "\n"
}

func PageTabsLayout(labels []string, active int, notice string, width int) (string, []TabSpan) {
	if len(labels) == 0 {
		return "", nil
	}
	active = MoveTab(active, len(labels), 0)
	parts := make([]string, 0, len(labels)*2)
	spans := make([]TabSpan, 0, len(labels))
	divider := Muted("│")
	dividerWidth := lipgloss.Width(divider)
	x := 0
	for index, raw := range labels {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		if len(spans) > 0 {
			if width > 0 && x+dividerWidth >= width {
				break
			}
			parts = append(parts, divider)
			x += dividerWidth
		}
		desired := lipgloss.Width(label) + 2
		if width > 0 && x >= width {
			break
		}
		cellWidth := desired
		if width > 0 {
			cellWidth = min(cellWidth, width-x)
		}
		if cellWidth <= 0 {
			break
		}
		visible := ansi.Truncate(label, max(0, cellWidth-2), "")
		parts = append(parts, NavItemStyle(index == active).Padding(0).Width(cellWidth).Align(lipgloss.Center).Render(visible))
		spans = append(spans, TabSpan{Index: index, X: x, Width: cellWidth})
		x += cellWidth
	}
	if notice = strings.TrimSpace(notice); notice != "" && (width <= 0 || x < width) {
		separator := "  "
		text := "· " + notice
		if width > 0 {
			text = ansi.Truncate(text, max(0, width-x-len(separator)), "")
		}
		if text != "" {
			parts = append(parts, separator+Muted(text))
		}
	}
	return strings.Join(parts, ""), spans
}
