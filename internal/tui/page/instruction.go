package page

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/tree"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/tui/component"
)

type instructionTab int

const (
	instructionTabContext instructionTab = iota
	instructionTabRules
	instructionTabSources
)

var instructionTabLabels = []string{"Context", "Rules", "Sources"}

type instructionRefreshMsg struct {
	settings application.InstructionSettings
	err      error
}

type instructionSavedMsg struct {
	settings application.InstructionSettings
	err      error
}

type instructionTabMsg struct{ Tab instructionTab }

type InstructionPage struct {
	ctx            context.Context
	service        *application.InstructionSettingsService
	settings       application.InstructionSettings
	tab            instructionTab
	contextEditor  *component.TextAreaEditor
	contextPreview component.MarkdownViewer
	contextHelp    component.HelpFooter
	ruleEditor     *component.Editor
	rules          component.Browser
	ruleEditID     string
	ruleName       string
	ruleContent    string
	ruleEnabled    bool
	ruleDeleteID   string
	ruleConfirm    component.ConfirmButtons
	sources        tree.Model
	sourceDark     bool
	saving         bool
	notice         string
	err            error
	width          int
	height         int
}

func NewInstruction(ctx context.Context) (*InstructionPage, error) {
	return NewInstructionRoute(ctx, "")
}

func newInstructionPage(ctx context.Context, service *application.InstructionSettingsService) (*InstructionPage, error) {
	return newInstructionPageRoute(ctx, service, "")
}

func NewInstructionRoute(ctx context.Context, section string) (*InstructionPage, error) {
	return NewInstructionRouteAction(ctx, section, "", "")
}

func newInstructionPageRoute(ctx context.Context, service *application.InstructionSettingsService, section string) (*InstructionPage, error) {
	return newInstructionPageRouteAction(ctx, service, section, "", "")
}

func NewInstructionRouteAction(ctx context.Context, section, resourceID, action string) (*InstructionPage, error) {
	return newInstructionPageRouteAction(ctx, application.NewInstructionSettingsService(nil), section, resourceID, action)
}

func newInstructionPageRouteAction(ctx context.Context, service *application.InstructionSettingsService, section, resourceID, action string) (*InstructionPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if service == nil {
		service = application.NewInstructionSettingsService(nil)
	}
	settings, err := service.Load()
	if err != nil {
		return nil, err
	}
	tab, err := instructionTabFromSection(section)
	if err != nil {
		return nil, err
	}
	page := &InstructionPage{ctx: ctx, service: service, settings: settings, sourceDark: true, tab: tab}
	page.contextHelp = component.NewHelpFooter(component.Binding([]string{"e"}, "e", "edit"), component.Binding([]string{"r"}, "r", "refresh"))
	page.syncDetail()
	if action != "" {
		if tab == instructionTabContext {
			if action != "edit" {
				return nil, fmt.Errorf("unsupported global context editor action: %s", action)
			}
			editor := component.NewTextAreaEditor("", settings.Context)
			page.contextEditor = &editor
		} else if tab == instructionTabRules {
			if err := page.initRuleEditor(resourceID, action); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("instruction editor action %q is unsupported for this tab", action)
		}
	}
	page.resizeContent()
	return page, nil
}

func instructionTabFromSection(section string) (instructionTab, error) {
	switch strings.ToLower(strings.TrimSpace(section)) {
	case "", "context":
		return instructionTabContext, nil
	case "rules":
		return instructionTabRules, nil
	case "sources":
		return instructionTabSources, nil
	default:
		return instructionTabContext, fmt.Errorf("unsupported instruction tab: %s", section)
	}
}

