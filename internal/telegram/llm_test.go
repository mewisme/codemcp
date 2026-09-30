package telegram

import (
	"fmt"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/llm"
)

func TestTelegramLLMNavigationUsesCanonicalStatusAndProviderList(t *testing.T) {
	openrouter := application.LLMProviderResult{
		ID: llm.OpenRouterID, Name: "OpenRouter", Model: "openrouter/free", Selected: true, Core: true,
		CoreKind: llm.CoreOpenRouter, Readiness: llm.ReadinessReady,
	}
	ollama := application.LLMProviderResult{
		ID: llm.OllamaID, Name: "Ollama", Model: "gpt-oss", Core: true, CoreKind: llm.CoreOllama,
		Readiness: llm.ReadinessUnavailable, Reason: "provider unavailable",
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.LLMStatus:       application.LLMStatusResult{ActiveProvider: llm.OpenRouterID, Active: openrouter},
		capability.LLMProviderList: []application.LLMProviderResult{openrouter, ollama},
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
	for _, want := range []string{"OpenRouter", "openrouter/free", "Ollama", "provider administration"} {
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
		ID: llm.OpenRouterID, Name: "OpenRouter", Protocol: llm.ProtocolOpenAI,
		BaseURL: "https://openrouter.ai/api/v1", Model: "openrouter/free", AuthMode: llm.AuthBearer,
		Discovery: llm.DiscoveryOpenAIModels, CoreKind: llm.CoreOpenRouter, Core: true,
		Readiness: llm.ReadinessReady, Credential: application.LLMCredentialResult{
			ProviderID: llm.OpenRouterID, Configured: true, Preview: "sk-t********alue",
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
	input, handled, err := llmActionInput(state, rawKey)
	if err != nil || !handled {
		t.Fatalf("protected LLM input handled=%t err=%v", handled, err)
	}
	credential := input.(application.LLMProviderCredentialInput)
	if credential.ID != string(provider.ID) || credential.APIKey != rawKey {
		t.Fatalf("credential input=%#v", credential)
	}
}

func TestTelegramLLMModelsUseBoundedCanonicalQueryWithoutSelectingProvider(t *testing.T) {
	page := application.LLMModelPage{
		ProviderID: llm.OpenRouterID, TotalCatalog: 20, Matched: 12, Offset: 6, Limit: telegramLLMPageSize, Returned: 2, HasMore: true,
		QueryCapabilities: application.LLMModelQueryCapabilities{Filters: []string{"free"}, Sorts: []string{"id", "context"}},
		Models: []llm.Model{
			{ID: "model/a", Name: "Model A", ContextLength: 128000, ContextLengthKnown: true, Free: true, FreeKnown: true},
			{ID: "model/b", Name: "Model B"},
		},
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.LLMProviderModels: page}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.llmModelsScreen(t.Context(), owner, ActionState{
		Route: RouteLLMModels, ResourceID: string(llm.OpenRouterID), Page: 1,
		Input: application.LLMModelQuery{Search: "model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.LLMProviderModels {
		t.Fatalf("model calls=%#v", dispatcher.calls)
	}
	input := dispatcher.calls[0].Input.(application.LLMProviderModelsInput)
	if input.ID != string(llm.OpenRouterID) || input.Query.Offset != telegramLLMPageSize || input.Query.Limit != telegramLLMPageSize || input.Query.Search != "model" {
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
			if write.Model == nil || *write.Model != "model/a" || write.ID != string(llm.OpenRouterID) {
				t.Fatalf("model selection input=%#v", write)
			}
		}
	}
	if !foundModel {
		t.Fatal("model selection action missing")
	}
}
