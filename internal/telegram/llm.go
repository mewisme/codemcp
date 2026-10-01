package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

const (
	telegramLLMPageSize = 6

	inputLLMProviderAdd       = "llm.provider.add"
	inputLLMProviderConfigure = "llm.provider.configure"
	inputLLMModelSearch       = "llm.model.search"
	inputLLMModelSet          = "llm.model.set"
	inputLLMCredentialSet     = "llm.credential.set"
)

func (ui *Interface) handleLLM(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen, err := ui.llmScreen(ctx, owner, ActionState{Route: RouteLLM})
	if err != nil {
		screen = ErrorScreen(err)
	}
	_ = ui.runtime.SendScreen(ctx, owner.ChatID, screen)
}

func (ui *Interface) llmScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	value, err := ui.dispatch(ctx, capability.LLMStatus, nil)
	if err != nil {
		return Screen{}, err
	}
	status, ok := value.(application.LLMStatusResult)
	if !ok {
		return Screen{}, errors.New("LLM status returned an unexpected result")
	}
	value, err = ui.dispatch(ctx, capability.LLMProviderList, nil)
	if err != nil {
		return Screen{}, err
	}
	providers, ok := value.([]application.LLMProviderResult)
	if !ok {
		return Screen{}, errors.New("LLM provider list returned an unexpected result")
	}
	start, end, page, pages := PageBounds(len(providers), state.Page, telegramLLMPageSize)
	items := make([]string, 0, end-start)
	buttons := make([]Button, 0, end-start)
	for _, provider := range providers[start:end] {
		marker := ""
		if provider.Selected {
			marker = " · active"
		}
		items = append(items, fmt.Sprintf("%s — %s · %s%s", provider.Name, provider.Model, provider.Readiness, marker))
		button, buttonErr := ui.stateButton(owner, provider.Name, CallbackOpen, ActionState{
			Route: RouteLLMProvider, Back: RouteLLM, ResourceID: string(provider.ID),
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		buttons = append(buttons, button)
	}
	add, err := ui.stateButton(owner, "Add provider", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteLLM, Operation: capability.LLMProviderAdd, InputKind: inputLLMProviderAdd,
	})
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.stateButton(owner, "Refresh", CallbackRefresh, ActionState{Route: RouteLLM, Page: page})
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	nav := llmPageButtons(ui, owner, RouteLLM, "", nil, page, pages)
	nav = append(nav, home)
	keyboard := ResourceRows(buttons...)
	keyboard = append(keyboard, BoundedActionGroups(ActionGroups{Secondary: []Button{add, refresh}, Navigation: nav})...)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "LLM", Text: "Provider administration"},
		RichBlock{Kind: RichTable, Rows: [][]string{
			{"Active provider", string(status.ActiveProvider)},
			{"Model", status.Active.Model},
			{"Readiness", string(status.Active.Readiness)},
		}},
		RichBlock{Kind: RichList, Title: PaginationLabel(page, pages), Items: items},
	), Keyboard: keyboard}, nil
}

