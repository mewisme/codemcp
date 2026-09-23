package component

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type Row struct {
	ID          string
	Title       string
	Description string
	Meta        string
	Summary     string
	Search      string
}

type RefreshFunc func(context.Context) ([]Row, error)

type RowAction struct {
	Key  string
	Desc string
	Run  func(Row) (string, tea.Cmd, error)
	When func(Row) bool
}

type browserItem struct{ Row }

func (i browserItem) Title() string {
	if value := strings.TrimSpace(i.Row.Title); value != "" {
		return value
	}
	if value := strings.TrimSpace(i.Row.ID); value != "" {
		return value
	}
	return i.Row.Summary
}

func (i browserItem) Description() string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(i.Row.Description); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(i.Row.Meta); value != "" {
		parts = append(parts, value)
	}
	return strings.Join(parts, " · ")
}

func (i browserItem) FilterValue() string {
	return strings.Join([]string{i.Row.ID, i.Row.Title, i.Row.Description, i.Row.Meta, i.Row.Summary, i.Row.Search}, " ")
}

type Browser struct {
	ctx                context.Context
	title              string
	titleNotice        string
	titleVisible       bool
	externalHelp       bool
	list               list.Model
	refresh            RefreshFunc
	actions            []RowAction
	helpBindings       []key.Binding
	loading            bool
	width              int
	height             int
	err                error
	notice             string
	pendingSelectionID string
}

type BrowserOpenMsg struct{ Row Row }

type browserRefreshMsg struct {
	rows []Row
	err  error
}

type browserMouseMsg struct {
	Index int
	Wheel int
	Open  bool
}

const browserShortCustomHelpLimit = 5

var browserOpenBinding = Binding([]string{"enter", "v"}, "enter", "open")
var browserRefreshBinding = Binding([]string{"r"}, "r", "refresh")

func NewBrowser(ctx context.Context, title string, rows []Row, refresh RefreshFunc) Browser {
	if ctx == nil {
		ctx = context.Background()
	}
	model := NewDefaultList(strings.TrimSpace(title), browserListItems(rows), 80, 20, "item", "items")
	model.InfiniteScrolling = true
	model.SetShowStatusBar(len(rows) > 0)
	result := Browser{ctx: ctx, title: strings.TrimSpace(title), titleVisible: true, list: model, refresh: refresh}
	result.syncHelp()
	return result
}

func (m Browser) WithAction(action RowAction) Browser {
	action.Key, action.Desc = strings.TrimSpace(action.Key), strings.TrimSpace(action.Desc)
	if action.Key != "" && action.Run != nil {
		m.actions = append(m.actions, action)
		m.syncHelp()
	}
	return m
}

func (m Browser) WithHelpBindings(bindings ...key.Binding) Browser {
	m.SetHelpBindings(bindings...)
	return m
}

func (m *Browser) SetHelpBindings(bindings ...key.Binding) {
	if m == nil {
		return
	}
	m.helpBindings = append([]key.Binding(nil), bindings...)
	m.syncHelp()
}

func (m Browser) WithTitleVisible(visible bool) Browser {
	m.list.SetShowTitle(visible)
	m.titleVisible = visible
	return m
}

func (m Browser) WithExternalHelp(enabled bool) Browser {
	m.externalHelp = enabled
	m.list.SetShowHelp(!enabled)
	return m
}

func (m *Browser) SetTitleNotice(notice string) {
	if m != nil {
		m.titleNotice = strings.TrimSpace(notice)
	}
}

func (m Browser) Init() tea.Cmd { return nil }

func (m Browser) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.BackgroundColorMsg:
		ApplyDefaultListTheme(&m.list, msg.IsDark())
		m.syncHelp()
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		ResizeDefaultList(&m.list, msg.Width, msg.Height)
		return m, nil
	case browserRefreshMsg:
		return m.finishRefresh(msg)
	case list.FilterMatchesMsg:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		if m.pendingSelectionID != "" {
			m.restoreSelection(m.pendingSelectionID)
			m.pendingSelectionID = ""
		}
		return m, cmd
	case browserMouseMsg:
		return m.handleMouse(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(message)
	return m, cmd
}

