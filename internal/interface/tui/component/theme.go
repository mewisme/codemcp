package component

import (
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/huh/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type Tone int

const (
	ToneNeutral Tone = iota
	ToneAccent
	ToneSuccess
	ToneWarning
	ToneDanger
)

type charmTheme struct {
	isDark       bool
	item         list.DefaultItemStyles
	pageTitle    lipgloss.Style
	pageTitleBar lipgloss.Style
	title        lipgloss.Style
	accent       lipgloss.Style
	muted        lipgloss.Style
	subtle       lipgloss.Style
	success      lipgloss.Style
	danger       lipgloss.Style
	panelBorder  lipgloss.Style
	navActive    lipgloss.Style
	navInactive  lipgloss.Style
}

var currentTheme = newCharmTheme(true)

func newCharmTheme(isDark bool) charmTheme {
	listStyles := list.DefaultStyles(isDark)
	itemStyles := list.NewDefaultItemStyles(isDark)
	huhStyles := huh.ThemeCharm(isDark)
	return charmTheme{
		isDark:       isDark,
		item:         itemStyles,
		pageTitle:    listStyles.Title,
		pageTitleBar: listStyles.TitleBar,
		title:        huhStyles.Focused.Title,
		accent:       lipgloss.NewStyle().Foreground(huhStyles.Focused.SelectSelector.GetForeground()),
		muted:        huhStyles.Focused.Description,
		subtle:       lipgloss.NewStyle().Foreground(listStyles.NoItems.GetForeground()),
		success:      lipgloss.NewStyle().Foreground(huhStyles.Focused.SelectedOption.GetForeground()),
		danger:       lipgloss.NewStyle().Foreground(huhStyles.Focused.ErrorMessage.GetForeground()),
		panelBorder:  huhStyles.Focused.Base,
		navActive:    listStyles.Title,
		navInactive:  lipgloss.NewStyle(),
	}
}

func SetDarkBackground(isDark bool) { currentTheme = newCharmTheme(isDark) }

func Title(value string) string { return currentTheme.title.Render(value) }
func Button(value string) string {
	return huh.ThemeCharm(currentTheme.isDark).Focused.FocusedButton.Render(strings.TrimSpace(value))
}
func PageTitle(value string, width int) string {
	title := currentTheme.pageTitle.Render(strings.TrimSpace(value))
	style := currentTheme.pageTitleBar
	if width > 0 {
		inner := max(1, width-style.GetPaddingLeft()-style.GetPaddingRight())
		title = ansi.Truncate(title, inner, "")
		style = style.Width(inner)
	}
	return style.Render(title)
}
func PageTitleNotice(value, notice string, width int) string {
	title := pageTitleNoticeContent(value, notice)
	style := currentTheme.pageTitleBar
	if width > 0 {
		inner := max(1, width-style.GetPaddingLeft()-style.GetPaddingRight())
		title = ansi.Truncate(title, inner, "")
		style = style.Width(inner)
	}
	return style.Render(title)
}
func pageTitleNoticeLine(value, notice string, width int) string {
	title := pageTitleNoticeContent(value, notice)
	style := currentTheme.pageTitleBar.PaddingTop(0).PaddingBottom(0)
	if width > 0 {
		inner := max(1, width-style.GetPaddingLeft()-style.GetPaddingRight())
		title = ansi.Truncate(title, inner, "")
		style = style.Width(inner)
	}
	return style.Render(title)
}
func pageTitleNoticeContent(value, notice string) string {
	title := currentTheme.pageTitle.Render(strings.TrimSpace(value))
	if notice = strings.TrimSpace(notice); notice != "" {
		title += " " + Muted("· "+notice)
	}
	return title
}
func Muted(value string) string { return currentTheme.muted.Render(value) }
func Label(value string) string { return currentTheme.muted.Render(value) }
func NavItemStyle(active bool) lipgloss.Style {
	if active {
		return currentTheme.navActive
	}
	return currentTheme.navInactive
}

func ToneText(value string, tone Tone) string {
	switch tone {
	case ToneAccent, ToneWarning:
		return currentTheme.accent.Render(value)
	case ToneSuccess:
		return currentTheme.success.Render(value)
	case ToneDanger:
		return currentTheme.danger.Render(value)
	default:
		return value
	}
}

func Banner(message string, tone Tone) string { return BannerWidth(message, tone, 0) }

func BannerWidth(message string, tone Tone, width int) string {
	if strings.TrimSpace(message) == "" {
		return ""
	}
	marker := "·"
	if tone == ToneDanger {
		marker = "×"
	} else if tone == ToneWarning {
		marker = "!"
	} else if tone == ToneSuccess {
		marker = "✓"
	}
	prefix := ToneText(marker, tone) + " "
	if width <= 0 {
		return prefix + message
	}
	prefixWidth := lipgloss.Width(prefix)
	if prefixWidth >= width {
		return WrapContent(ToneText(marker, tone), width) + "\n" + WrapContent(message, width)
	}
	lines := strings.Split(WrapContent(message, width-prefixWidth), "\n")
	for index := range lines {
		if index == 0 {
			lines[index] = prefix + lines[index]
		} else {
			lines[index] = strings.Repeat(" ", prefixWidth) + lines[index]
		}
	}
	return strings.Join(lines, "\n")
}

func Secondary(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return Muted(value)
}

func Panel(body string, width int) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(currentTheme.panelBorder.GetBorderLeftForeground()).Padding(0, 1)
	if width > 0 {
		style = style.MaxWidth(width)
	}
	return style.Render(body)
}

