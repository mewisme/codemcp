package page

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type LLMCommand string

const (
	LLMRefresh         LLMCommand = "llm.refresh"
	LLMAdd             LLMCommand = "llm.add"
	LLMEdit            LLMCommand = "llm.edit"
	LLMUse             LLMCommand = "llm.use"
	LLMProbe           LLMCommand = "llm.probe"
	LLMModels          LLMCommand = "llm.models"
	LLMModelQuery      LLMCommand = "llm.models.query"
	LLMModelRefresh    LLMCommand = "llm.models.refresh"
	LLMCredential      LLMCommand = "llm.credential"
	LLMCredentialClear LLMCommand = "llm.credential.clear"
	LLMRemove          LLMCommand = "llm.remove"
)

type LLMCommandMsg struct {
	Command    LLMCommand
	ResourceID string
}

type llmMutationMsg struct {
	notice   string
	err      error
	removed  string
	navigate []string
}

type llmModelsMsg struct {
	page application.LLMModelPage
	err  error
}

type llmOverlay uint8

const (
	llmOverlayNone llmOverlay = iota
	llmOverlayConfirm
)

type llmSessionViewState struct {
	SelectedProvider string
	SelectedModel    string
	Query            application.LLMModelQuery
	HelpExpanded     bool
}

type LLMPage struct {
	ctx        context.Context
	service    *application.LLMService
	resourceID string
	section    string
	action     string

	status    application.LLMStatusResult
	providers []application.LLMProviderResult
	catalog   application.LLMModelPage
	query     application.LLMModelQuery

	browser         component.Browser
	browserReady    bool
	detail          component.DetailPage
	detailReady     bool
	editor          *component.Editor
	restoreSelected string

	providerForm   *llmProviderFormData
	credentialForm *llmCredentialFormData
	modelForm      *llmModelFormData
	queryForm      *llmModelQueryFormData

	overlay        llmOverlay
	confirm        component.ConfirmButtons
	confirmCommand LLMCommand
	targetID       string

	notice string
	err    error
	width  int
	height int
}

func NewLLMRouteAction(ctx context.Context, resourceID, section, action string) (*LLMPage, error) {
	return newLLMRouteAction(ctx, resourceID, section, action, application.NewLLMService(config.RootPath()))
}

func newLLMRouteAction(ctx context.Context, resourceID, section, action string, service *application.LLMService) (*LLMPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if service == nil {
		return nil, fmt.Errorf("LLM service is required")
	}
	page := &LLMPage{
		ctx: ctx, service: service,
		resourceID: strings.TrimSpace(resourceID), section: strings.TrimSpace(section), action: strings.TrimSpace(action),
		query: application.LLMModelQuery{Limit: 25},
	}
	if err := page.reload(); err != nil {
		return nil, err
	}
	if err := page.initRouteEditor(); err != nil {
		return nil, err
	}
	return page, nil
}

func (page *LLMPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	var commands []tea.Cmd
	if page.editor != nil {
		commands = append(commands, page.editor.Init())
	}
	if page.resourceID != "" && page.section == "models" && page.editor == nil {
		commands = append(commands, page.loadModelsCmd(false))
	}
	return tea.Batch(commands...)
}

func (page *LLMPage) OverlayActive() bool {
	return page != nil && page.overlay != llmOverlayNone
}

func (page *LLMPage) InputActive() bool {
	return page != nil && (page.editor != nil || page.browser.InputActive())
}

func (page *LLMPage) Dirty() bool {
	return page != nil && page.editor != nil && page.editor.Dirty()
}

func (page *LLMPage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *LLMPage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *LLMPage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *LLMPage) SessionViewState() any {
	if page == nil {
		return llmSessionViewState{}
	}
	state := llmSessionViewState{Query: page.query, HelpExpanded: page.browser.HelpExpanded()}
	if row, ok := page.browser.Selected(); ok {
		if page.section == "models" {
			state.SelectedModel = row.ID
		} else {
			state.SelectedProvider = row.ID
		}
	}
	return state
}

func (page *LLMPage) RestoreSessionViewState(value any) {
	if page == nil {
		return
	}
	state, ok := value.(llmSessionViewState)
	if !ok {
		return
	}
	page.query = state.Query
	if page.query.Limit <= 0 {
		page.query.Limit = 25
	}
	page.browser.SetHelpExpanded(state.HelpExpanded)
	if page.section == "models" {
		page.restoreSelected = state.SelectedModel
		page.browser.SelectID(state.SelectedModel)
	} else {
		page.browser.SelectID(state.SelectedProvider)
	}
}