func (m Browser) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	if m.externalHelp && msg.String() == "?" {
		m.list.Help.ShowAll = !m.list.Help.ShowAll
		return m, nil
	}
	switch {
	case key.Matches(msg, browserOpenBinding):
		if selected, ok := m.selected(); ok {
			return m, browserOpenCmd(selected)
		}
		return m, nil
	case m.refresh != nil && key.Matches(msg, browserRefreshBinding):
		return m.startRefresh()
	default:
		if handled, cmd := m.runAction(msg.String()); handled {
			return m, cmd
		}
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
}

func (m Browser) View() tea.View {
	view := tea.NewView(m.Content())
	view.AltScreen = true
	return view
}

func (m Browser) Content() string {
	content := m.list.View()
	if m.titleVisible && m.titleNotice != "" {
		lines := strings.Split(content, "\n")
		if len(lines) > 0 {
			lines[0] = pageTitleNoticeLine(m.title, m.titleNotice, m.width)
			content = strings.Join(lines, "\n")
		}
	}
	return fitRenderedContent(content, m.width, m.height)
}

func (m Browser) BodyContent() string {
	content := m.list.View()
	if !m.externalHelp {
		listView := m.list
		listView.SetShowHelp(false)
		content = listView.View()
	}
	if m.titleVisible && m.titleNotice != "" {
		lines := strings.Split(content, "\n")
		if len(lines) > 0 {
			lines[0] = pageTitleNoticeLine(m.title, m.titleNotice, m.width)
			content = strings.Join(lines, "\n")
		}
	}
	return fitRenderedContent(content, m.width, m.height)
}

func fitRenderedContent(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	if width > 0 {
		for index := range lines {
			lines[index] = ansi.Truncate(lines[index], width, "")
		}
	}
	return strings.Join(lines, "\n")
}

func (m Browser) HelpView() string {
	width := m.width
	if width <= 0 {
		width = defaultLayoutWidth
	}
	help := m.list.Help
	help.SetWidth(width)
	return WrapContent(help.View(m.list), width)
}

func (m Browser) HelpMouseTargets(originX, originY, z int) []MouseTarget {
	lines := strings.Split(ansi.Strip(m.HelpView()), "\n")
	targets := make([]MouseTarget, 0, len(m.renderedHelpBindings()))
	for _, binding := range m.renderedHelpBindings() {
		help := binding.Help()
		keys := binding.Keys()
		if strings.TrimSpace(help.Key+help.Desc) == "" || len(keys) == 0 {
			continue
		}
		line, column, width := findBrowserHelpBinding(lines, help.Key, help.Desc)
		if line < 0 {
			continue
		}
		keyValue := keys[0]
		targets = append(targets, MouseTarget{
			ID: "browser.help", Rect: Rect{X: originX + column, Y: originY + line, Width: width, Height: 1}, Z: z,
			Handle: func(event MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return browserHelpKeyMsg(keyValue)
			},
		})
	}
	return targets
}

func (m Browser) Selected() (Row, bool) { return m.selected() }

func (m *Browser) ReplaceRows(rows []Row, selectedID string) tea.Cmd {
	if m == nil {
		return nil
	}
	m.list.SetShowStatusBar(len(rows) > 0)
	cmd := m.list.SetItems(browserListItems(rows))
	if cmd != nil {
		m.pendingSelectionID = selectedID
	} else if selectedID != "" {
		m.restoreSelection(selectedID)
	}
	return cmd
}

func (m *Browser) SelectLast() bool {
	if m == nil {
		return false
	}
	items := m.list.VisibleItems()
	if len(items) == 0 {
		return false
	}
	m.list.Select(len(items) - 1)
	return true
}

func (m *Browser) SelectID(id string) bool {
	if m == nil {
		return false
	}
	for index, item := range m.list.VisibleItems() {
		value, ok := item.(browserItem)
		if ok && value.ID == id {
			m.list.Select(index)
			return true
		}
	}
	return false
}

func (m Browser) HelpExpanded() bool { return m.list.Help.ShowAll }