func (page *InstructionPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	if page.ruleEditor != nil {
		return page.ruleEditor.Init()
	}
	if page.contextEditor != nil {
		return page.contextEditor.Init()
	}
	if page.tab == instructionTabContext {
		return page.contextPreview.Init()
	}
	return nil
}
func (page *InstructionPage) OverlayActive() bool { return page != nil && page.ruleDeleteID != "" }
func (page *InstructionPage) InputActive() bool {
	return page != nil && (page.contextEditor != nil || page.ruleEditor != nil || page.tab == instructionTabRules && page.rules.InputActive())
}
func (page *InstructionPage) Dirty() bool {
	if page == nil {
		return false
	}
	if page.ruleEditor != nil {
		return page.ruleEditor.Dirty()
	}
	return page.contextEditor != nil && page.contextEditor.Dirty()
}
func (page *InstructionPage) Submitting() bool { return page != nil && page.saving }
func (page *InstructionPage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}
func (page *InstructionPage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *InstructionPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		page.resizeContent()
		return page, nil
	case tea.BackgroundColorMsg:
		page.sourceDark = msg.IsDark()
		page.contextHelp.Update(msg)
		if page.ruleEditor != nil {
			updated, cmd := page.ruleEditor.Update(msg)
			page.ruleEditor = &updated
			return page, cmd
		}
		if page.contextEditor != nil {
			updated, cmd := page.contextEditor.Update(msg)
			page.contextEditor = &updated
			return page, cmd
		}
		if page.tab == instructionTabContext {
			updated, cmd := page.contextPreview.Update(msg)
			page.contextPreview = updated
			return page, cmd
		}
		if page.tab == instructionTabRules {
			updated, cmd := page.rules.Update(msg)
			page.rules = updated.(component.Browser)
			return page, cmd
		}
		if page.tab == instructionTabSources {
			page.applySourceTreeTheme(&page.sources)
			return page, nil
		}
		return page, nil
	case component.EditorSubmitMsg:
		if page.ruleEditor == nil || page.saving {
			return page, nil
		}
		if err := page.ruleEditor.Validate(); err != nil {
			page.ruleEditor.SetFeedback("", err)
			return page, nil
		}
		page.saving = true
		page.err, page.notice = nil, ""
		page.ruleEditor.SetSubmitting(true)
		return page, page.saveRuleEditorCmd()
	case component.EditorCancelMsg:
		if page.ruleEditor != nil && !page.saving {
			return page, page.ruleEditorParentNavigation()
		}
		return page, nil
	case component.BrowserOpenMsg:
		if page.tab == instructionTabRules && page.ruleEditor == nil && msg.Row.ID != "" {
			return page, page.ruleEditorNavigation(msg.Row.ID)
		}
		return page, nil
	case component.ConfirmChoiceMsg:
		if page.ruleDeleteID != "" {
			page.ruleConfirm.Select(msg.Affirmative)
			return page, page.updateRuleConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case component.TextAreaSavedMsg:
		if page.contextEditor == nil || page.saving {
			return page, nil
		}
		page.saving = true
		page.err, page.notice = nil, ""
		return page, page.saveContextCmd(msg.Value)
	case component.TextAreaCancelledMsg:
		if page.contextEditor != nil && !page.saving {
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"instruction", "context"}, Replace: true} }
		}
		return page, nil
	case instructionRefreshMsg:
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.settings, page.err = msg.settings, nil
		page.notice = "Instructions refreshed"
		page.syncDetail()
		return page, nil
	case instructionSavedMsg:
		page.saving = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.settings, page.err = msg.settings, nil
		page.notice = "Global context saved"
		wasEditing := page.contextEditor != nil
		page.contextEditor = nil
		page.syncDetail()
		if wasEditing {
			return page, tea.Batch(
				func() tea.Msg { return NavigateMsg{Path: []string{"instruction", "context"}, Replace: true} },
				func() tea.Msg {
					return ToastMsg{Title: "Instruction", Message: "Global context saved", Tone: component.ToneSuccess}
				},
			)
		}
		return page, nil
	case instructionRulesSavedMsg:
		page.saving = false
		if msg.err != nil {
			if page.ruleEditor != nil {
				page.ruleEditor.SetSubmitting(false)
				page.ruleEditor.SetFeedback("", msg.err)
			} else {
				page.err = msg.err
			}
			return page, nil
		}
		if page.ruleEditor != nil {
			page.settings, page.err = msg.settings, nil
			page.ruleEditor.SetSubmitting(false)
			page.ruleEditor.Accept()
			return page, tea.Batch(page.ruleEditorParentNavigation(), func() tea.Msg {
				return ToastMsg{Title: "Instruction", Message: msg.notice, Tone: component.ToneSuccess}
			})
		}
		page.settings, page.err = msg.settings, nil
		page.notice = msg.notice
		page.closeRuleConfirm()
		page.syncRuleBrowser()
		return page, nil
	case instructionSourcesSavedMsg:
		page.saving = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.settings, page.err = msg.settings, nil
		page.notice = msg.notice
		page.syncSourceTree()
		return page, nil
	case instructionSourceWheelMsg:
		if page.tab == instructionTabSources {
			page.updateSourceWheel(msg)
		}
		return page, nil
	case instructionTabMsg:
		return page, page.instructionTabNavigation(msg.Tab)
	case tea.KeyPressMsg:
		if page.ruleDeleteID != "" {
			return page, page.updateRuleConfirm(msg)
		}
		if page.ruleEditor != nil {
			if page.saving {
				return page, nil
			}
			updated, cmd := page.ruleEditor.Update(msg)
			page.ruleEditor = &updated
			return page, cmd
		}
		if page.contextEditor != nil {
			if page.saving {
				return page, nil
			}
			updated, cmd := page.contextEditor.Update(msg)
			page.contextEditor = &updated
			return page, cmd
		}
		if cmd, handled := page.handleTabKey(msg); handled {
			return page, cmd
		}
		if page.tab == instructionTabContext {
			switch msg.String() {
			case "e":
				return page, func() tea.Msg { return NavigateMsg{Path: []string{"instruction", "context", "edit"}} }
			case "r":
				page.err, page.notice = nil, ""
				return page, page.refreshCmd()
			}
			updated, cmd := page.contextPreview.Update(msg)
			page.contextPreview = updated
			return page, cmd
		}
		if page.tab == instructionTabRules && page.rules.InputActive() {
			updated, cmd := page.rules.Update(msg)
			page.rules = updated.(component.Browser)
			return page, cmd
		}
		if page.tab == instructionTabRules {
			if cmd, handled := page.handleRuleKey(msg); handled {
				return page, cmd
			}
		}
		if page.tab == instructionTabSources {
			return page, page.handleSourceKey(msg)
		}
		switch msg.String() {
		case "r":
			page.err, page.notice = nil, ""
			return page, page.refreshCmd()
		}
	}
	if page.ruleEditor != nil {
		updated, cmd := page.ruleEditor.Update(message)
		page.ruleEditor = &updated
		return page, cmd
	}
	if page.contextEditor != nil {
		updated, cmd := page.contextEditor.Update(message)
		page.contextEditor = &updated
		return page, cmd
	}
	if page.tab == instructionTabContext {
		updated, cmd := page.contextPreview.Update(message)
		page.contextPreview = updated
		return page, cmd
	}
	if page.tab == instructionTabRules {
		updated, cmd := page.rules.Update(message)
		page.rules = updated.(component.Browser)
		return page, cmd
	}
	if page.tab == instructionTabSources {
		updated, cmd := page.sources.Update(message)
		page.sources = updated
		return page, cmd
	}
	return page, nil
}