func Modal(body string, width int) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(currentTheme.panelBorder.GetBorderLeftForeground()).Padding(1, 2)
	if width > 0 {
		style = style.Width(width)
	}
	return style.Render(body)
}

func OverlayAt(background, foreground string, width, height, x, y int) string {
	if width <= 0 {
		width = max(1, lipgloss.Width(background))
	}
	if height <= 0 {
		height = max(1, lipgloss.Height(background))
	}
	x, y = max(0, x), max(0, y)
	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(background).X(0).Y(0).Z(0), lipgloss.NewLayer(foreground).X(x).Y(y).Z(1)))
	return canvas.Render()
}

func CenterOverlay(background, foreground string, width, height int) string {
	if width <= 0 {
		width = max(1, lipgloss.Width(background))
	}
	if height <= 0 {
		height = max(1, lipgloss.Height(background))
	}
	x := max(0, (width-lipgloss.Width(foreground))/2)
	y := max(0, (height-lipgloss.Height(foreground))/2)
	return OverlayAt(background, foreground, width, height, x, y)
}

func CenterLayout(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return content
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func Divider(width int) string {
	if width <= 0 {
		return ""
	}
	return currentTheme.subtle.Render(strings.Repeat("─", width))
}

func TwoColumn(left, right string, width int) string {
	if right == "" || width <= 0 {
		return left
	}
	if lipgloss.Width(left)+2+lipgloss.Width(right) > width {
		return WrapContent(left, width) + "\n" + WrapContent(right, width)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	return left + strings.Repeat(" ", max(2, gap)) + right
}

func KeyValue(label, value string) string { return Label(label) + "  " + value }

type ActionHint struct {
	Key     string
	Label   string
	Enabled bool
	Danger  bool
}

func PageActionBar(width int, groups ...[]ActionHint) string {
	if width <= 0 {
		width = 80
	}
	separator := "   " + Muted("│") + "   "
	renderedGroups := make([][]string, 0, len(groups))
	for _, group := range groups {
		actions := make([]string, 0, len(group))
		for _, action := range group {
			if strings.TrimSpace(action.Key) == "" || strings.TrimSpace(action.Label) == "" {
				continue
			}
			actions = append(actions, renderActionHint(action))
		}
		if len(actions) == 0 {
			continue
		}
		renderedGroups = append(renderedGroups, actions)
	}
	groupLines := make([]string, 0, len(renderedGroups))
	for _, actions := range renderedGroups {
		groupLines = append(groupLines, strings.Join(actions, "   "))
	}
	joined := strings.Join(groupLines, separator)
	if lipgloss.Width(joined) <= width {
		return joined
	}
	lines := make([]string, 0, len(groupLines))
	for groupIndex, groupLine := range groupLines {
		if lipgloss.Width(groupLine) <= width {
			lines = append(lines, groupLine)
			continue
		}
		current := ""
		flush := func() {
			if strings.TrimSpace(current) != "" {
				lines = append(lines, current)
			}
			current = ""
		}
		actions := renderedGroups[groupIndex]
		for _, action := range actions {
			candidate := action
			if current != "" {
				candidate = current + "   " + action
			}
			if current != "" && lipgloss.Width(candidate) > width {
				flush()
				current = action
			} else {
				current = candidate
			}
		}
		flush()
	}
	return strings.Join(lines, "\n")
}

func renderActionHint(action ActionHint) string {
	keyText, labelText := strings.TrimSpace(action.Key), strings.TrimSpace(action.Label)
	if !action.Enabled {
		return currentTheme.subtle.Render(keyText + " " + labelText)
	}
	keyStyle := currentTheme.accent
	if action.Danger {
		keyStyle = currentTheme.danger
	}
	return keyStyle.Render(keyText) + " " + currentTheme.muted.Render(labelText)
}