func (m *Browser) SetHelpExpanded(expanded bool) {
	if m != nil {
		m.list.Help.ShowAll = expanded
	}
}

func (m Browser) InputActive() bool { return m.list.FilterState() == list.Filtering }

func (m *Browser) StartFilter() {
	if m == nil {
		return
	}
	m.list.SetFilterState(list.Filtering)
}

func (m Browser) MouseTargets(originX, originY, z int) []MouseTarget {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	targets := []MouseTarget{{
		ID: "browser.scroll", Rect: Rect{X: originX, Y: originY, Width: m.width, Height: m.height}, Z: z,
		Handle: func(event MouseEvent) tea.Msg {
			switch event.Button {
			case tea.MouseWheelUp:
				return browserMouseMsg{Wheel: -1}
			case tea.MouseWheelDown:
				return browserMouseMsg{Wheel: 1}
			default:
				return nil
			}
		},
	}}
	startY := 0
	if m.list.ShowTitle() || m.list.ShowFilter() && m.list.FilteringEnabled() {
		if m.list.ShowTitle() || m.list.FilterState() == list.Filtering {
			startY += 1 + m.list.Styles.TitleBar.GetPaddingTop() + m.list.Styles.TitleBar.GetPaddingBottom()
		} else {
			startY++
		}
	}
	if m.list.ShowStatusBar() {
		startY += 1 + m.list.Styles.StatusBar.GetPaddingTop() + m.list.Styles.StatusBar.GetPaddingBottom()
	}
	visible := m.list.VisibleItems()
	start, end := m.list.Paginator.GetSliceBounds(len(visible))
	for index := start; index < end; index++ {
		rowIndex := index
		y := originY + startY + (index-start)*3
		if y >= originY+m.height {
			break
		}
		open := index == m.list.GlobalIndex()
		rowHeight := min(2, originY+m.height-y)
		targets = append(targets, MouseTarget{
			ID: "browser.row", Rect: Rect{X: originX, Y: y, Width: m.width, Height: rowHeight}, Z: z + 1,
			Handle: func(event MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return browserMouseMsg{Index: rowIndex, Open: open}
			},
		})
	}
	viewLines := strings.Split(ansi.Strip(m.Content()), "\n")
	for _, binding := range m.renderedHelpBindings() {
		help := binding.Help()
		label := strings.TrimSpace(help.Key + " " + help.Desc)
		keys := binding.Keys()
		if label == "" || len(keys) == 0 {
			continue
		}
		line, column, width := findBrowserHelpBinding(viewLines, help.Key, help.Desc)
		if line < 0 || line >= m.height {
			continue
		}
		width = min(width, max(0, m.width-column))
		if width <= 0 {
			continue
		}
		keyValue := keys[0]
		targets = append(targets, MouseTarget{
			ID: "browser.help", Rect: Rect{X: originX + column, Y: originY + line, Width: width, Height: 1}, Z: z + 2,
			Handle: func(event MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return browserHelpKeyMsg(keyValue)
			},
		})
	}
	return targets
}

func (m Browser) handleMouse(msg browserMouseMsg) (tea.Model, tea.Cmd) {
	if msg.Wheel < 0 {
		m.list.CursorUp()
		return m, nil
	}
	if msg.Wheel > 0 {
		m.list.CursorDown()
		return m, nil
	}
	if msg.Index < 0 || msg.Index >= len(m.list.VisibleItems()) {
		return m, nil
	}
	m.list.Select(msg.Index)
	if msg.Open {
		if selected, ok := m.selected(); ok {
			return m, browserOpenCmd(selected)
		}
	}
	return m, nil
}

func (m Browser) selected() (Row, bool) {
	item, ok := m.list.SelectedItem().(browserItem)
	if !ok {
		return Row{}, false
	}
	return item.Row, true
}

func (m Browser) startRefresh() (tea.Model, tea.Cmd) {
	if m.refresh == nil || m.loading {
		return m, nil
	}
	m.loading = true
	m.list.StartSpinner()
	return m, func() tea.Msg {
		rows, err := m.refresh(m.ctx)
		return browserRefreshMsg{rows: rows, err: err}
	}
}