func (page *InstructionPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Instruction page unavailable", "")
	}
	page.width, page.height = width, height
	if page.ruleEditor != nil {
		return page.ruleEditorView(width, height)
	}
	tabs := component.PageTabsNotice(instructionTabLabels, int(page.tab), page.notice, width)
	bodyHeight := max(1, height-lipgloss.Height(tabs))
	if page.tab == instructionTabRules {
		return page.rulesView(tabs, width, bodyHeight)
	}
	if page.tab == instructionTabSources {
		return page.sourcesView(tabs, width, bodyHeight)
	}
	if page.contextEditor != nil {
		feedback := page.instructionFeedback(width)
		layout := page.instructionSectionLayout(instructionTabContext, feedback, width, bodyHeight)
		page.contextEditor.Resize(width, layout.BodyHeight)
		return tabs + "\n" + layout.View(page.contextEditor.View())
	}
	feedback := page.instructionFeedback(width)
	help := page.contextHelp.View(width)
	layout := component.NewSectionLayout("", formatInstructionBytes(len([]byte(page.settings.Context))), feedback, width, bodyHeight, lipgloss.Height(help))
	page.contextPreview.Resize(width, layout.BodyHeight)
	return tabs + "\n" + component.BottomHelp(layout.View(page.contextPreview.View()), help, width, bodyHeight)
}

