package component

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type DetailPageBinding struct {
	Key     string
	HelpKey string
	Desc    string
	Message tea.Msg
}

type DetailPage struct {
	viewport  viewport.Model
	help      HelpFooter
	title     string
	hideTitle bool
	meta      string
	content   string
	notice    string
	err       string
	bindings  []DetailPageBinding
	width     int
	height    int
}

func NewDetailPage(title, meta, content string) DetailPage {
	view := viewport.New(viewport.WithWidth(defaultLayoutWidth), viewport.WithHeight(defaultLayoutHeight))
	view.SoftWrap = false
	view.FillHeight = false
	page := DetailPage{viewport: view, help: NewHelpFooter(), title: strings.TrimSpace(title), meta: strings.TrimSpace(meta), content: strings.TrimSpace(content)}
	page.reflowContent(true)
	page.syncHelp()
	return page
}

func (page *DetailPage) SetTitle(value string) {
	if page != nil {
		page.title = strings.TrimSpace(value)
	}
}

func (page *DetailPage) SetTitleVisible(visible bool) {
	if page == nil {
		return
	}
	page.hideTitle = !visible
	page.resizeViewport()
}

func (page DetailPage) WithTitleVisible(visible bool) DetailPage {
	page.hideTitle = !visible
	page.resizeViewport()
	return page
}

func (page *DetailPage) SetMeta(value string) {
	if page != nil {
		page.meta = strings.TrimSpace(value)
	}
}

func (page *DetailPage) SetContent(value string) {
	if page == nil {
		return
	}
	page.content = strings.TrimSpace(value)
	page.reflowContent(true)
}

func (page *DetailPage) SetContentPreserveScroll(value string) {
	if page == nil {
		return
	}
	page.content = strings.TrimSpace(value)
	page.reflowContent(false)
}

func (page DetailPage) YOffset() int { return page.viewport.YOffset() }

func (page *DetailPage) SetFeedback(notice string, err error) {
	if page == nil {
		return
	}
	page.notice = strings.TrimSpace(notice)
	page.err = ""
	if err != nil {
		page.err = strings.TrimSpace(err.Error())
	}
}

func (page *DetailPage) SetBindings(bindings ...DetailPageBinding) {
	if page == nil {
		return
	}
	page.bindings = page.bindings[:0]
	for _, binding := range bindings {
		binding.Key = strings.TrimSpace(binding.Key)
		binding.HelpKey = strings.TrimSpace(binding.HelpKey)
		binding.Desc = strings.TrimSpace(binding.Desc)
		if binding.Key == "" || binding.Message == nil {
			continue
		}
		if binding.HelpKey == "" {
			binding.HelpKey = binding.Key
		}
		page.bindings = append(page.bindings, binding)
	}
	page.syncHelp()
}

func (page *DetailPage) Resize(width, height int) {
	if page == nil {
		return
	}
	page.width, page.height = max(1, width), max(1, height)
	page.resizeViewport()
}

func (page DetailPage) Update(message tea.Msg) (DetailPage, tea.Cmd) {
	if page.help.Update(message) {
		page.resizeViewport()
		return page, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		page.Resize(msg.Width, msg.Height)
		return page, nil
	case tea.BackgroundColorMsg:
		SetDarkBackground(msg.IsDark())
		return page, nil
	case tea.KeyPressMsg:
		for _, binding := range page.bindings {
			if key.Matches(msg, Binding([]string{binding.Key}, binding.HelpKey, binding.Desc)) {
				message := binding.Message
				return page, func() tea.Msg { return message }
			}
		}
	case detailPageWheelMsg:
		if msg < 0 {
			page.viewport.ScrollUp(3)
		} else if msg > 0 {
			page.viewport.ScrollDown(3)
		}
		return page, nil
	}
	updated, cmd := page.viewport.Update(message)
	page.viewport = updated
	return page, cmd
}

func (page DetailPage) View() string {
	width, height := page.width, page.height
	if width <= 0 {
		width = defaultLayoutWidth
	}
	if height <= 0 {
		height = defaultLayoutHeight
	}
	feedback := page.feedbackView(width)
	footer := page.footerView(width)
	layout := NewSectionLayout(page.layoutTitle(), page.meta, feedback, width, height, lipgloss.Height(footer))
	body := layout.View(page.viewport.View())
	return BottomHelp(body, footer, width, height)
}

func (page DetailPage) MouseTargets(originX, originY, z int) []MouseTarget {
	view := page.View()
	width, height := page.width, page.height
	if width <= 0 {
		width = defaultLayoutWidth
	}
	if height <= 0 {
		height = defaultLayoutHeight
	}
	targets := []MouseTarget{{
		ID: "detail.scroll", Rect: Rect{X: originX, Y: originY, Width: width, Height: height}, Z: z,
		Handle: func(event MouseEvent) tea.Msg {
			switch event.Button {
			case tea.MouseWheelUp:
				return detailPageWheelMsg(-1)
			case tea.MouseWheelDown:
				return detailPageWheelMsg(1)
			default:
				return nil
			}
		},
	}}
	for _, binding := range page.bindings {
		label := strings.TrimSpace(binding.HelpKey + " " + binding.Desc)
		if label == "" {
			continue
		}
		plain := ansi.Strip(view)
		line, column := findRenderedLine(strings.Split(plain, "\n"), label, 0)
		if line < 0 {
			continue
		}
		message := binding.Message
		targets = append(targets, MouseTarget{
			ID: "detail.binding", Rect: Rect{X: originX + column, Y: originY + line, Width: lipgloss.Width(label), Height: 1}, Z: z + 1,
			Handle: func(event MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return message
			},
		})
	}
	return targets
}

type detailPageWheelMsg int

func (page DetailPage) feedbackView(width int) string {
	parts := make([]string, 0, 2)
	if page.err != "" {
		parts = append(parts, BannerWidth(page.err, ToneDanger, width))
	}
	if page.notice != "" {
		parts = append(parts, BannerWidth(page.notice, ToneSuccess, width))
	}
	return strings.Join(parts, "\n")
}

func (page DetailPage) footerView(width int) string {
	return page.help.View(width)
}

func (page *DetailPage) syncHelp() {
	if page == nil {
		return
	}
	expanded := page.help.Expanded()
	bindings := []key.Binding{Binding([]string{"j", "k", "up", "down", "pgup", "pgdown"}, "j/k", "scroll")}
	for _, binding := range page.bindings {
		bindings = append(bindings, Binding([]string{binding.Key}, binding.HelpKey, binding.Desc))
	}
	page.help.SetBindings(bindings...)
	page.help.SetExpanded(expanded)
}

func (page *DetailPage) resizeViewport() {
	if page == nil {
		return
	}
	width := max(1, page.width)
	feedback := page.feedbackView(width)
	footerHeight := lipgloss.Height(page.footerView(width))
	layout := NewSectionLayout(page.layoutTitle(), page.meta, feedback, width, page.height, footerHeight)
	page.viewport.SetWidth(width)
	page.viewport.SetHeight(layout.BodyHeight)
	page.reflowContent(false)
}

func (page DetailPage) layoutTitle() string {
	if page.hideTitle {
		return ""
	}
	return page.title
}

func (page *DetailPage) reflowContent(reset bool) {
	if page == nil {
		return
	}
	offset := page.viewport.YOffset()
	page.viewport.SetContent(WrapContent(page.content, max(1, page.viewport.Width())))
	if reset {
		page.viewport.GotoTop()
		return
	}
	maxOffset := max(0, page.viewport.TotalLineCount()-page.viewport.Height())
	page.viewport.SetYOffset(min(offset, maxOffset))
}
