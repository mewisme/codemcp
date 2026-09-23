package page

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/tui/component"
)

func TestInstructionPageShowsContextAndReadOnlySummaries(t *testing.T) {
	page, _ := newTestInstructionPage(t)
	plain := ansi.Strip(page.View(100, 30))
	for _, want := range []string{"Context", "Rules", "Sources", "Shared instructions", "e edit", "r refresh"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("context view missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Global Context") {
		t.Fatalf("context tab repeated root title: %q", plain)
	}

	page.switchTab(instructionTabRules)
	plain = ansi.Strip(page.View(100, 30))
	for _, want := range []string{"One global rule", "enabled", "rule_one"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rules view missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Global Rules") {
		t.Fatalf("rules tab repeated root title: %q", plain)
	}

	page.switchTab(instructionTabSources)
	plain = ansi.Strip(page.View(100, 30))
	for _, want := range []string{"Providers", "Claude · enabled", "Context · 1 · detected"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("sources view missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Instruction Sources") {
		t.Fatalf("sources tab repeated root title: %q", plain)
	}
}

func TestInstructionRouteSelectsRequestedTab(t *testing.T) {
	_, service := newTestInstructionPage(t)
	for section, want := range map[string]instructionTab{"": instructionTabContext, "context": instructionTabContext, "rules": instructionTabRules, "sources": instructionTabSources} {
		page, err := newInstructionPageRoute(t.Context(), service, section)
		if err != nil {
			t.Fatalf("section %q: %v", section, err)
		}
		if page.tab != want {
			t.Fatalf("section %q tab=%d want=%d", section, page.tab, want)
		}
	}
	if _, err := newInstructionPageRoute(t.Context(), service, "missing"); err == nil {
		t.Fatal("invalid instruction route section was accepted")
	}
}

func TestInstructionPageManagesSourcePolicy(t *testing.T) {
	page, service := newTestInstructionPage(t)
	page.switchTab(instructionTabSources)
	page.sources.Down()
	updated, cmd := page.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	page = updated.(*InstructionPage)
	if cmd == nil || !page.saving {
		t.Fatalf("provider toggle cmd=%v saving=%t", cmd, page.saving)
	}
	updated, _ = page.Update(cmd())
	page = updated.(*InstructionPage)
	settings, err := service.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy := settings.SourcePolicy["claude"]
	if policy.Enabled == nil || *policy.Enabled || page.Notice() != "Claude source disabled" {
		t.Fatalf("provider policy=%#v notice=%q", policy, page.Notice())
	}
	if len(settings.DetectedSources) != 1 || settings.DetectedSources[0].Enabled {
		t.Fatalf("disabled source=%#v", settings.DetectedSources)
	}

	page.sources.Down()
	page.sources.Down()
	updated, cmd = page.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	page = updated.(*InstructionPage)
	if cmd != nil || page.Notice() != "Enable the provider before changing resource policy" {
		t.Fatalf("disabled child cmd=%v notice=%q", cmd, page.Notice())
	}

	page.sources.GoToTop()
	page.sources.Down()
	updated, cmd = page.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	page = updated.(*InstructionPage)
	updated, _ = page.Update(cmd())
	page = updated.(*InstructionPage)
	settings, err = service.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy = settings.SourcePolicy["claude"]
	if policy.Enabled == nil || !*policy.Enabled {
		t.Fatalf("provider was not re-enabled: %#v", policy)
	}

	page.sources.Down()
	page.sources.Down()
	updated, cmd = page.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	page = updated.(*InstructionPage)
	if cmd == nil {
		t.Fatal("resource toggle returned no command")
	}
	updated, _ = page.Update(cmd())
	page = updated.(*InstructionPage)
	settings, err = service.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy = settings.SourcePolicy["claude"]
	if policy.Context == nil || *policy.Context || page.Notice() != "Claude Context disabled" {
		t.Fatalf("resource policy=%#v notice=%q", policy, page.Notice())
	}
}

func TestInstructionSourcesMouseWheelUsesSemanticMessage(t *testing.T) {
	page, _ := newTestInstructionPage(t)
	page.switchTab(instructionTabSources)
	page.View(100, 30)
	targets := page.sourceMouseTargets(2, 4, 10)
	if len(targets) != 1 || targets[0].ID != "instruction.sources.scroll" {
		t.Fatalf("targets=%#v", targets)
	}
	message, ok := targets[0].Handle(component.MouseEvent{Button: tea.MouseWheelDown}).(instructionSourceWheelMsg)
	if !ok || message != 1 {
		t.Fatalf("wheel message=%#v", message)
	}
}

func TestInstructionPageEditsAndSavesGlobalContext(t *testing.T) {
	_, service := newTestInstructionPage(t)
	page, err := newInstructionPageRouteAction(t.Context(), service, "context", "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	if page.contextEditor == nil || !page.InputActive() || page.Init() == nil {
		t.Fatalf("editor active=%t input=%t init=%v", page.contextEditor != nil, page.InputActive(), page.Init() != nil)
	}
	updated, saveCmd := page.Update(component.TextAreaSavedMsg{Value: "# Updated\n\nUse pnpm."})
	page = updated.(*InstructionPage)
	if saveCmd == nil || !page.saving {
		t.Fatalf("save cmd=%v saving=%t", saveCmd, page.saving)
	}
	updated, navigation := page.Update(saveCmd())
	page = updated.(*InstructionPage)
	if page.contextEditor != nil || page.saving || page.Notice() != "Global context saved" || navigation == nil {
		t.Fatalf("editor=%v saving=%t notice=%q navigation=%v err=%v", page.contextEditor != nil, page.saving, page.Notice(), navigation != nil, page.err)
	}
	settings, err := service.Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Context != "# Updated\n\nUse pnpm." {
		t.Fatalf("stored context=%q", settings.Context)
	}
	if plain := ansi.Strip(page.View(100, 30)); !strings.Contains(plain, "Use pnpm.") {
		t.Fatalf("updated context not rendered: %q", plain)
	}
}

func TestInstructionContextDefaultsToMarkdownPreviewAndEditIsRouted(t *testing.T) {
	page, _ := newTestInstructionPage(t)
	if page.contextEditor != nil || page.InputActive() {
		t.Fatalf("context unexpectedly mounted editor=%v input=%t", page.contextEditor != nil, page.InputActive())
	}
	plain := ansi.Strip(page.View(72, 20))
	if !strings.Contains(plain, "Shared instructions") || !strings.Contains(plain, "Prefer compact code.") || !strings.Contains(plain, "e edit") {
		t.Fatalf("context preview=%q", plain)
	}
	_, cmd := page.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmd == nil {
		t.Fatal("context edit returned no navigation")
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "instruction/context/edit" {
		t.Fatalf("context edit navigation=%#v", navigate)
	}
}

func TestInstructionContextPreviewLeavesTabNavigationKeysToPage(t *testing.T) {
	page, _ := newTestInstructionPage(t)
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: 'l', Text: "l"}, {Code: '2', Text: "2"}} {
		_, cmd := page.Update(key)
		if cmd == nil {
			t.Fatalf("tab navigation key %q returned no command", key.String())
		}
		navigate, ok := cmd().(NavigateMsg)
		if !ok || strings.Join(navigate.Path, "/") != "instruction/rules" || !navigate.Replace {
			t.Fatalf("tab navigation key %q message=%#v", key.String(), navigate)
		}
	}
}

func TestInstructionSourcesFirstRenderUsesUsableTreeWidth(t *testing.T) {
	page, _ := newTestInstructionPage(t)
	page.switchTab(instructionTabSources)
	view := ansi.Strip(page.View(64, 18))
	if !strings.Contains(view, "Claude · enabled") || !strings.Contains(view, "Context · 1 · detected") {
		t.Fatalf("sources first render=%q", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 64 {
			t.Fatalf("sources first render overflow width=%d line=%q", lipgloss.Width(line), line)
		}
	}
}

func TestInstructionPageRefreshesExternalChanges(t *testing.T) {
	page, service := newTestInstructionPage(t)
	value := "Externally updated"
	if _, err := service.Save(application.InstructionSettingsPatch{Context: &value}); err != nil {
		t.Fatal(err)
	}
	cmd := page.refreshCmd()
	if cmd == nil {
		t.Fatal("refresh command is nil")
	}
	updated, _ := page.Update(cmd())
	page = updated.(*InstructionPage)
	if page.settings.Context != value || page.Notice() != "Instructions refreshed" {
		t.Fatalf("context=%q notice=%q err=%v", page.settings.Context, page.Notice(), page.err)
	}
}

func TestInstructionPageMouseTabsUseSemanticMessage(t *testing.T) {
	page, _ := newTestInstructionPage(t)
	page.View(100, 30)
	targets := page.MouseTargets(2, 3, 10)
	var sourceTarget *component.MouseTarget
	for index := range targets {
		if targets[index].ID != "instruction.tab" {
			continue
		}
		message := targets[index].Handle(component.MouseEvent{Button: tea.MouseLeft})
		if tab, ok := message.(instructionTabMsg); ok && tab.Tab == instructionTabSources {
			sourceTarget = &targets[index]
			break
		}
	}
	if sourceTarget == nil {
		t.Fatal("sources tab target not found")
	}
	message := sourceTarget.Handle(component.MouseEvent{Button: tea.MouseLeft})
	updated, cmd := page.Update(message)
	page = updated.(*InstructionPage)
	if page.tab != instructionTabContext || cmd == nil {
		t.Fatalf("tab=%d cmd=%v", page.tab, cmd != nil)
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "instruction/sources" || !navigate.Replace {
		t.Fatalf("navigation=%#v", navigate)
	}
}

func TestInstructionPageManagesGlobalRules(t *testing.T) {
	page, service := newTestInstructionPage(t)
	page.switchTab(instructionTabRules)
	updated, cmd := page.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	page = updated.(*InstructionPage)
	if cmd == nil || !page.saving {
		t.Fatalf("toggle cmd=%v saving=%t", cmd, page.saving)
	}
	updated, _ = page.Update(cmd())
	page = updated.(*InstructionPage)
	settings, err := service.Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Rules[0].Enabled || page.Notice() != "Global rule disabled" {
		t.Fatalf("toggle rule=%#v notice=%q", settings.Rules[0], page.Notice())
	}

	_, cmd = page.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmd == nil {
		t.Fatal("edit route returned no command")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "instruction/rules/rule_one/edit" {
		t.Fatalf("edit navigation=%#v", message)
	}
	edit, err := newInstructionPageRouteAction(t.Context(), service, "rules", "rule_one", "edit")
	if err != nil {
		t.Fatal(err)
	}
	editView := ansi.Strip(edit.View(100, 30))
	if edit.ruleEditor == nil || edit.ruleEditID != "rule_one" || !strings.Contains(editView, "rule_one") || !strings.Contains(editView, "enter next") || strings.Contains(editView, "Edit Global Rule") {
		t.Fatalf("edit route editor=%v id=%q view=%q", edit.ruleEditor != nil, edit.ruleEditID, editView)
	}
	edit.ruleName, edit.ruleContent, edit.ruleEnabled = "Updated rule", "Updated content", true
	_, cmd = edit.Update(edit.saveRuleEditorCmd()())
	if cmd == nil || edit.Dirty() {
		t.Fatalf("edit save navigation=%v dirty=%t", cmd != nil, edit.Dirty())
	}
	settings, err = service.Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Rules[0].Name != "Updated rule" || settings.Rules[0].Content != "Updated content" || !settings.Rules[0].Enabled {
		t.Fatalf("edited rule=%#v", settings.Rules[0])
	}

	create, err := newInstructionPageRouteAction(t.Context(), service, "rules", "", "create")
	if err != nil {
		t.Fatal(err)
	}
	if create.ruleEditor == nil || !strings.HasPrefix(create.ruleEditID, "rule_") || create.ruleEditID == "rule_one" {
		t.Fatalf("create editor=%v id=%q", create.ruleEditor != nil, create.ruleEditID)
	}
	createdID := create.ruleEditID
	create.ruleName, create.ruleContent, create.ruleEnabled = "Second rule", "Second content", true
	_, cmd = create.Update(create.saveRuleEditorCmd()())
	if cmd == nil {
		t.Fatal("create save did not navigate to parent")
	}
	settings, err = service.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Rules) != 2 || settings.Rules[1].ID != createdID {
		t.Fatalf("created rules=%#v", settings.Rules)
	}

	page, err = newInstructionPageRoute(t.Context(), service, "rules")
	if err != nil {
		t.Fatal(err)
	}
	if !page.rules.SelectID(createdID) {
		t.Fatalf("created rule %q not selectable", createdID)
	}
	updated, _ = page.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	page = updated.(*InstructionPage)
	if page.ruleDeleteID != createdID || !page.OverlayActive() {
		t.Fatalf("delete id=%q overlay=%t", page.ruleDeleteID, page.OverlayActive())
	}
	updated, cmd = page.Update(component.ConfirmChoiceMsg{Affirmative: true})
	page = updated.(*InstructionPage)
	if cmd == nil || !page.saving {
		t.Fatalf("delete cmd=%v saving=%t", cmd, page.saving)
	}
	updated, _ = page.Update(cmd())
	page = updated.(*InstructionPage)
	settings, err = service.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Rules) != 1 || settings.Rules[0].ID != "rule_one" || page.OverlayActive() {
		t.Fatalf("deleted rules=%#v overlay=%t", settings.Rules, page.OverlayActive())
	}
}

func TestInstructionRuleBrowserSearchesRuleContent(t *testing.T) {
	page, _ := newTestInstructionPage(t)
	page.switchTab(instructionTabRules)
	page.rules.StartFilter()
	updated, _ := page.Update(tea.KeyPressMsg{Text: "verify", Code: 'v'})
	page = updated.(*InstructionPage)
	if !page.rules.InputActive() {
		t.Fatal("rule browser filter is not active")
	}
}

func TestInstructionTabsKeepSectionGeometryAcrossSizesAndThemes(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {100, 30}} {
		for _, dark := range []bool{false, true} {
			page, _ := newTestInstructionPage(t)
			color := lipgloss.Color("#ffffff")
			if dark {
				color = lipgloss.Color("#000000")
			}
			updated, _ := page.Update(tea.BackgroundColorMsg{Color: color})
			page = updated.(*InstructionPage)
			var bodyY, bodyHeight int
			for _, tab := range []instructionTab{instructionTabContext, instructionTabRules, instructionTabSources} {
				page.switchTab(tab)
				view := page.View(size[0], size[1])
				if width, height := lipgloss.Width(view), lipgloss.Height(view); width > size[0] || height > size[1] {
					t.Fatalf("size=%v dark=%t tab=%d rendered=%dx%d", size, dark, tab, width, height)
				}
				tabs := component.PageTabsNotice(instructionTabLabels, int(tab), page.notice, size[0])
				layout := page.instructionSectionLayout(tab, page.instructionFeedback(size[0]), size[0], max(1, size[1]-lipgloss.Height(tabs)-1))
				if tab == instructionTabContext {
					bodyY, bodyHeight = layout.BodyY, layout.BodyHeight
				} else if layout.BodyY != bodyY || layout.BodyHeight != bodyHeight {
					t.Fatalf("size=%v dark=%t tab=%d body=%d/%d want=%d/%d", size, dark, tab, layout.BodyY, layout.BodyHeight, bodyY, bodyHeight)
				}
			}
		}
	}
}

func newTestInstructionPage(t *testing.T) (*InstructionPage, *application.InstructionSettingsService) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("User context"), 0644); err != nil {
		t.Fatal(err)
	}
	store := &instructionpolicy.Store{Path: filepath.Join(t.TempDir(), "global.json")}
	value := instructionpolicy.DefaultConfig()
	value.Context = "# Shared instructions\n\nPrefer compact code."
	value.Rules = []instructionpolicy.GlobalRule{{ID: "rule_one", Name: "One global rule", Enabled: true, Content: "Always verify."}}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	service := application.NewInstructionSettingsService(store)
	service.UserHomeDir = func() (string, error) { return home, nil }
	page, err := newInstructionPage(t.Context(), service)
	if err != nil {
		t.Fatal(err)
	}
	return page, service
}