func (page *LLMPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		if page.resourceID != "" && page.section != "models" && page.editor == nil {
			if err := page.syncDetail(); err != nil {
				page.err = err
			}
		}
		page.resize()
		return page, nil
	case llmMutationMsg:
		return page, page.finishMutation(msg)
	case llmModelsMsg:
		return page, page.finishModels(msg)
	case component.EditorSubmitMsg:
		return page, page.submitEditor()
	case component.EditorCancelMsg:
		return page, page.closeEditor()
	case component.ConfirmChoiceMsg:
		if page.overlay == llmOverlayConfirm {
			page.confirm.Select(msg.Affirmative)
			return page, page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case LLMCommandMsg:
		return page, page.handleCommand(msg.Command, msg.ResourceID)
	case component.BrowserOpenMsg:
		if msg.Row.ID == "" {
			return page, nil
		}
		if page.resourceID == "" {
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"llm", msg.Row.ID}} }
		}
		if page.section == "models" {
			return page, page.setModelCmd(msg.Row.ID)
		}
		return page, nil
	}

	if page.overlay == llmOverlayConfirm {
		if msg, ok := message.(tea.KeyPressMsg); ok {
			return page, page.updateConfirm(msg)
		}
		return page, nil
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return page, cmd
	}

	if msg, ok := message.(tea.KeyPressMsg); ok {
		if page.browser.InputActive() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		if cmd, handled := page.handleKey(msg); handled {
			return page, cmd
		}
	}
	if page.resourceID != "" && page.section != "models" {
		updated, cmd := page.detail.Update(message)
		page.detail = updated
		return page, cmd
	}
	updated, cmd := page.browser.Update(message)
	page.browser = updated.(component.Browser)
	return page, cmd
}

func (page *LLMPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "LLM administration unavailable", "")
	}
	page.width, page.height = width, height
	page.resize()
	var content string
	switch {
	case page.editor != nil:
		content = page.editor.View()
	case page.resourceID != "" && page.section != "models":
		page.detail.SetFeedback(page.notice, page.err)
		content = page.detail.View()
	default:
		page.browser.SetTitleNotice(page.notice)
		feedback := ""
		if page.err != nil {
			feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
		}
		content = prependPageFeedback(feedback, page.browser.Content())
	}
	if page.overlay == llmOverlayConfirm {
		modalWidth := overlayWidth(width, 68)
		body := confirmOverlayBody(page.confirm, page.confirmTitle(), page.confirmDescription(), modalWidth)
		content = component.CenterOverlay(content, component.Modal(body, modalWidth), width, height)
	}
	return content
}

func (page *LLMPage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	if page.overlay == llmOverlayConfirm {
		return append([]component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}, page.confirm.MouseTargets(originX, originY, z+21)...)
	}
	if page.editor != nil {
		return page.editor.MouseTargets(originX, originY, z)
	}
	if page.resourceID != "" && page.section != "models" {
		return page.detail.MouseTargets(originX, originY, z)
	}
	return page.browser.MouseTargets(originX, originY, z)
}

func (page *LLMPage) reload() error {
	status, err := page.service.Status(page.ctx)
	if err != nil {
		return err
	}
	page.status = status
	page.providers = append(page.providers[:0], status.Providers...)
	if page.resourceID == "" {
		selected := ""
		help := page.browser.HelpExpanded()
		if row, ok := page.browser.Selected(); ok {
			selected = row.ID
		}
		rows := page.providerRows()
		if !page.browserReady {
			page.browser = component.NewBrowser(page.ctx, "LLM providers", rows, nil).WithHelpBindings(
				component.Binding([]string{"a"}, "a", "add"),
				component.Binding([]string{"r"}, "r", "refresh"),
			)
			page.browserReady = true
		} else {
			page.browser.ReplaceRows(rows, selected)
		}
		page.browser.SetHelpExpanded(help)
		return nil
	}
	if page.section != "models" {
		return page.syncDetail()
	}
	page.ensureModelsBrowser()
	return nil
}