func (ui *Interface) llmProviderScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	id := strings.TrimSpace(state.ResourceID)
	value, err := ui.dispatch(ctx, capability.LLMProviderGet, application.LLMProviderIDInput{ID: id})
	if err != nil {
		return Screen{}, err
	}
	provider, ok := value.(application.LLMProviderResult)
	if !ok {
		return Screen{}, errors.New("LLM provider view returned an unexpected result")
	}
	rows := [][]string{
		{"ID", string(provider.ID)},
		{"Protocol", string(provider.Protocol)},
		{"Model", provider.Model},
		{"Base URL", provider.BaseURL},
		{"Auth", string(provider.AuthMode)},
		{"Discovery", string(provider.Discovery)},
		{"Core", fmt.Sprint(provider.Core)},
		{"Active", fmt.Sprint(provider.Selected)},
		{"Configured", fmt.Sprint(provider.Configured)},
		{"Readiness", string(provider.Readiness)},
		{"API key", provider.Credential.Preview},
	}
	if provider.Reason != "" {
		rows = append(rows, []string{"Reason", provider.Reason})
	}
	models, err := ui.stateButton(owner, "Models", CallbackOpen, ActionState{Route: RouteLLMModels, Back: RouteLLMProvider, ResourceID: id})
	if err != nil {
		return Screen{}, err
	}
	probe, err := ui.stateButton(owner, "Probe", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderProbe,
		Input: application.LLMProviderIDInput{ID: id},
	})
	if err != nil {
		return Screen{}, err
	}
	setModel, err := ui.stateButton(owner, "Set model", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderConfigure, InputKind: inputLLMModelSet,
	})
	if err != nil {
		return Screen{}, err
	}
	setKey, err := ui.stateButton(owner, "Set API key", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderCredentialSet,
		InputKind: inputLLMCredentialSet, SecretInput: true,
	})
	if err != nil {
		return Screen{}, err
	}
	secondary := []Button{models, probe, setModel, setKey}
	if !provider.Selected {
		use, useErr := ui.stateButton(owner, "Use", CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderSelect,
			Input: application.LLMProviderIDInput{ID: id},
		})
		if useErr != nil {
			return Screen{}, useErr
		}
		secondary = append([]Button{use}, secondary...)
	}
	if string(provider.CoreKind) == "ollama" {
		autoModel := application.LLMOllamaAutoModel
		auto, autoErr := ui.stateButton(owner, "Auto model", CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderConfigure,
			Input: application.LLMProviderWriteInput{ID: id, Model: &autoModel},
		})
		if autoErr != nil {
			return Screen{}, autoErr
		}
		secondary = append(secondary, auto)
		for _, item := range []struct {
			label string
			mode  string
		}{{"Cloud mode", "cloud"}, {"Local mode", "local"}} {
			mode := item.mode
			button, buttonErr := ui.stateButton(owner, item.label, CallbackOpen, ActionState{
				Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderConfigure,
				Input: application.LLMProviderWriteInput{ID: id, OllamaMode: &mode},
			})
			if buttonErr != nil {
				return Screen{}, buttonErr
			}
			secondary = append(secondary, button)
		}
	}
	destructive := []Button{}
	if provider.Credential.Configured {
		clear, clearErr := ui.stateButton(owner, "Clear API key", CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderCredentialClear,
			Input: application.LLMProviderIDInput{ID: id}, ForceConfirm: true,
		})
		if clearErr != nil {
			return Screen{}, clearErr
		}
		destructive = append(destructive, clear)
	}
	if !provider.Core {
		configure, configureErr := ui.stateButton(owner, "Configure", CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteLLMProvider, ResourceID: id, Operation: capability.LLMProviderConfigure, InputKind: inputLLMProviderConfigure,
		})
		if configureErr != nil {
			return Screen{}, configureErr
		}
		secondary = append(secondary, configure)
		remove, removeErr := ui.stateButton(owner, "Remove", CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteLLM, ResourceID: id, Operation: capability.LLMProviderRemove,
			Input: application.LLMProviderIDInput{ID: id}, ForceConfirm: true,
		})
		if removeErr != nil {
			return Screen{}, removeErr
		}
		destructive = append(destructive, remove)
	}
	back, err := ui.stateButton(owner, "Back", CallbackBack, ActionState{Route: RouteLLM})
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	keyboard := boundedLLMActionRows(append(secondary, destructive...)...)
	keyboard = append(keyboard, BoundedActionGroups(ActionGroups{Navigation: []Button{back, home}})...)
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: provider.Name, Text: "LLM provider"},
		RichBlock{Kind: RichTable, Rows: rows},
	), Keyboard: keyboard}, nil
}

func (ui *Interface) llmModelsScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	query := llmQueryFromState(state)
	if state.ResourceID == application.LLMOllamaProviderID {
		query.CheckAccess = true
	}
	query.Offset = max(0, state.Page) * telegramLLMPageSize
	query.Limit = telegramLLMPageSize
	value, err := ui.dispatch(ctx, capability.LLMProviderModels, application.LLMProviderModelsInput{ID: state.ResourceID, Query: query})
	if err != nil {
		return Screen{}, err
	}
	page, ok := value.(application.LLMModelPage)
	if !ok {
		return Screen{}, errors.New("LLM model catalog returned an unexpected result")
	}
	return ui.llmModelPageScreen(owner, state, page)
}

