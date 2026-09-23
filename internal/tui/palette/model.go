package palette

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/tui/action"
	"go.mewis.me/codemcp/internal/tui/component"
)

const maxVisibleResults = 9

type SelectedMsg struct{ ID string }
type ClosedMsg struct{}
type MouseScrollMsg struct{ Delta int }

type Options struct {
	Title       string
	Hint        string
	Placeholder string
	Footer      string
	Recent      []string
}

type paletteItem struct{ Result }

func (item paletteItem) FilterValue() string { return item.Action.ID }

type paletteDelegate struct{ isDark bool }

func (delegate paletteDelegate) Height() int                         { return 1 }
func (delegate paletteDelegate) Spacing() int                        { return 0 }
func (delegate paletteDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (delegate paletteDelegate) Render(w io.Writer, model list.Model, index int, raw list.Item) {
	item, ok := raw.(paletteItem)
	if !ok {
		return
	}
	styles := list.NewDefaultItemStyles(delegate.isDark)
	titleStyle, shortcutStyle := styles.NormalTitle, styles.NormalDesc
	if index == model.Index() {
		titleStyle, shortcutStyle = styles.SelectedTitle, styles.SelectedDesc
	}
	title := item.Action.Title
	if item.Action.Category != "" {
		title = item.Action.Category + ": " + item.Action.Title
	}
	shortcut := ""
	if help := item.Action.Shortcut.Help(); help.Key != "" {
		shortcut = help.Key
	}
	left, right := titleStyle.Render(title), shortcutStyle.Render(shortcut)
	gap := max(2, model.Width()-lipgloss.Width(left)-lipgloss.Width(right))
	_, _ = fmt.Fprint(w, left+strings.Repeat(" ", gap)+right)
}

type Model struct {
	list    list.Model
	actions []action.Action
	context action.Context
	options Options
	isDark  bool
}

func New(actions []action.Action, ctx action.Context) Model {
	return NewWithOptions(actions, ctx, Options{})
}

func NewWithOptions(actions []action.Action, ctx action.Context, options Options) Model {
	if strings.TrimSpace(options.Title) == "" {
		options.Title = "Command Palette"
	}
	if strings.TrimSpace(options.Hint) == "" {
		options.Hint = "Ctrl+K"
	}
	if strings.TrimSpace(options.Placeholder) == "" {
		options.Placeholder = "Type a command"
	}
	if strings.TrimSpace(options.Footer) == "" {
		options.Footer = "↑/↓ navigate  ·  Enter run  ·  Esc close"
	}
	items := list.New(nil, paletteDelegate{isDark: true}, 64, maxVisibleResults)
	items.InfiniteScrolling = true
	items.DisableQuitKeybindings()
	items.SetFilteringEnabled(false)
	items.SetShowTitle(false)
	items.SetShowFilter(false)
	items.SetShowStatusBar(false)
	items.SetShowPagination(false)
	items.SetShowHelp(false)
	items.SetStatusBarItemName("command", "commands")
	items.FilterInput.Prompt = "> "
	items.FilterInput.Placeholder = options.Placeholder
	items.FilterInput.CharLimit = 160
	items.FilterInput.SetStyles(items.Styles.Filter)
	items.FilterInput.Focus()
	model := Model{list: items, actions: append([]action.Action(nil), actions...), context: ctx, options: options, isDark: true}
	model.refresh()
	return model
}

func (model *Model) SetActions(actions []action.Action, ctx action.Context) {
	if model == nil {
		return
	}
	model.actions = append([]action.Action(nil), actions...)
	model.context = ctx
	model.refresh()
}

func (model *Model) SetQuery(value string) {
	if model == nil {
		return
	}
	model.list.FilterInput.SetValue(value)
	model.refresh()
}

func (model Model) Query() string { return model.list.FilterInput.Value() }

func (model Model) SelectedID() string {
	item, ok := model.list.SelectedItem().(paletteItem)
	if !ok {
		return ""
	}
	return item.Action.ID
}

func (model Model) Update(message tea.Msg) (Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.BackgroundColorMsg:
		model.isDark = msg.IsDark()
		model.list.Styles = list.DefaultStyles(model.isDark)
		model.list.FilterInput.SetStyles(model.list.Styles.Filter)
		model.list.SetDelegate(paletteDelegate{isDark: model.isDark})
		return model, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return model, func() tea.Msg { return ClosedMsg{} }
		case "enter":
			if id := model.SelectedID(); id != "" {
				return model, func() tea.Msg { return SelectedMsg{ID: id} }
			}
			return model, nil
		case "up":
			model.list.CursorUp()
			return model, nil
		case "down":
			model.list.CursorDown()
			return model, nil
		}
	case MouseScrollMsg:
		if msg.Delta < 0 {
			model.list.CursorUp()
		} else if msg.Delta > 0 {
			model.list.CursorDown()
		}
		return model, nil
	}
	previous := model.list.FilterInput.Value()
	updated, cmd := model.list.FilterInput.Update(message)
	model.list.FilterInput = updated
	if model.list.FilterInput.Value() != previous {
		model.refresh()
	}
	return model, cmd
}