func (page *LLMPage) ensureModelsBrowser() {
	if page == nil || page.browserReady {
		return
	}
	page.browser = component.NewBrowser(page.ctx, "Models · "+page.resourceID, nil, nil).WithHelpBindings(
		component.Binding([]string{"s"}, "s", "set exact"),
		component.Binding([]string{"q"}, "q", "query"),
		component.Binding([]string{"r"}, "r", "refresh"),
		component.Binding([]string{"b"}, "b", "previous"),
		component.Binding([]string{"n"}, "n", "next"),
	)
	page.browserReady = true
}

func (page *LLMPage) providerRows() []component.Row {
	rows := make([]component.Row, 0, len(page.providers))
	for _, provider := range page.providers {
		state := string(provider.Readiness)
		if provider.Selected {
			state = "active · " + state
		}
		kind := "custom"
		if provider.Core {
			kind = "core"
		}
		description := strings.TrimSpace(provider.Model)
		if description == "" {
			description = "No model configured"
		}
		rows = append(rows, component.Row{
			ID: string(provider.ID), Title: provider.Name,
			Description: string(provider.ID) + " · " + description,
			Meta:        kind + " · " + state,
			Search:      strings.Join([]string{string(provider.ID), provider.Name, provider.BaseURL, provider.Model, string(provider.Protocol), state}, " "),
		})
	}
	return rows
}

func (page *LLMPage) syncDetail() error {
	provider, err := page.service.ProviderResult(page.ctx, page.resourceID)
	if err != nil {
		return err
	}
	contentWidth := page.width
	if contentWidth <= 0 {
		contentWidth = 80
	}
	lines := []string{
		component.WrapKeyValue("Provider", provider.Name, contentWidth),
		component.WrapKeyValue("ID", string(provider.ID), contentWidth),
		component.WrapKeyValue("State", string(provider.Readiness), contentWidth),
		component.WrapKeyValue("Active", yesNo(provider.Selected), contentWidth),
		component.WrapKeyValue("Identity", map[bool]string{true: "core · immutable", false: "custom"}[provider.Core], contentWidth),
		component.WrapKeyValue("Protocol", string(provider.Protocol), contentWidth),
		component.WrapKeyValue("Endpoint", provider.BaseURL, contentWidth),
		component.WrapKeyValue("Model", fallback(provider.Model, "Not configured"), contentWidth),
		component.WrapKeyValue("Discovery", string(provider.Discovery), contentWidth),
		component.WrapKeyValue("Credential", credentialLabel(provider), contentWidth),
	}
	if provider.Reason != "" {
		lines = append(lines, "", component.BannerWidth(provider.Reason, component.ToneWarning, contentWidth))
	}
	content := strings.Join(lines, "\n")
	if !page.detailReady {
		page.detail = component.NewDetailPage("LLM provider", string(provider.ID), content).WithTitleVisible(false)
		page.detailReady = true
	} else {
		page.detail.SetTitle("LLM provider")
		page.detail.SetMeta(string(provider.ID))
		page.detail.SetContentPreserveScroll(content)
	}
	bindings := []component.DetailPageBinding{
		{Key: "m", Desc: "models", Message: NavigateMsg{Path: []string{"llm", page.resourceID, "models"}}},
		{Key: "p", Desc: "probe", Message: LLMCommandMsg{Command: LLMProbe, ResourceID: page.resourceID}},
		{Key: "k", Desc: "set key", Message: NavigateMsg{Path: []string{"llm", page.resourceID, "credential"}}},
	}
	if !provider.Selected {
		bindings = append(bindings, component.DetailPageBinding{Key: "u", Desc: "use", Message: LLMCommandMsg{Command: LLMUse, ResourceID: page.resourceID}})
	}
	if provider.Credential.Configured {
		bindings = append(bindings, component.DetailPageBinding{Key: "c", Desc: "clear key", Message: LLMCommandMsg{Command: LLMCredentialClear, ResourceID: page.resourceID}})
	}
	if !provider.Core {
		bindings = append(bindings,
			component.DetailPageBinding{Key: "e", Desc: "edit", Message: NavigateMsg{Path: []string{"llm", page.resourceID, "edit"}}},
			component.DetailPageBinding{Key: "d", Desc: "remove", Message: LLMCommandMsg{Command: LLMRemove, ResourceID: page.resourceID}},
		)
	}
	page.detail.SetBindings(bindings...)
	return nil
}

