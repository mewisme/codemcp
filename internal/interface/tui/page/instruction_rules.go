package page

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type instructionRulesSavedMsg struct {
	settings application.InstructionSettings
	notice   string
	err      error
}

func (page *InstructionPage) syncRuleBrowser() {
	if page == nil {
		return
	}
	selectedID := ""
	helpExpanded := false
	if selected, ok := page.rules.Selected(); ok {
		selectedID = selected.ID
		helpExpanded = page.rules.HelpExpanded()
	}
	rows := make([]component.Row, 0, len(page.settings.Rules))
	for _, rule := range page.settings.Rules {
		state := "disabled"
		if rule.Enabled {
			state = "enabled"
		}
		name := strings.TrimSpace(rule.Name)
		if name == "" {
			name = rule.ID
		}
		rows = append(rows, component.Row{
			ID: rule.ID, Title: name, Description: rule.ID, Meta: state + " · " + formatInstructionBytes(len([]byte(rule.Content))),
			Search: strings.Join([]string{rule.ID, rule.Name, rule.Content, state}, " "),
		})
	}
	browser := component.NewBrowser(page.ctx, "Global Rules", rows, nil).WithTitleVisible(false).WithExternalHelp(true)
	browser.SetHelpBindings(
		component.Binding([]string{"a"}, "a", "add"),
		component.Binding([]string{"e"}, "e", "edit"),
		component.Binding([]string{"space"}, "space", "toggle"),
		component.Binding([]string{"d"}, "d", "delete"),
		component.Binding([]string{"r"}, "r", "refresh"),
	)
	browser.SetHelpExpanded(helpExpanded)
	if selectedID != "" {
		browser.SelectID(selectedID)
	}
	page.rules = browser
	page.resizeContent()
}

func (page *InstructionPage) handleRuleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if page == nil || page.tab != instructionTabRules || page.saving {
		return nil, false
	}
	switch msg.String() {
	case "a":
		return page.ruleEditorNavigation(""), true
	case "e":
		if id := page.selectedRuleID(); id != "" {
			return page.ruleEditorNavigation(id), true
		}
		return nil, true
	case "space":
		if id := page.selectedRuleID(); id != "" {
			page.saving = true
			page.err, page.notice = nil, ""
			return page.toggleRuleCmd(id), true
		}
		return nil, true
	case "d":
		if id := page.selectedRuleID(); id != "" {
			page.ruleDeleteID = id
			page.ruleConfirm = component.NewConfirmButtons("Delete", "Cancel", false)
			page.err, page.notice = nil, ""
		}
		return nil, true
	case "r":
		page.err, page.notice = nil, ""
		return page.refreshCmd(), true
	}
	return nil, false
}

func (page *InstructionPage) selectedRuleID() string {
	if page == nil {
		return ""
	}
	selected, ok := page.rules.Selected()
	if !ok {
		return ""
	}
	return selected.ID
}

func (page *InstructionPage) initRuleEditor(id, action string) error {
	page.ruleEditID, page.ruleName, page.ruleContent, page.ruleEnabled = strings.TrimSpace(id), "", "", true
	if action == "create" {
		if page.ruleEditID != "" {
			return fmt.Errorf("create rule route must not include a rule ID")
		}
		generated, err := application.NewInstructionRuleID()
		if err != nil {
			return err
		}
		page.ruleEditID = generated
		page.ruleName = "New rule"
	} else if action == "edit" {
		if page.ruleEditID == "" {
			return fmt.Errorf("edit rule route requires a rule ID")
		}
		rule, ok := page.ruleByID(page.ruleEditID)
		if !ok {
			return fmt.Errorf("global rule not found: %s", page.ruleEditID)
		}
		page.ruleName, page.ruleContent, page.ruleEnabled = rule.Name, rule.Content, rule.Enabled
	} else {
		return fmt.Errorf("unsupported instruction rule editor action: %s", action)
	}
	lines := 10
	if page.height > 0 {
		lines = max(5, min(16, page.height-12))
	}
	form := component.NewEditorForm(component.Group(
		component.Input("Name", &page.ruleName),
		component.Switch("Enabled", &page.ruleEnabled, "ENABLED", "DISABLED"),
		component.TextLines("Content", &page.ruleContent, lines),
	))
	primary := "save"
	if action == "create" {
		primary = "create"
	}
	editor := component.NewEditor(primary, component.EditorSection{ID: "rule", Title: "Rule", Description: page.ruleEditID, Form: form})
	page.ruleEditor = &editor
	page.err, page.notice = nil, ""
	page.resizeContent()
	return nil
}

func (page *InstructionPage) ruleEditorNavigation(id string) tea.Cmd {
	path := []string{"instruction", "rules", "create"}
	if id = strings.TrimSpace(id); id != "" {
		path = []string{"instruction", "rules", id, "edit"}
	}
	return func() tea.Msg { return NavigateMsg{Path: path} }
}

func (page *InstructionPage) ruleEditorParentNavigation() tea.Cmd {
	return func() tea.Msg { return NavigateMsg{Path: []string{"instruction", "rules"}, Replace: true} }
}