func (m Browser) finishRefresh(msg browserRefreshMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		return m, m.list.NewStatusMessage("Refresh failed: " + msg.err.Error())
	}
	m.err = nil
	selectedID := ""
	if selected, ok := m.selected(); ok {
		selectedID = selected.ID
	}
	m.list.SetShowStatusBar(len(msg.rows) > 0)
	cmd := m.list.SetItems(browserListItems(msg.rows))
	if cmd != nil {
		m.pendingSelectionID = selectedID
	} else {
		m.restoreSelection(selectedID)
	}
	return m, cmd
}

func (m *Browser) runAction(keyValue string) (bool, tea.Cmd) {
	for _, action := range m.actions {
		if action.Key != keyValue {
			continue
		}
		selected, ok := m.selected()
		if !ok || !actionAvailable(action, selected) {
			return true, nil
		}
		m.notice, m.err = "", nil
		notice, cmd, err := action.Run(selected)
		if err != nil {
			m.err = err
			return true, tea.Batch(cmd, m.list.NewStatusMessage(err.Error()))
		}
		m.notice = notice
		return true, tea.Batch(cmd, m.list.NewStatusMessage(notice))
	}
	return false, nil
}

func actionAvailable(action RowAction, row Row) bool { return action.When == nil || action.When(row) }

func (m *Browser) syncHelp() {
	custom := m.customHelpBindings()
	short := []key.Binding{browserOpenBinding}
	if len(custom) <= browserShortCustomHelpLimit {
		short = append(short, custom...)
	}
	full := append([]key.Binding{browserOpenBinding}, custom...)
	m.list.AdditionalShortHelpKeys = func() []key.Binding { return append([]key.Binding(nil), short...) }
	m.list.AdditionalFullHelpKeys = func() []key.Binding { return append([]key.Binding(nil), full...) }
}

func (m Browser) customHelpBindings() []key.Binding {
	custom := append([]key.Binding(nil), m.helpBindings...)
	for _, action := range m.actions {
		custom = append(custom, Binding([]string{action.Key}, action.Key, action.Desc))
	}
	if m.refresh != nil {
		custom = append(custom, browserRefreshBinding)
	}
	return custom
}

func (m Browser) renderedHelpBindings() []key.Binding {
	return append([]key.Binding{browserOpenBinding}, m.customHelpBindings()...)
}

func browserOpenCmd(row Row) tea.Cmd { return func() tea.Msg { return BrowserOpenMsg{Row: row} } }

func (m *Browser) restoreSelection(id string) {
	if id == "" {
		return
	}
	for index, item := range m.list.VisibleItems() {
		value, ok := item.(browserItem)
		if ok && value.ID == id {
			m.list.Select(index)
			return
		}
	}
}

func browserListItems(rows []Row) []list.Item {
	items := make([]list.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, browserItem{Row: row})
	}
	return items
}

func findBrowserHelpBinding(lines []string, helpKey, description string) (int, int, int) {
	label := strings.TrimSpace(helpKey + " " + description)
	if line, column := findRenderedLine(lines, label, 0); line >= 0 {
		return line, column, lipgloss.Width(label)
	}
	for lineIndex, line := range lines {
		descIndex := strings.Index(line, description)
		if descIndex < 0 {
			continue
		}
		prefix := line[:descIndex]
		for keyIndex := strings.LastIndex(prefix, helpKey); keyIndex >= 0; keyIndex = strings.LastIndex(prefix[:keyIndex], helpKey) {
			if strings.TrimSpace(line[keyIndex+len(helpKey):descIndex]) != "" {
				continue
			}
			start := lipgloss.Width(line[:keyIndex])
			width := lipgloss.Width(line[keyIndex : descIndex+len(description)])
			return lineIndex, start, width
		}
	}
	return -1, -1, 0
}

func browserHelpKeyMsg(value string) tea.KeyPressMsg {
	switch value {
	case "space", " ":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		runes := []rune(value)
		if len(runes) == 0 {
			return tea.KeyPressMsg{}
		}
		return tea.KeyPressMsg{Code: runes[0]}
	}
}