func (page *LLMPage) initRouteEditor() error {
	switch page.action {
	case "":
		return nil
	case "create":
		editor, data := newLLMProviderEditor(nil)
		page.editor, page.providerForm = &editor, data
	case "edit":
		provider, err := page.service.ProviderResult(page.ctx, page.resourceID)
		if err != nil {
			return err
		}
		if provider.Core {
			return fmt.Errorf("core provider identity is immutable")
		}
		editor, data := newLLMProviderEditor(&provider)
		page.editor, page.providerForm = &editor, data
	case "credential":
		provider, err := page.service.ProviderResult(page.ctx, page.resourceID)
		if err != nil {
			return err
		}
		editor, data := newLLMCredentialEditor(provider)
		page.editor, page.credentialForm = &editor, data
	case "query":
		if page.section != "models" {
			return fmt.Errorf("model query editor requires models section")
		}
		editor, data := newLLMModelQueryEditor(page.query)
		page.editor, page.queryForm = &editor, data
	case "set":
		if page.section != "models" {
			return fmt.Errorf("model editor requires models section")
		}
		provider, err := page.service.ProviderResult(page.ctx, page.resourceID)
		if err != nil {
			return err
		}
		editor, data := newLLMModelEditor(provider)
		page.editor, page.modelForm = &editor, data
	default:
		return fmt.Errorf("unsupported LLM action: %s", page.action)
	}
	page.resize()
	return nil
}

func (page *LLMPage) submitEditor() tea.Cmd {
	if page.editor == nil {
		return nil
	}
	switch {
	case page.providerForm != nil:
		data := *page.providerForm
		id := strings.TrimSpace(data.ID)
		if page.resourceID != "" {
			id = page.resourceID
		}
		cfg := application.NewCustomLLMProviderConfig(data.Name, data.Protocol, data.BaseURL, data.Model, data.AuthMode, data.Discovery)
		page.editor.SetSubmitting(true)
		if page.resourceID == "" {
			return func() tea.Msg {
				_, err := page.service.AddCustomProvider(page.ctx, id, cfg)
				return llmMutationMsg{notice: "Provider added", err: err, navigate: []string{"llm", id}}
			}
		}
		return func() tea.Msg {
			_, err := page.service.ConfigureCustomProvider(page.ctx, id, cfg)
			return llmMutationMsg{notice: "Provider updated", err: err, navigate: []string{"llm", id}}
		}
	case page.credentialForm != nil:
		value := strings.TrimSpace(page.credentialForm.Value)
		page.credentialForm.Value = ""
		page.editor.SetSubmitting(true)
		id := page.resourceID
		return func() tea.Msg {
			err := page.service.SetCredential(page.ctx, id, value)
			return llmMutationMsg{notice: "Credential updated", err: err, navigate: []string{"llm", id}}
		}
	case page.modelForm != nil:
		modelID := strings.TrimSpace(page.modelForm.Model)
		page.editor.SetSubmitting(true)
		id := page.resourceID
		return func() tea.Msg {
			_, err := page.service.SetProviderModel(page.ctx, id, modelID)
			return llmMutationMsg{notice: "Model set to " + modelID, err: err, navigate: []string{"llm", id}}
		}
	case page.queryForm != nil:
		query, err := page.queryForm.Query()
		if err != nil {
			page.editor.SetFeedback("", err)
			return nil
		}
		page.query = query
		page.editor, page.queryForm, page.action = nil, nil, ""
		return tea.Batch(page.loadModelsCmd(false), func() tea.Msg {
			return NavigateMsg{Path: []string{"llm", page.resourceID, "models"}, Replace: true, PreservePage: true}
		})
	default:
		return nil
	}
}

func (page *LLMPage) closeEditor() tea.Cmd {
	id, section := page.resourceID, page.section
	page.editor, page.providerForm, page.credentialForm, page.modelForm, page.queryForm, page.action = nil, nil, nil, nil, nil, ""
	path := []string{"llm"}
	if id != "" {
		path = append(path, id)
	}
	if section != "" {
		path = append(path, section)
	}
	return func() tea.Msg { return NavigateMsg{Path: path, Replace: true, PreservePage: true} }
}