func (ui *Interface) llmModelPageScreen(owner ViewOwner, state ActionState, page application.LLMModelPage) (Screen, error) {
	query := llmQueryFromState(state)
	query.Refresh = false
	if string(page.ProviderID) == application.LLMOllamaProviderID {
		query.CheckAccess = true
	}
	currentPage := max(0, page.Offset/telegramLLMPageSize)
	pages := max(1, (page.Matched+telegramLLMPageSize-1)/telegramLLMPageSize)
	items := make([]string, 0, len(page.Models))
	buttons := make([]Button, 0, len(page.Models))
	for _, model := range page.Models {
		detail := model.Name
		if detail == "" {
			detail = model.ID
		}
		if model.ContextLengthKnown {
			detail += fmt.Sprintf(" · ctx %d", model.ContextLength)
		}
		if model.FreeKnown && model.Free {
			detail += " · free"
		}
		access, accessKnown := page.ModelAccess[model.ID]
		if accessKnown {
			detail += " · " + string(access.State)
			if access.State == application.LLMModelAccessUnavailable && access.Reason != "" {
				detail += " (" + compactPresentationValue(access.Reason) + ")"
			}
		}
		items = append(items, detail)
		if accessKnown && access.State == application.LLMModelAccessUnavailable {
			continue
		}
		modelID := model.ID
		button, err := ui.stateButton(owner, modelID, CallbackOpen, ActionState{
			Route: RouteOperation, Back: RouteLLMModels, ResourceID: string(page.ProviderID), Operation: capability.LLMProviderConfigure,
			Input: application.LLMProviderWriteInput{ID: string(page.ProviderID), Model: &modelID},
		})
		if err != nil {
			return Screen{}, err
		}
		buttons = append(buttons, button)
	}
	search, err := ui.stateButton(owner, "Search", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteLLMModels, ResourceID: string(page.ProviderID), Operation: capability.LLMProviderModels,
		InputKind: inputLLMModelSearch, Input: application.LLMProviderModelsInput{ID: string(page.ProviderID), Query: query},
	})
	if err != nil {
		return Screen{}, err
	}
	filters := []Button{search}
	if string(page.ProviderID) == application.LLMOllamaProviderID {
		retest := query
		retest.CheckAccess = true
		retest.Refresh = true
		retest.Offset = 0
		button, buttonErr := ui.stateButton(owner, "Retest", CallbackOpen, ActionState{
			Route: RouteLLMModels, Back: RouteLLMProvider, ResourceID: string(page.ProviderID), Input: retest,
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		filters = append(filters, button)
	}
	if containsTelegramString(page.QueryCapabilities.Filters, "free") {
		for _, item := range []struct {
			label string
			free  *bool
		}{{"Free", boolPointerTelegram(true)}, {"Paid", boolPointerTelegram(false)}, {"All", nil}} {
			next := query
			next.Free = item.free
			button, buttonErr := ui.stateButton(owner, item.label, CallbackOpen, ActionState{
				Route: RouteLLMModels, Back: RouteLLMProvider, ResourceID: string(page.ProviderID), Input: next,
			})
			if buttonErr != nil {
				return Screen{}, buttonErr
			}
			filters = append(filters, button)
		}
	}
	sorts := []Button{}
	for _, item := range []struct {
		field string
		dir   string
	}{{"id", "asc"}, {"context", "desc"}} {
		if !containsTelegramString(page.QueryCapabilities.Sorts, item.field) {
			continue
		}
		next := query
		next.Sort = []application.LLMModelSort{{Field: item.field, Direction: item.dir}}
		next.Rank, next.RecommendFor = "", ""
		button, buttonErr := ui.stateButton(owner, "Sort "+item.field, CallbackOpen, ActionState{
			Route: RouteLLMModels, Back: RouteLLMProvider, ResourceID: string(page.ProviderID), Input: next,
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		sorts = append(sorts, button)
	}
	back, err := ui.stateButton(owner, "Back", CallbackBack, ActionState{Route: RouteLLMProvider, ResourceID: string(page.ProviderID)})
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	nav := llmPageButtons(ui, owner, RouteLLMModels, string(page.ProviderID), query, currentPage, pages)
	nav = append(nav, back, home)
	keyboard := ResourceRows(buttons...)
	keyboard = append(keyboard, BoundedActionGroups(ActionGroups{
		Secondary: append(filters, sorts...), Navigation: nav,
	})...)
	meta := [][]string{
		{"Matched", fmt.Sprint(page.Matched)},
		{"Catalog", fmt.Sprint(page.TotalCatalog)},
		{"Returned", fmt.Sprint(page.Returned)},
	}
	if page.ModelAccess != nil {
		meta = append(meta,
			[]string{"Available", fmt.Sprint(page.AccessAvailable)},
			[]string{"Unavailable", fmt.Sprint(page.AccessUnavailable)},
			[]string{"Unknown", fmt.Sprint(page.AccessUnknown)},
		)
		if page.AccessError != "" {
			meta = append(meta, []string{"Access check", page.AccessError})
		}
	}
	if page.RankBasis != "" {
		meta = append(meta, []string{"Rank basis", page.RankBasis})
	}
	if page.RecommendationBasis != "" {
		meta = append(meta, []string{"Recommendation", page.RecommendationBasis})
	}
	return Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: "Models", Text: string(page.ProviderID)},
		RichBlock{Kind: RichTable, Rows: meta},
		RichBlock{Kind: RichList, Title: PaginationLabel(currentPage, pages), Items: items},
	), Keyboard: keyboard}, nil
}