func (page *InstructionPage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	if page.ruleEditor != nil {
		return page.ruleEditorMouseTargets(originX, originY, z)
	}
	tabs, spans := component.PageTabsLayout(instructionTabLabels, int(page.tab), page.notice, page.width)
	if page.ruleDeleteID != "" {
		return page.ruleConfirmMouseTargets(originX, originY, z)
	}
	targets := make([]component.MouseTarget, 0, len(spans)+1)
	for _, span := range spans {
		tab := instructionTab(span.Index)
		targets = append(targets, component.MouseTarget{
			ID: "instruction.tab", Rect: component.Rect{X: originX + span.X, Y: originY, Width: span.Width, Height: 1}, Z: z + 2,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return instructionTabMsg{Tab: tab}
			},
		})
	}
	contentY := originY + lipgloss.Height(tabs) + 1
	if page.tab == instructionTabRules {
		feedback := page.instructionFeedback(page.width)
		height := max(1, page.height-lipgloss.Height(tabs))
		help := page.rules.HelpView()
		layout := component.NewSectionLayout("", fmt.Sprintf("%d rules", len(page.settings.Rules)), feedback, page.width, height, lipgloss.Height(help))
		targets = append(targets, page.rules.MouseTargets(originX, contentY+layout.BodyY, z)...)
		helpY := contentY + height - lipgloss.Height(help)
		return append(targets, page.rules.HelpMouseTargets(originX, helpY, z+2)...)
	}
	if page.tab == instructionTabSources {
		feedback := page.instructionFeedback(page.width)
		layout := page.instructionSectionLayout(instructionTabSources, feedback, page.width, max(1, page.height-lipgloss.Height(tabs)-1))
		return append(targets, page.sourceMouseTargets(originX, contentY+layout.BodyY, z)...)
	}
	if page.tab == instructionTabContext && page.contextEditor == nil {
		feedback := page.instructionFeedback(page.width)
		help := page.contextHelp.View(page.width)
		layout := component.NewSectionLayout("", formatInstructionBytes(len([]byte(page.settings.Context))), feedback, page.width, max(1, page.height-lipgloss.Height(tabs)-1), lipgloss.Height(help))
		return append(targets, page.contextPreview.MouseTargets(originX, contentY+layout.BodyY, z)...)
	}
	return targets
}

func (page *InstructionPage) handleTabKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "1":
		return page.instructionTabNavigation(instructionTabContext), true
	case "2":
		return page.instructionTabNavigation(instructionTabRules), true
	case "3":
		return page.instructionTabNavigation(instructionTabSources), true
	}
	delta, ok := component.TabDelta(msg)
	if !ok {
		return nil, false
	}
	next := instructionTab(component.MoveTab(int(page.tab), len(instructionTabLabels), delta))
	return page.instructionTabNavigation(next), true
}

func (page *InstructionPage) instructionTabNavigation(tab instructionTab) tea.Cmd {
	if page == nil || int(tab) < 0 || int(tab) >= len(instructionTabLabels) || page.tab == tab {
		return nil
	}
	section := []string{"context", "rules", "sources"}[tab]
	return func() tea.Msg { return NavigateMsg{Path: []string{"instruction", section}, Replace: true} }
}