func (page *LLMPage) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch {
	case page.resourceID == "":
		switch msg.String() {
		case "a":
			return func() tea.Msg { return NavigateMsg{Path: []string{"llm", "create"}} }, true
		case "r":
			return page.refreshCmd(), true
		}
	case page.section == "models":
		switch msg.String() {
		case "s":
			return func() tea.Msg { return NavigateMsg{Path: []string{"llm", page.resourceID, "models", "set"}} }, true
		case "q":
			return func() tea.Msg { return NavigateMsg{Path: []string{"llm", page.resourceID, "models", "query"}} }, true
		case "r":
			return page.loadModelsCmd(true), true
		case "n":
			if page.catalog.HasMore {
				page.query.Offset = page.catalog.Offset + page.catalog.Returned
				return page.loadModelsCmd(false), true
			}
		case "b":
			if page.catalog.Offset > 0 {
				page.query.Offset = max(0, page.catalog.Offset-page.catalog.Limit)
				return page.loadModelsCmd(false), true
			}
		}
	}
	return nil, false
}

func (page *LLMPage) handleCommand(command LLMCommand, id string) tea.Cmd {
	if id = strings.TrimSpace(id); id == "" {
		id = page.resourceID
	}
	switch command {
	case LLMRefresh:
		return page.refreshCmd()
	case LLMAdd:
		return func() tea.Msg { return NavigateMsg{Path: []string{"llm", "create"}} }
	case LLMEdit:
		return func() tea.Msg { return NavigateMsg{Path: []string{"llm", id, "edit"}} }
	case LLMModels:
		return func() tea.Msg { return NavigateMsg{Path: []string{"llm", id, "models"}} }
	case LLMModelQuery:
		return func() tea.Msg { return NavigateMsg{Path: []string{"llm", id, "models", "query"}} }
	case LLMCredential:
		return func() tea.Msg { return NavigateMsg{Path: []string{"llm", id, "credential"}} }
	case LLMUse:
		return func() tea.Msg {
			_, err := page.service.SelectProvider(page.ctx, id)
			return llmMutationMsg{notice: "Active provider changed", err: err}
		}
	case LLMProbe:
		return func() tea.Msg {
			result, err := page.service.Probe(page.ctx, id)
			notice := "Probe succeeded · " + string(result.Readiness)
			return llmMutationMsg{notice: notice, err: err}
		}
	case LLMCredentialClear, LLMRemove:
		page.overlay = llmOverlayConfirm
		page.confirmCommand, page.targetID = command, id
		label := "Clear"
		if command == LLMRemove {
			label = "Remove"
		}
		page.confirm = component.NewConfirmButtons(label, "Cancel", false)
		return nil
	case LLMModelRefresh:
		return page.loadModelsCmd(true)
	default:
		page.err = fmt.Errorf("unsupported LLM command: %s", command)
		return nil
	}
}

func (page *LLMPage) updateConfirm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		page.closeConfirm()
		return nil
	case "enter":
		if !page.confirm.AffirmativeSelected() {
			page.closeConfirm()
			return nil
		}
		command, id := page.confirmCommand, page.targetID
		page.closeConfirm()
		switch command {
		case LLMCredentialClear:
			return func() tea.Msg {
				err := page.service.ClearCredential(page.ctx, id)
				return llmMutationMsg{notice: "Credential cleared", err: err}
			}
		case LLMRemove:
			return func() tea.Msg {
				_, err := page.service.RemoveProviderResult(page.ctx, id)
				return llmMutationMsg{notice: "Provider removed", err: err, removed: id, navigate: []string{"llm"}}
			}
		}
	default:
		return page.confirm.Update(msg)
	}
	return nil
}

func (page *LLMPage) closeConfirm() {
	page.overlay = llmOverlayNone
	page.confirm = component.ConfirmButtons{}
	page.confirmCommand, page.targetID = "", ""
}

func (page *LLMPage) confirmTitle() string {
	if page.confirmCommand == LLMRemove {
		return "Remove LLM provider?"
	}
	return "Clear provider credential?"
}

func (page *LLMPage) confirmDescription() string {
	if page.confirmCommand == LLMRemove {
		return "This removes the custom provider and its stored credential. Core providers cannot be removed."
	}
	return "This removes the managed credential. The raw secret is never displayed by the TUI."
}

func (page *LLMPage) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		status, err := page.service.Status(page.ctx)
		if err != nil {
			return llmMutationMsg{err: err}
		}
		_ = status
		return llmMutationMsg{notice: "LLM state refreshed"}
	}
}

