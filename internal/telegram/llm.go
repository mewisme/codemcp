package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
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

var telegramLLMProviderIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)

func (ui *Interface) handleLLM(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteLLM, Back: RouteHome})
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
		meta := []string{provider.Model, displayState(string(provider.Readiness))}
		if provider.Selected {
			meta = append(meta, "Active")
		}
		items = append(items, provider.Name+"\n"+strings.Join(meta, " · "))
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
		RichBlock{Kind: RichHeading, Title: "LLM", Text: "Model providers and active inference configuration"},
		StateBlock(statusTone(string(status.Active.Readiness)), displayState(string(status.Active.Readiness)), ""),
		FieldsBlock("Active provider",
			[]string{"Provider", string(status.ActiveProvider)},
			[]string{"Model", status.Active.Model},
		),
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
		{"Core provider", stateLabel(provider.Core, "Yes", "No")},
		{"Selection", stateLabel(provider.Selected, "Active", "Inactive")},
		{"Configuration", configuredLabel(provider.Configured)},
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
		StateBlock(statusTone(string(provider.Readiness)), displayState(string(provider.Readiness)), ""),
		RichBlock{Kind: RichFields, Title: "Configuration", Rows: rows},
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
		label := model.Name
		if label == "" {
			label = model.ID
		}
		meta := make([]string, 0, 4)
		if model.ContextLengthKnown {
			meta = append(meta, fmt.Sprintf("ctx %d", model.ContextLength))
		}
		if model.FreeKnown && model.Free {
			meta = append(meta, "Free")
		}
		access, accessKnown := page.ModelAccess[model.ID]
		if accessKnown {
			meta = append(meta, displayState(string(access.State)))
			if access.State == application.LLMModelAccessUnavailable && access.Reason != "" {
				meta = append(meta, compactPresentationValue(access.Reason))
			}
		}
		if len(meta) == 0 {
			items = append(items, label)
		} else {
			items = append(items, label+"\n"+strings.Join(meta, " · "))
		}
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
		RichBlock{Kind: RichFields, Title: "Catalog", Rows: meta},
		RichBlock{Kind: RichList, Title: PaginationLabel(currentPage, pages), Items: items},
	), Keyboard: keyboard}, nil
}

func llmInputPrompt(kind string) (title, prompt, placeholder string) {
	switch kind {
	case inputLLMModelSearch:
		return "Search LLM models", "Reply with a model search term.", "model name or ID"
	default:
		return "", "", ""
	}
}