func llmInputPrompt(kind string) (title, prompt, placeholder string) {
	switch kind {
	case inputLLMProviderAdd:
		return "Add LLM provider", "Reply with: id | name | protocol | base URL | model | auth mode | discovery", "acme | Acme | openai | https://... | model | bearer | openai-models"
	case inputLLMProviderConfigure:
		return "Configure LLM provider", "Reply with: name | protocol | base URL | model | auth mode | discovery", "Acme | openai | https://... | model | bearer | openai-models"
	case inputLLMModelSearch:
		return "Search LLM models", "Reply with a model search term.", "model name or ID"
	case inputLLMModelSet:
		return "Set LLM model", "Reply with the exact model ID. This does not change active provider.", "provider/model"
	case inputLLMCredentialSet:
		return "Set LLM API key", "Reply with the API key. The secret-bearing message is deleted after capture.", "API key"
	default:
		return "", "", ""
	}
}

func llmActionInput(state ActionState, text string) (any, bool, error) {
	text = strings.TrimSpace(text)
	switch state.InputKind {
	case inputLLMProviderAdd:
		parts, err := llmInputParts(text, 7)
		if err != nil {
			return nil, true, err
		}
		return application.LLMProviderWriteInput{
			ID:     parts[0],
			Config: application.NewCustomLLMProviderConfig(parts[1], parts[2], parts[3], parts[4], parts[5], parts[6]),
		}, true, nil
	case inputLLMProviderConfigure:
		parts, err := llmInputParts(text, 6)
		if err != nil {
			return nil, true, err
		}
		return application.LLMProviderWriteInput{
			ID:     state.ResourceID,
			Config: application.NewCustomLLMProviderConfig(parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]),
		}, true, nil
	case inputLLMModelSearch:
		input, _ := state.Input.(application.LLMProviderModelsInput)
		input.ID = state.ResourceID
		input.Query.Search = text
		input.Query.Offset = 0
		input.Query.Limit = telegramLLMPageSize
		return input, true, nil
	case inputLLMModelSet:
		if text == "" {
			return nil, true, errors.New("LLM model ID is required")
		}
		return application.LLMProviderWriteInput{ID: state.ResourceID, Model: &text}, true, nil
	case inputLLMCredentialSet:
		if text == "" {
			return nil, true, errors.New("LLM API key is required")
		}
		return application.LLMProviderCredentialInput{ID: state.ResourceID, APIKey: text}, true, nil
	default:
		return nil, false, nil
	}
}

func llmInputParts(text string, want int) ([]string, error) {
	parts := strings.Split(text, "|")
	if len(parts) != want {
		return nil, fmt.Errorf("expected %d pipe-separated fields", want)
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return nil, fmt.Errorf("field %d must not be empty", index+1)
		}
	}
	return parts, nil
}

func llmQueryFromState(state ActionState) application.LLMModelQuery {
	switch value := state.Input.(type) {
	case application.LLMModelQuery:
		return value
	case application.LLMProviderModelsInput:
		return value.Query
	default:
		return application.LLMModelQuery{}
	}
}

func llmPageButtons(ui *Interface, owner ViewOwner, route Route, resourceID string, input any, page, pages int) []Button {
	buttons := []Button{}
	if page > 0 {
		if button, err := ui.stateButton(owner, "Previous", CallbackOpen, ActionState{
			Route: route, ResourceID: resourceID, Page: page - 1, Input: input,
		}); err == nil {
			buttons = append(buttons, button)
		}
	}
	if page+1 < pages {
		if button, err := ui.stateButton(owner, "Next", CallbackOpen, ActionState{
			Route: route, ResourceID: resourceID, Page: page + 1, Input: input,
		}); err == nil {
			buttons = append(buttons, button)
		}
	}
	return buttons
}

func containsTelegramString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func boolPointerTelegram(value bool) *bool { return &value }

func boundedLLMActionRows(buttons ...Button) [][]Button {
	rows := make([][]Button, 0, (len(buttons)+maxActionButtonsPerRow-1)/maxActionButtonsPerRow)
	for len(buttons) > 0 {
		count := min(maxActionButtonsPerRow, len(buttons))
		row := append([]Button(nil), buttons[:count]...)
		for index := range row {
			row[index].Text = CompactActionLabel(row[index].Text)
		}
		rows = append(rows, row)
		buttons = buttons[count:]
	}
	return rows
}
