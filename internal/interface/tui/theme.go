package tui

import (
	"charm.land/bubbles/v2/list"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type theme struct {
	title       lipgloss.Style
	accent      lipgloss.Style
	muted       lipgloss.Style
	subtle      lipgloss.Style
	border      lipgloss.Style
	current     lipgloss.Style
	navActive   lipgloss.Style
	navInactive lipgloss.Style
}

func newTheme(isDark bool) theme {
	listStyles := list.DefaultStyles(isDark)
	huhStyles := huh.ThemeCharm(isDark)
	accent := lipgloss.NewStyle().Foreground(huhStyles.Focused.SelectSelector.GetForeground())
	return theme{
		title: huhStyles.Focused.Title, accent: accent, muted: huhStyles.Focused.Description,
		subtle: lipgloss.NewStyle().Foreground(listStyles.NoItems.GetForeground()), border: huhStyles.Focused.Base,
		current: accent.Bold(true), navActive: listStyles.Title, navInactive: lipgloss.NewStyle(),
	}
}

func centerOverlay(background, foreground string, width, height int) string {
	if width <= 0 {
		width = max(1, lipgloss.Width(background))
	}
	if height <= 0 {
		height = max(1, lipgloss.Height(background))
	}
	x := max(0, (width-lipgloss.Width(foreground))/2)
	y := max(0, (height-lipgloss.Height(foreground))/2)
	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(background).X(0).Y(0).Z(0), lipgloss.NewLayer(foreground).X(x).Y(y).Z(1)))
	return canvas.Render()
}