func (page *LLMPage) finishMutation(msg llmMutationMsg) tea.Cmd {
	if page.editor != nil {
		page.editor.SetSubmitting(false)
	}
	if msg.err != nil {
		page.err = msg.err
		if page.editor != nil {
			page.editor.SetFeedback("", msg.err)
		}
		return nil
	}
	page.err = nil
	page.notice = msg.notice
	if msg.removed != "" {
		page.resourceID, page.section, page.action = "", "", ""
	}
	if err := page.reload(); err != nil {
		page.err = err
	}
	if len(msg.navigate) > 0 {
		page.editor, page.providerForm, page.credentialForm, page.modelForm, page.queryForm = nil, nil, nil, nil, nil
		return func() tea.Msg { return NavigateMsg{Path: msg.navigate, Replace: true} }
	}
	return nil
}

func (page *LLMPage) loadModelsCmd(refresh bool) tea.Cmd {
	id := page.resourceID
	query := page.query
	if query.Limit <= 0 {
		query.Limit = 25
	}
	query.Refresh = refresh
	return func() tea.Msg {
		result, err := page.service.ModelCatalog(page.ctx, id, query)
		return llmModelsMsg{page: result, err: err}
	}
}

func (page *LLMPage) finishModels(msg llmModelsMsg) tea.Cmd {
	if msg.err != nil {
		page.err = msg.err
		return nil
	}
	page.err = nil
	page.catalog = msg.page
	page.ensureModelsBrowser()
	selected := ""
	help := page.browser.HelpExpanded()
	if row, ok := page.browser.Selected(); ok {
		selected = row.ID
	}
	if selected == "" {
		selected = page.restoreSelected
	}
	rows := make([]component.Row, 0, len(msg.page.Models))
	for _, model := range msg.page.Models {
		description := model.Name
		if description == "" {
			description = model.ID
		}
		metaParts := make([]string, 0, 4)
		if model.Author != "" {
			metaParts = append(metaParts, model.Author)
		}
		if model.ContextLengthKnown {
			metaParts = append(metaParts, fmt.Sprintf("ctx %d", model.ContextLength))
		}
		if model.FreeKnown {
			if model.Free {
				metaParts = append(metaParts, "free")
			} else {
				metaParts = append(metaParts, "paid")
			}
		}
		if model.Rank != nil {
			metaParts = append(metaParts, fmt.Sprintf("%s #%d", model.Rank.Kind, model.Rank.Position))
		}
		searchParts := []string{model.ID, model.Name, model.Author, strings.Join(model.Capabilities, " "), strings.Join(model.SupportedParameters, " "), strings.Join(model.InputModalities, " "), strings.Join(model.OutputModalities, " ")}
		if model.Ollama != nil {
			searchParts = append(searchParts, model.Ollama.Family, strings.Join(model.Ollama.Families, " "), model.Ollama.Format, model.Ollama.QuantizationLevel, model.Ollama.ParameterSize)
		}
		rows = append(rows, component.Row{ID: model.ID, Title: description, Description: model.ID, Meta: strings.Join(metaParts, " · "), Search: strings.Join(searchParts, " ")})
	}
	page.browser.ReplaceRows(rows, selected)
	if selected != "" {
		page.browser.SelectID(selected)
	}
	page.restoreSelected = ""
	page.browser.SetHelpExpanded(help)
	page.notice = fmt.Sprintf("%d matched · %d returned · offset %d", msg.page.Matched, msg.page.Returned, msg.page.Offset)
	page.resize()
	return nil
}

func (page *LLMPage) setModelCmd(modelID string) tea.Cmd {
	id := page.resourceID
	modelID = strings.TrimSpace(modelID)
	return func() tea.Msg {
		_, err := page.service.SetProviderModel(page.ctx, id, modelID)
		return llmMutationMsg{notice: "Model set to " + modelID, err: err}
	}
}

func (page *LLMPage) resize() {
	if page == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	if page.editor != nil {
		page.editor.Resize(page.width, page.height)
		return
	}
	if page.resourceID != "" && page.section != "models" {
		page.detail.Resize(page.width, page.height)
		return
	}
	updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: page.height})
	page.browser = updated.(component.Browser)
}

func credentialLabel(provider application.LLMProviderResult) string {
	if !provider.Credential.Configured {
		return "Not configured"
	}
	if provider.Credential.Preview != "" {
		return provider.Credential.Preview
	}
	return "Configured"
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}
