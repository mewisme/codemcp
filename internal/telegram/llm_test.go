package telegram

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/llm"
)

func TestTelegramLLMNavigationUsesCanonicalStatusAndProviderList(t *testing.T) {
	ollama := application.LLMProviderResult{
		ID: llm.OllamaID, Name: "Ollama", Model: "qwen3:8b", Selected: true, Core: true,
		CoreKind: llm.CoreOllama, Readiness: llm.ReadinessReady,
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.LLMStatus:       application.LLMStatusResult{ActiveProvider: llm.OllamaID, Active: ollama},
		capability.LLMProviderList: []application.LLMProviderResult{ollama},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.llmScreen(t.Context(), owner, ActionState{Route: RouteLLM})
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 2 || dispatcher.calls[0].Operation != capability.LLMStatus || dispatcher.calls[1].Operation != capability.LLMProviderList {
		t.Fatalf("LLM canonical reads=%#v", dispatcher.calls)
	}
	fallback := RichFallback(screen.Rich).Text
	for _, want := range []string{"Ollama", "qwen3:8b", "active inference configuration"} {
		if !strings.Contains(strings.ToLower(fallback), strings.ToLower(want)) {
			t.Fatalf("LLM screen missing %q: %q", want, fallback)
		}
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		t.Fatal(err)
	}
	foundCommand := false
	for _, command := range Commands() {
		if command.Name == "llm" && command.Route == RouteLLM {
			foundCommand = true
		}
	}
	if !foundCommand {
		t.Fatal("LLM command is not discoverable")
	}
}

func TestTelegramLLMProviderActionsProtectCoreIdentityAndSecretState(t *testing.T) {
	const rawKey = "sk-telegram-secret-value"
	provider := application.LLMProviderResult{
		ID: llm.OllamaID, Name: "Ollama", Protocol: llm.ProtocolOpenAI,
		BaseURL: "https://ollama.ai/api/v1", Model: "qwen3:8b", AuthMode: llm.AuthBearer,
		Discovery: llm.DiscoveryOpenAIModels, CoreKind: llm.CoreOllama, Core: true,
		Readiness: llm.ReadinessReady, Credential: application.LLMCredentialResult{
			ProviderID: llm.OllamaID, Configured: true, Preview: "sk-t********alue",
		},
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.LLMProviderGet: provider}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.llmProviderScreen(t.Context(), owner, ActionState{Route: RouteLLMProvider, ResourceID: string(provider.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		t.Fatal(err)
	}
	fallback := RichFallback(screen.Rich).Text
	if strings.Contains(fallback, rawKey) || !strings.Contains(fallback, provider.Credential.Preview) {
		t.Fatalf("credential presentation=%q", fallback)
	}
	labels := map[string]Button{}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			labels[button.Text] = button
		}
	}
	if _, ok := labels["Remove"]; ok {
		t.Fatal("core provider exposes remove")
	}
	if _, ok := labels["Configure"]; ok {
		t.Fatal("core provider exposes identity configure")
	}
	keyButton, ok := labels["Set API key"]
	if !ok {
		t.Fatal("set API key action missing")
	}
	ref, err := ui.callbacks.Decode(keyButton.CallbackData)
	if err != nil {
		t.Fatal(err)
	}
	value, err := ui.states.Get(ref.Token, owner)
	if err != nil {
		t.Fatal(err)
	}
	state := value.(ActionState)
	if !state.SecretInput || state.InputKind != inputLLMCredentialSet || state.ResourceID != string(provider.ID) {
		t.Fatalf("credential callback state=%#v", state)
	}
	if strings.Contains(fmt.Sprintf("%#v", state), rawKey) || strings.Contains(keyButton.CallbackData, rawKey) {
		t.Fatal("raw LLM key leaked into callback state")
	}
	if _, err := ui.states.Get(ref.Token, ViewOwner{ChatID: owner.ChatID + 1, UserID: owner.UserID + 1, Generation: owner.Generation}); err == nil {
		t.Fatal("foreign owner can read LLM callback state")
	}
	if _, err := ui.states.Get(ref.Token, ViewOwner{ChatID: owner.ChatID, UserID: owner.UserID, Generation: owner.Generation + 1}); err == nil {
		t.Fatal("stale generation can read LLM callback state")
	}
	descriptor, handled, err := ui.llmInputFlow(t.Context(), state)
	if err != nil || !handled {
		t.Fatalf("protected LLM flow handled=%t err=%v", handled, err)
	}
	if len(descriptor.Fields) != 1 || !descriptor.Fields[0].Secret || descriptor.Fields[0].Kind != inputFlowSecret {
		t.Fatalf("credential flow fields=%#v", descriptor.Fields)
	}
	input, err := descriptor.Build(inputFlowData{values: map[string]string{"api_key": rawKey}, set: map[string]bool{"api_key": true}})
	if err != nil {
		t.Fatal(err)
	}
	credential := input.(application.LLMProviderCredentialInput)
	if credential.ID != string(provider.ID) || credential.APIKey != rawKey {
		t.Fatalf("credential input=%#v", credential)
	}
}

func TestTelegramLLMModelsUseBoundedCanonicalQueryWithoutSelectingProvider(t *testing.T) {
	page := application.LLMModelPage{
		ProviderID: llm.OllamaID, TotalCatalog: 20, Matched: 12, Offset: 6, Limit: telegramLLMPageSize, Returned: 2, HasMore: true,
		QueryCapabilities: application.LLMModelQueryCapabilities{Filters: []string{"free"}, Sorts: []string{"id", "context"}},
		Models: []llm.Model{
			{ID: "model/a", Name: "Model A", ContextLength: 128000, ContextLengthKnown: true, Free: true, FreeKnown: true},
			{ID: "model/b", Name: "Model B"},
		},
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.LLMProviderModels: page}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.llmModelsScreen(t.Context(), owner, ActionState{
		Route: RouteLLMModels, ResourceID: string(llm.OllamaID), Page: 1,
		Input: application.LLMModelQuery{Search: "model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.LLMProviderModels {
		t.Fatalf("model calls=%#v", dispatcher.calls)
	}
	input := dispatcher.calls[0].Input.(application.LLMProviderModelsInput)
	if input.ID != string(llm.OllamaID) || input.Query.Offset != telegramLLMPageSize || input.Query.Limit != telegramLLMPageSize || input.Query.Search != "model" || !input.Query.CheckAccess {
		t.Fatalf("model query=%#v", input)
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		t.Fatal(err)
	}
	foundModel := false
	for _, row := range screen.Keyboard {
		for _, button := range row {
			if button.Text != "model/a" {
				continue
			}
			foundModel = true
			ref, decodeErr := ui.callbacks.Decode(button.CallbackData)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			value, stateErr := ui.states.Get(ref.Token, owner)
			if stateErr != nil {
				t.Fatal(stateErr)
			}
			state := value.(ActionState)
			if state.Operation != capability.LLMProviderConfigure || state.Operation == capability.LLMProviderSelect {
				t.Fatalf("model selection state=%#v", state)
			}
			write := state.Input.(application.LLMProviderWriteInput)
			if write.Model == nil || *write.Model != "model/a" || write.ID != string(llm.OllamaID) {
				t.Fatalf("model selection input=%#v", write)
			}
		}
	}
	if !foundModel {
		t.Fatal("model selection action missing")
	}
}

func TestTelegramOllamaModelsExposeAccessResultsAndDisableUnavailableModels(t *testing.T) {
	page := application.LLMModelPage{
		ProviderID:        llm.OllamaID,
		TotalCatalog:      3,
		Matched:           3,
		Limit:             telegramLLMPageSize,
		Returned:          3,
		AccessChecked:     true,
		AccessAvailable:   1,
		AccessUnavailable: 1,
		AccessUnknown:     1,
		ModelAccess: map[string]application.LLMModelAccessResult{
			"paid-model": {State: application.LLMModelAccessUnavailable, ErrorCategory: llm.ErrorProvider, Reason: "provider returned HTTP 402"},
			"free-model": {State: application.LLMModelAccessAvailable},
			"slow-model": {State: application.LLMModelAccessUnknown, ErrorCategory: llm.ErrorTimeout, Reason: "provider request timed out"},
		},
		QueryCapabilities: application.LLMModelQueryCapabilities{Sorts: []string{"id"}},
		Models:            []llm.Model{{ID: "paid-model"}, {ID: "free-model"}, {ID: "slow-model"}},
	}
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	screen, err := ui.llmModelPageScreen(owner, ActionState{Route: RouteLLMModels, ResourceID: string(llm.OllamaID)}, page)
	if err != nil {
		t.Fatal(err)
	}
	fallback := strings.ToLower(RichFallback(screen.Rich).Text)
	for _, want := range []string{"available", "unavailable", "unknown", "http 402"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("model access presentation missing %q: %q", want, fallback)
		}
	}
	labels := keyboardLabels(screen.Keyboard)
	if strings.Contains(labels, "paid-model") {
		t.Fatalf("unavailable model remains selectable: %s", labels)
	}
	for _, want := range []string{"free-model", "slow-model", "Retest"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("model access action %q missing: %s", want, labels)
		}
	}
}

func TestTelegramLLMOperationErrorBackPreservesProviderResource(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	state := ActionState{
		Route:      RouteOperation,
		Back:       RouteLLMModels,
		ResourceID: string(llm.OllamaID),
		Operation:  capability.LLMProviderConfigure,
		Input:      "operation payload must not be copied into navigation state",
	}
	screen, err := ui.operationErrorScreen(owner, state, errors.New("provider returned HTTP 402"))
	if err != nil {
		t.Fatal(err)
	}
	var back Button
	for _, row := range screen.Keyboard {
		for _, button := range row {
			if button.Text == "« Back" {
				back = button
			}
		}
	}
	if back.CallbackData == "" {
		t.Fatalf("operation error has no Back action: %#v", screen.Keyboard)
	}
	ref, err := ui.callbacks.Decode(back.CallbackData)
	if err != nil {
		t.Fatal(err)
	}
	value, err := ui.states.Get(ref.Token, owner)
	if err != nil {
		t.Fatal(err)
	}
	backState := value.(ActionState)
	if backState.Route != RouteLLMModels || backState.ResourceID != string(llm.OllamaID) || backState.Input != nil {
		t.Fatalf("operation Back lost provider context: %#v", backState)
	}
}
