package page

import (
	"context"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/docs/tuiguide"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type GuidePage struct {
	ctx      context.Context
	topic    tuiguide.Topic
	children []tuiguide.Topic
	browser  component.Browser
	viewer   component.MarkdownViewer
	tab      int
	width    int
	height   int
}

func NewGuide(ctx context.Context, topicID string) (*GuidePage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	page := &GuidePage{ctx: ctx}
	if strings.TrimSpace(topicID) == "" {
		page.children = tuiguide.Children("")
		page.browser = newGuideBrowser(ctx, page.children)
		return page, nil
	}
	topic, ok := tuiguide.Lookup(topicID)
	if !ok {
		return nil, &guideTopicError{id: topicID}
	}
	markdown, err := tuiguide.Markdown(topic.ID)
	if err != nil {
		return nil, err
	}
	page.topic = topic
	page.children = tuiguide.Children(topic.ID)
	if len(page.children) > 0 {
		page.browser = newGuideBrowser(ctx, page.children)
	}
	page.viewer = component.NewMarkdownViewer(markdown)
	return page, nil
}

func newGuideBrowser(ctx context.Context, topics []tuiguide.Topic) component.Browser {
	rows := make([]component.Row, 0, len(topics))
	for _, topic := range topics {
		rows = append(rows, component.Row{ID: topic.ID, Title: topic.Title, Description: topic.Description, Search: strings.Join(append([]string{topic.ID, topic.Title}, topic.Keywords...), " ")})
	}
	return component.NewBrowser(ctx, "TUI Guide", rows, nil).WithTitleVisible(false).WithExternalHelp(true)
}

type guideTopicError struct{ id string }

func (err *guideTopicError) Error() string {
	return "unknown TUI guide topic: " + strings.TrimSpace(err.id)
}

func (page *GuidePage) Init() tea.Cmd {
	if page == nil || page.topic.ID == "" {
		return nil
	}
	return page.viewer.Init()
}

func (page *GuidePage) OverlayActive() bool { return false }
func (page *GuidePage) InputActive() bool {
	return page != nil && page.showBrowser() && page.browser.InputActive()
}

func (page *GuidePage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		page.resize()
		return page, nil
	case component.BrowserOpenMsg:
		if page.showBrowser() && msg.Row.ID != "" {
			return page, func() tea.Msg { return NavigateMsg{Path: append([]string{"guide"}, strings.Split(msg.Row.ID, "/")...)} }
		}
	case tea.BackgroundColorMsg:
		if page.showBrowser() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		updated, cmd := page.viewer.Update(msg)
		page.viewer = updated
		return page, cmd
	case tea.KeyPressMsg:
		if len(page.children) > 0 {
			switch msg.Keystroke() {
			case "left", "alt+1":
				page.tab = 0
				page.resize()
				return page, nil
			case "right", "alt+2":
				page.tab = 1
				page.resize()
				return page, nil
			}
		}
	}
	if page.showBrowser() {
		updated, cmd := page.browser.Update(message)
		page.browser = updated.(component.Browser)
		return page, cmd
	}
	updated, cmd := page.viewer.Update(message)
	page.viewer = updated
	return page, cmd
}

func (page *GuidePage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "TUI Guide unavailable", "")
	}
	page.width, page.height = width, height
	page.resize()
	if page.topic.ID == "" {
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("TUI Guide", "embedded · "+guideTopicCountLabel(len(page.children)), "", width, height, lipgloss.Height(help))
		body := component.BottomHelp(layout.View(page.browser.BodyContent()), help, width, height)
		return body
	}
	if page.showBrowser() {
		tabs := component.PageTabsNotice([]string{"Overview", "Topics"}, page.tab, "", width)
		bodyHeight := max(1, height-lipgloss.Height(tabs))
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("", guideTopicCountLabel(len(page.children)), "", width, bodyHeight, lipgloss.Height(help))
		return tabs + "\n" + component.BottomHelp(layout.View(page.browser.BodyContent()), help, width, bodyHeight)
	}
	helpBindings := []key.Binding{component.Binding([]string{"j", "k"}, "j/k", "scroll")}
	if len(page.children) > 0 {
		helpBindings = append(helpBindings, component.Binding([]string{"alt+2"}, "alt+2", "topics"))
	}
	help := component.DefaultHelp(width, helpBindings...)
	tabs := ""
	if len(page.children) > 0 {
		tabs = component.PageTabsNotice([]string{"Overview", "Topics"}, page.tab, "", width) + "\n"
	}
	bodyHeight := max(1, height-lipgloss.Height(tabs))
	layout := component.NewSectionLayout("", page.topic.Description, "", width, bodyHeight, lipgloss.Height(help))
	page.viewer.Resize(width, layout.BodyHeight)
	content := tabs + layout.View(page.viewer.View())
	return component.BottomHelp(content, help, width, height)
}