func (model Model) MouseTargets(originX, originY, z, width int) []component.MouseTarget {
	view := model.View(width)
	renderedWidth, renderedHeight := lipgloss.Width(view), lipgloss.Height(view)
	targets := []component.MouseTarget{{
		ID: "palette.scroll", Rect: component.Rect{X: originX, Y: originY, Width: renderedWidth, Height: renderedHeight}, Z: z,
		Handle: func(event component.MouseEvent) tea.Msg {
			switch event.Button {
			case tea.MouseWheelUp:
				return MouseScrollMsg{Delta: -1}
			case tea.MouseWheelDown:
				return MouseScrollMsg{Delta: 1}
			default:
				return nil
			}
		},
	}}
	lines := strings.Split(ansi.Strip(view), "\n")
	items := model.list.VisibleItems()
	start, end := model.list.Paginator.GetSliceBounds(len(items))
	searchLine := 0
	for _, raw := range items[start:end] {
		item, ok := raw.(paletteItem)
		if !ok {
			continue
		}
		title := item.Action.Title
		if item.Action.Category != "" {
			title = item.Action.Category + ": " + item.Action.Title
		}
		line, column := findPaletteLine(lines, title, searchLine)
		if line < 0 {
			continue
		}
		id := item.Action.ID
		targets = append(targets, component.MouseTarget{
			ID: "palette.result", Rect: component.Rect{X: originX + max(0, column-2), Y: originY + line, Width: max(1, renderedWidth-max(0, column-2)-2), Height: 1}, Z: z + 1,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return SelectedMsg{ID: id}
			},
		})
		searchLine = line + 1
	}
	return targets
}

func findPaletteLine(lines []string, needle string, start int) (int, int) {
	for index := max(0, start); index < len(lines); index++ {
		if column := strings.Index(lines[index], needle); column >= 0 {
			return index, column
		}
	}
	return -1, -1
}

func (model Model) View(width int) string {
	if width <= 0 {
		width = 72
	}
	width = max(42, min(78, width))
	contentWidth := max(32, width-6)
	items := model.list
	items.SetWidth(contentWidth)
	items.SetHeight(max(1, min(maxVisibleResults, len(items.Items()))))
	items.FilterInput.SetWidth(contentWidth - 2)
	listStyles := list.DefaultStyles(model.isDark)
	huhStyles := huh.ThemeCharm(model.isDark)
	titleStyle := huhStyles.Focused.Title
	mutedStyle := listStyles.StatusEmpty
	borderColor := huhStyles.Focused.Base.GetBorderLeftForeground()
	var builder strings.Builder
	builder.WriteString(titleStyle.Render(model.options.Title))
	builder.WriteString("\n")
	builder.WriteString(mutedStyle.Render(model.options.Hint))
	builder.WriteString("\n\n")
	builder.WriteString(items.FilterInput.View())
	builder.WriteString("\n")
	builder.WriteString(listStyles.NoItems.Render(strings.Repeat("─", contentWidth)))
	builder.WriteString("\n")
	if len(items.Items()) == 0 {
		builder.WriteString("\n")
		builder.WriteString(listStyles.NoItems.Render("No matching commands"))
		builder.WriteString("\n")
	} else {
		builder.WriteString(items.View())
		builder.WriteString("\n")
	}
	if item, ok := items.SelectedItem().(paletteItem); ok && item.Action.Description != "" {
		builder.WriteString("\n")
		builder.WriteString(mutedStyle.Render(item.Action.Description))
	}
	builder.WriteString("\n\n")
	builder.WriteString(mutedStyle.Render(model.options.Footer))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).Padding(1, 2).Width(width).Render(builder.String())
}

func (model *Model) refresh() {
	if model == nil {
		return
	}
	results := RankWithRecent(model.actions, model.list.FilterInput.Value(), model.context, model.options.Recent)
	items := make([]list.Item, 0, len(results))
	for _, result := range results {
		items = append(items, paletteItem{Result: result})
	}
	_ = model.list.SetItems(items)
	model.list.Select(0)
}

func (model Model) DebugResults() []string {
	result := make([]string, 0, len(model.list.Items()))
	for _, raw := range model.list.Items() {
		item, ok := raw.(paletteItem)
		if ok {
			result = append(result, fmt.Sprintf("%s:%d", item.Action.ID, item.Score))
		}
	}
	return result
}