func (page *InstructionPage) switchTab(tab instructionTab) {
	if page == nil || int(tab) < 0 || int(tab) >= len(instructionTabLabels) || page.tab == tab {
		return
	}
	page.tab = tab
	page.err, page.notice = nil, ""
	page.syncDetail()
}

func (page *InstructionPage) syncDetail() {
	if page == nil {
		return
	}
	switch page.tab {
	case instructionTabContext:
		page.contextPreview = component.NewMarkdownViewer(page.settings.Context)
	case instructionTabRules:
		page.contextEditor = nil
		page.syncRuleBrowser()
	case instructionTabSources:
		page.contextEditor = nil
		page.syncSourceTree()
	}
	page.resizeContent()
}

func (page *InstructionPage) resizeContent() {
	if page == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	tabs := component.PageTabsNotice(instructionTabLabels, int(page.tab), page.notice, page.width)
	height := max(1, page.height-lipgloss.Height(tabs))
	if page.ruleEditor != nil {
		page.ruleEditor.Resize(page.width, page.height)
		return
	}
	if page.contextEditor != nil {
		layout := page.instructionSectionLayout(instructionTabContext, page.instructionFeedback(page.width), page.width, height)
		page.contextEditor.Resize(page.width, layout.BodyHeight)
		return
	}
	if page.tab == instructionTabContext {
		feedback := page.instructionFeedback(page.width)
		help := page.contextHelp.View(page.width)
		layout := component.NewSectionLayout("", formatInstructionBytes(len([]byte(page.settings.Context))), feedback, page.width, height, lipgloss.Height(help))
		page.contextPreview.Resize(page.width, layout.BodyHeight)
		return
	}
	if page.tab == instructionTabRules {
		help := page.rules.HelpView()
		layout := component.NewSectionLayout("", fmt.Sprintf("%d rules", len(page.settings.Rules)), page.instructionFeedback(page.width), page.width, height, lipgloss.Height(help))
		updated, _ := page.rules.Update(tea.WindowSizeMsg{Width: page.width, Height: layout.BodyHeight})
		page.rules = updated.(component.Browser)
		return
	}
	if page.tab == instructionTabSources {
		layout := page.instructionSectionLayout(instructionTabSources, page.instructionFeedback(page.width), page.width, height)
		page.sources.SetSize(page.width, layout.BodyHeight)
		return
	}
}

func (page *InstructionPage) instructionFeedback(width int) string {
	if page == nil || page.err == nil {
		return ""
	}
	return component.BannerWidth(page.err.Error(), component.ToneDanger, width)
}

func (page *InstructionPage) instructionSectionLayout(tab instructionTab, feedback string, width, height int) component.SectionLayout {
	context := page.settings.Context
	if page.contextEditor != nil {
		context = page.contextEditor.Value()
	}
	meta := formatInstructionBytes(len([]byte(context)))
	switch tab {
	case instructionTabRules:
		meta = fmt.Sprintf("%d rules", len(page.settings.Rules))
	case instructionTabSources:
		meta = fmt.Sprintf("%d providers", len(groupedInstructionSources(page.settings.DetectedSources)))
	}
	return component.NewSectionLayout("", meta, feedback, width, height, 0)
}

func (page *InstructionPage) refreshCmd() tea.Cmd {
	service := page.service
	return func() tea.Msg {
		settings, err := service.Load()
		return instructionRefreshMsg{settings: settings, err: err}
	}
}

func (page *InstructionPage) saveContextCmd(value string) tea.Cmd {
	service := page.service
	return func() tea.Msg {
		settings, err := service.Save(application.InstructionSettingsPatch{Context: &value})
		return instructionSavedMsg{settings: settings, err: err}
	}
}

func formatInstructionBytes(value int) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	if value < 10240 {
		return fmt.Sprintf("%.1f KB", float64(value)/1024)
	}
	return fmt.Sprintf("%.0f KB", float64(value)/1024)
}