func (page *GuidePage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil || page.width <= 0 || page.height <= 0 {
		return nil
	}
	if page.topic.ID == "" {
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("TUI Guide", "embedded · "+guideTopicCountLabel(len(tuiguide.Topics())), "", page.width, page.height, lipgloss.Height(help))
		targets := page.browser.MouseTargets(originX, originY+layout.BodyY, z)
		helpY := originY + page.height - lipgloss.Height(help)
		return append(targets, page.browser.HelpMouseTargets(originX, helpY, z+2)...)
	}
	if page.showBrowser() {
		tabs := component.PageTabsNotice([]string{"Overview", "Topics"}, page.tab, "", page.width)
		bodyHeight := max(1, page.height-lipgloss.Height(tabs))
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("", guideTopicCountLabel(len(page.children)), "", page.width, bodyHeight, lipgloss.Height(help))
		targets := page.browser.MouseTargets(originX, originY+lipgloss.Height(tabs)+1+layout.BodyY, z)
		helpY := originY + page.height - lipgloss.Height(help)
		return append(targets, page.browser.HelpMouseTargets(originX, helpY, z+2)...)
	}
	help := component.DefaultHelp(page.width, component.Binding([]string{"j", "k"}, "j/k", "scroll"), component.Binding([]string{"esc"}, "esc", "topics"))
	tabsHeight := 0
	if len(page.children) > 0 {
		tabsHeight = lipgloss.Height(component.PageTabsNotice([]string{"Overview", "Topics"}, page.tab, "", page.width)) + 1
	}
	bodyHeight := max(1, page.height-tabsHeight)
	layout := component.NewSectionLayout("", page.topic.Description, "", page.width, bodyHeight, lipgloss.Height(help))
	viewerY := originY + tabsHeight + layout.BodyY
	page.viewer.Resize(page.width, layout.BodyHeight)
	return page.viewer.MouseTargets(originX, viewerY, z)
}

func (page *GuidePage) resize() {
	if page == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	if page.topic.ID == "" {
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("TUI Guide", "embedded · "+guideTopicCountLabel(len(tuiguide.Topics())), "", page.width, page.height, lipgloss.Height(help))
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: layout.BodyHeight})
		page.browser = updated.(component.Browser)
		return
	}
	if page.showBrowser() {
		tabs := component.PageTabsNotice([]string{"Overview", "Topics"}, page.tab, "", page.width)
		bodyHeight := max(1, page.height-lipgloss.Height(tabs))
		help := page.browser.HelpView()
		layout := component.NewSectionLayout("", guideTopicCountLabel(len(page.children)), "", page.width, bodyHeight, lipgloss.Height(help))
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: layout.BodyHeight})
		page.browser = updated.(component.Browser)
		return
	}
	helpBindings := []key.Binding{component.Binding([]string{"j", "k"}, "j/k", "scroll")}
	if len(page.children) > 0 {
		helpBindings = append(helpBindings, component.Binding([]string{"alt+2"}, "alt+2", "topics"))
	}
	help := component.DefaultHelp(page.width, helpBindings...)
	tabsHeight := 0
	if len(page.children) > 0 {
		tabsHeight = lipgloss.Height(component.PageTabsNotice([]string{"Overview", "Topics"}, page.tab, "", page.width)) + 1
	}
	bodyHeight := max(1, page.height-tabsHeight)
	layout := component.NewSectionLayout("", page.topic.Description, "", page.width, bodyHeight, lipgloss.Height(help))
	page.viewer.Resize(page.width, layout.BodyHeight)
}

func (page *GuidePage) showBrowser() bool {
	return page != nil && (page.topic.ID == "" || len(page.children) > 0 && page.tab == 1)
}

func guideTopicCountLabel(count int) string {
	if count == 1 {
		return "1 topic"
	}
	return strconv.Itoa(count) + " topics"
}