func (page *InstructionPage) ruleEditorView(width, height int) string {
	page.ruleEditor.Resize(width, height)
	return page.ruleEditor.View()
}

func (page *InstructionPage) ruleEditorMouseTargets(originX, originY, z int) []component.MouseTarget {
	return page.ruleEditor.MouseTargets(originX, originY, z)
}

func (page *InstructionPage) saveRuleEditorCmd() tea.Cmd {
	if page == nil {
		return nil
	}
	rules := append([]instructionpolicy.GlobalRule(nil), page.settings.Rules...)
	rule := instructionpolicy.GlobalRule{ID: page.ruleEditID, Name: strings.TrimSpace(page.ruleName), Enabled: page.ruleEnabled, Content: page.ruleContent}
	created := true
	for index := range rules {
		if rules[index].ID == rule.ID {
			rules[index] = rule
			created = false
			break
		}
	}
	if created {
		rules = append(rules, rule)
	}
	notice := "Global rule saved"
	if created {
		notice = "Global rule created"
	}
	return page.saveRulesCmd(rules, notice)
}

func (page *InstructionPage) toggleRuleCmd(id string) tea.Cmd {
	rules := append([]instructionpolicy.GlobalRule(nil), page.settings.Rules...)
	notice := "Global rule updated"
	for index := range rules {
		if rules[index].ID != id {
			continue
		}
		rules[index].Enabled = !rules[index].Enabled
		if rules[index].Enabled {
			notice = "Global rule enabled"
		} else {
			notice = "Global rule disabled"
		}
		break
	}
	return page.saveRulesCmd(rules, notice)
}

func (page *InstructionPage) saveRulesCmd(rules []instructionpolicy.GlobalRule, notice string) tea.Cmd {
	service := page.service
	rules = append([]instructionpolicy.GlobalRule(nil), rules...)
	return func() tea.Msg {
		settings, err := service.Save(application.InstructionSettingsPatch{Rules: &rules})
		return instructionRulesSavedMsg{settings: settings, notice: notice, err: err}
	}
}

func (page *InstructionPage) updateRuleConfirm(msg tea.KeyPressMsg) tea.Cmd {
	if page == nil || page.ruleDeleteID == "" || page.saving {
		return nil
	}
	switch msg.String() {
	case "esc":
		page.closeRuleConfirm()
		return nil
	case "enter":
		if !page.ruleConfirm.AffirmativeSelected() {
			page.closeRuleConfirm()
			return nil
		}
		rules := make([]instructionpolicy.GlobalRule, 0, len(page.settings.Rules))
		for _, rule := range page.settings.Rules {
			if rule.ID != page.ruleDeleteID {
				rules = append(rules, rule)
			}
		}
		page.saving = true
		return page.saveRulesCmd(rules, "Global rule deleted")
	default:
		return page.ruleConfirm.Update(msg)
	}
}

func (page *InstructionPage) closeRuleConfirm() {
	if page == nil {
		return
	}
	page.ruleDeleteID = ""
	page.ruleConfirm = component.ConfirmButtons{}
}

func (page *InstructionPage) ruleByID(id string) (instructionpolicy.GlobalRule, bool) {
	if page == nil {
		return instructionpolicy.GlobalRule{}, false
	}
	for _, rule := range page.settings.Rules {
		if rule.ID == id {
			return rule, true
		}
	}
	return instructionpolicy.GlobalRule{}, false
}

func (page *InstructionPage) rulesView(tabs string, width, bodyHeight int) string {
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
	}
	help := page.rules.HelpView()
	layout := component.NewSectionLayout("", fmt.Sprintf("%d rules", len(page.settings.Rules)), feedback, width, bodyHeight, lipgloss.Height(help))
	updated, _ := page.rules.Update(tea.WindowSizeMsg{Width: width, Height: layout.BodyHeight})
	page.rules = updated.(component.Browser)
	content := tabs + "\n" + component.BottomHelp(layout.View(page.rules.BodyContent()), help, width, bodyHeight)
	if page.ruleDeleteID == "" {
		return content
	}
	modalWidth := overlayWidth(width, 64)
	modal := component.Modal(confirmOverlayBody(page.ruleConfirm, page.ruleDeleteTitle(), "This removes the managed global rule. Project files are unchanged.", modalWidth), modalWidth)
	return component.CenterOverlay(content, modal, width, page.height)
}

func (page *InstructionPage) ruleDeleteTitle() string {
	if rule, ok := page.ruleByID(page.ruleDeleteID); ok {
		name := strings.TrimSpace(rule.Name)
		if name == "" {
			name = rule.ID
		}
		return "Delete global rule " + name + "?"
	}
	return "Delete global rule?"
}

func (page *InstructionPage) ruleConfirmMouseTargets(originX, originY, z int) []component.MouseTarget {
	return confirmOverlayMouseTargets(
		page.ruleConfirm, page.ruleDeleteTitle(), "This removes the managed global rule. Project files are unchanged.",
		overlayWidth(page.width, 64), page.width, page.height, originX, originY, z+20,
	)
}