func (ui *Interface) llmInputFlow(ctx context.Context, state ActionState) (inputFlowDescriptor, bool, error) {
	providerFields := func(current *application.LLMProviderResult, includeID bool) []inputFlowField {
		fields := []inputFlowField{}
		if includeID {
			fields = append(fields, inputFlowField{
				Key: "id", Label: "Provider ID", Description: "Stable identifier used by CodeMCP configuration and commands.",
				Kind: inputFlowText, Required: true, Placeholder: "acme", Example: "acme",
				Accepted: "1–64 lowercase letters, digits, '.', '_' or '-'; must start and end with a letter or digit.",
				Validate: validateTelegramLLMProviderID,
			})
		}
		name := ""
		protocol := "openai"
		baseURL := ""
		model := ""
		authMode := "none"
		discovery := "none"
		if current != nil {
			name, protocol, baseURL, model = current.Name, string(current.Protocol), current.BaseURL, current.Model
			authMode, discovery = string(current.AuthMode), string(current.Discovery)
		}
		fields = append(fields,
			inputFlowField{Key: "name", Label: "Name", Description: "Display name shown in CodeMCP.", Kind: inputFlowText, Placeholder: "Acme", Example: "Acme", Accepted: "Up to 128 bytes.", HasDefault: current != nil, Default: name, CanClear: current != nil, Validate: func(value string) error {
				if len(value) > 128 {
					return errors.New("provider name must be at most 128 bytes")
				}
				return nil
			}},
			inputFlowField{Key: "protocol", Label: "Protocol", Description: "Request format used when CodeMCP calls this provider.", Kind: inputFlowEnum, Required: true, HasDefault: true, Default: protocol, Options: []inputFlowOption{
				{Label: "OpenAI compatible", Value: "openai", Description: "OpenAI-compatible chat/completions APIs."},
				{Label: "Anthropic compatible", Value: "anthropic", Description: "Anthropic-compatible Messages API."},
			}},
			inputFlowField{Key: "base_url", Label: "Base URL", Description: "Provider API endpoint. Do not include credentials, query parameters, or fragments.", Kind: inputFlowText, Required: true, Placeholder: "https://api.example.com/v1", Example: "https://api.example.com/v1", HasDefault: current != nil, Default: baseURL, Validate: validateTelegramLLMEndpoint},
			inputFlowField{Key: "model", Label: "Model", Description: "Default model ID. Optional when the provider can operate without a fixed model.", Kind: inputFlowText, Placeholder: "provider/model", Example: "gpt-5", Accepted: "Up to 256 bytes.", HasDefault: current != nil, Default: model, CanClear: true, Validate: func(value string) error {
				if len(value) > 256 {
					return errors.New("model ID must be at most 256 bytes")
				}
				return nil
			}},
			inputFlowField{Key: "auth_mode", Label: "Authentication", Description: "How CodeMCP attaches the provider credential.", Kind: inputFlowEnum, Required: true, HasDefault: true, Default: authMode, Options: []inputFlowOption{
				{Label: "None", Value: "none", Description: "No credential header."},
				{Label: "Bearer token", Value: "bearer", Description: "Authorization: Bearer <token>."},
				{Label: "x-api-key", Value: "x-api-key", Description: "x-api-key request header."},
			}},
			inputFlowField{Key: "discovery", Label: "Model discovery", Description: "Endpoint used to discover model IDs.", Kind: inputFlowEnum, Required: true, HasDefault: true, Default: discovery, Options: []inputFlowOption{
				{Label: "None", Value: "none", Description: "Do not discover models automatically."},
				{Label: "OpenAI /models", Value: "openai-models", Description: "Use an OpenAI-compatible /models endpoint."},
				{Label: "Ollama /api/tags", Value: "ollama-tags", Description: "Use Ollama native model tags."},
			}},
		)
		return fields
	}
	buildProvider := func(id string, data inputFlowData) (any, error) {
		config := application.NewCustomLLMProviderConfig(
			data.Value("name"), data.Value("protocol"), data.Value("base_url"), data.Value("model"), data.Value("auth_mode"), data.Value("discovery"),
		)
		return application.LLMProviderWriteInput{ID: id, Config: config}, nil
	}

	switch state.InputKind {
	case inputLLMProviderAdd:
		descriptor := inputFlowDescriptor{
			Title: "Add LLM provider", Description: "Configure a custom OpenAI-compatible or Anthropic-compatible provider.", SubmitLabel: "Create provider",
			Fields: providerFields(nil, true),
		}
		descriptor.Build = func(data inputFlowData) (any, error) { return buildProvider(data.Value("id"), data) }
		return descriptor, true, nil
	case inputLLMProviderConfigure:
		value, err := ui.dispatch(ctx, capability.LLMProviderGet, application.LLMProviderIDInput{ID: state.ResourceID})
		if err != nil {
			return inputFlowDescriptor{}, true, err
		}
		provider, ok := value.(application.LLMProviderResult)
		if !ok {
			return inputFlowDescriptor{}, true, errors.New("LLM provider view returned an unexpected result")
		}
		descriptor := inputFlowDescriptor{
			Title: "Configure LLM provider", Description: provider.Name + " · custom provider configuration", SubmitLabel: "Save changes",
			Fields: providerFields(&provider, false),
		}
		descriptor.Build = func(data inputFlowData) (any, error) { return buildProvider(state.ResourceID, data) }
		return descriptor, true, nil
	case inputLLMModelSet:
		return inputFlowDescriptor{
			Title: "Set LLM model", Description: "Choose the exact default model ID for this provider.", SubmitLabel: "Set model",
			Fields: []inputFlowField{{Key: "model", Label: "Model ID", Description: "Exact provider model ID. This does not change the active provider.", Kind: inputFlowText, Required: true, Placeholder: "provider/model", Example: "qwen3:8b"}},
			Build: func(data inputFlowData) (any, error) {
				model := data.Value("model")
				return application.LLMProviderWriteInput{ID: state.ResourceID, Model: &model}, nil
			},
		}, true, nil
	case inputLLMCredentialSet:
		return inputFlowDescriptor{
			Title: "Set LLM API key", Description: "The reply is protected and deleted after capture.", SubmitLabel: "Save API key",
			Fields: []inputFlowField{{Key: "api_key", Label: "API key", Description: "Credential sent using the provider's configured authentication mode.", Kind: inputFlowSecret, Required: true, Secret: true, Placeholder: "API key"}},
			Build: func(data inputFlowData) (any, error) {
				return application.LLMProviderCredentialInput{ID: state.ResourceID, APIKey: data.Value("api_key")}, nil
			},
		}, true, nil
	default:
		return inputFlowDescriptor{}, false, nil
	}
}

func validateTelegramLLMProviderID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || !telegramLLMProviderIDPattern.MatchString(value) {
		return errors.New("provider ID must use 1-64 lowercase letters, digits, '.', '_' or '-' and start/end with a letter or digit")
	}
	if value == "ollama" {
		return errors.New("provider ID \"ollama\" is reserved")
	}
	return nil
}

func validateTelegramLLMEndpoint(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 {
		return errors.New("base URL is required and must be at most 2048 bytes")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("base URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return errors.New("base URL must not contain credentials, query parameters, or fragments")
	}
	return nil
}

func llmActionInput(state ActionState, text string) (any, bool, error) {
	text = strings.TrimSpace(text)
	switch state.InputKind {
	case inputLLMModelSearch:
		input, _ := state.Input.(application.LLMProviderModelsInput)
		input.ID = state.ResourceID
		input.Query.Search = text
		input.Query.Offset = 0
		input.Query.Limit = telegramLLMPageSize
		return input, true, nil
	default:
		return nil, false, nil
	}
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
		if button, err := ui.stateButton(owner, previousNavigationLabel("Previous"), CallbackOpen, ActionState{
			Route: route, ResourceID: resourceID, Page: page - 1, Input: input,
		}); err == nil {
			buttons = append(buttons, button)
		}
	}
	if page+1 < pages {
		if button, err := ui.stateButton(owner, nextNavigationLabel("Next"), CallbackOpen, ActionState{
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
