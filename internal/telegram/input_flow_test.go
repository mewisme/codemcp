package telegram

import (
	"slices"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestMutationInputKindsUseStructuredFlows(t *testing.T) {
	provider := application.LLMProviderResult{
		ID: "acme", Name: "Acme", Protocol: llm.ProtocolOpenAI, BaseURL: "https://api.example.com/v1",
		Model: "model", AuthMode: llm.AuthBearer, Discovery: llm.DiscoveryOpenAIModels,
	}
	prompt := instructioncontext.ScopedPrompt{
		Scope: instructioncontext.PromptScopeWorkspace,
		Definition: instructioncontext.PromptDefinition{
			Version: instructioncontext.PromptDefinitionVersion,
			Name:    "review-code",
			Messages: []instructioncontext.PromptMessage{{
				Role: "user", Content: instructioncontext.PromptTextContent{Type: "text", Text: "Review this."},
			}},
		},
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.LLMProviderGet:     provider,
		capability.TunnelStatus:       application.TunnelView{Enabled: true},
		capability.TunnelGet:          tunnel.Metadata{ID: "tunnel_1", Name: "Managed"},
		capability.UpstreamServerShow: upstream.Server{ID: "remote", Name: "Remote", Transport: "http", Enabled: true, URL: "https://mcp.example.com", Auth: upstream.AuthConfig{Type: "none"}, Expose: "all", IdleTimeoutSec: 600},
		capability.ConfigGet:          application.SettingResult{Spec: config.FieldSpec{Key: "example.mode", Label: "Example mode", Description: "Select the example mode.", Kind: config.FieldEnum, Options: []string{"one", "two"}, Editable: true}, Value: "one"},
		capability.PromptGet:          prompt,
	}}
	ui, _ := newDomainTestInterface(t, dispatcher)
	states := []ActionState{
		{InputKind: inputWorkspaceRegister},
		{InputKind: inputWorkspaceRelocate, ResourceID: "ws_1"},
		{InputKind: inputWorkspaceAccessAdd, ResourceID: "ws_1"},
		{InputKind: inputWorkspaceContainerCreate},
		{InputKind: inputWorkspaceContainerRename, ResourceID: "container_1"},
		{InputKind: inputLLMProviderAdd},
		{InputKind: inputLLMProviderConfigure, ResourceID: "acme"},
		{InputKind: inputLLMModelSet, ResourceID: "acme"},
		{InputKind: inputLLMCredentialSet, ResourceID: "acme"},
		{InputKind: inputUpstreamAdd},
		{InputKind: inputUpstreamConfigure, ResourceID: "remote"},
		{InputKind: inputTunnelConfigure},
		{InputKind: inputTunnelAdminKey},
		{InputKind: inputTunnelAdminScope},
		{InputKind: inputUpstreamOAuthLogin, ResourceID: "remote"},
		{InputKind: inputManagedTunnelCreate},
		{InputKind: inputManagedTunnelUpdate, ResourceID: "tunnel_1"},
		{InputKind: inputSettingSet, ResourceID: "example.mode"},
		{InputKind: inputSettingsApply},
		{InputKind: inputConfigPatch},
		{InputKind: inputTelegramUserManual, ExpectedVersion: "42"},
		{InputKind: inputPromptCreate},
		{InputKind: inputPromptUpdate, ResourceID: "review-code", ExpectedVersion: "ws_1"},
	}
	for _, state := range states {
		descriptor, ok, err := ui.inputFlowDescriptor(t.Context(), state)
		if err != nil {
			t.Fatalf("%s descriptor: %v", state.InputKind, err)
		}
		if !ok || len(descriptor.Fields) == 0 || descriptor.Build == nil {
			t.Fatalf("%s is not a structured input flow: %#v", state.InputKind, descriptor)
		}
		for _, field := range descriptor.Fields {
			if strings.TrimSpace(field.Key) == "" || strings.TrimSpace(field.Label) == "" || strings.TrimSpace(field.Description) == "" {
				t.Fatalf("%s has under-described field: %#v", state.InputKind, field)
			}
			if (field.Kind == inputFlowEnum || field.Kind == inputFlowBool) && len(field.Options) == 0 {
				t.Fatalf("%s field %s has no selectable values", state.InputKind, field.Key)
			}
		}
	}
}

func TestLLMProviderAddFlowReplacesPipePayloadAndAllowsOptionalFields(t *testing.T) {
	ui, _ := newDomainTestInterface(t, &domainTestDispatcher{})
	descriptor, ok, err := ui.llmInputFlow(t.Context(), ActionState{InputKind: inputLLMProviderAdd})
	if err != nil || !ok {
		t.Fatalf("LLM add flow ok=%v err=%v", ok, err)
	}
	if title, _, _ := llmInputPrompt(inputLLMProviderAdd); title != "" {
		t.Fatalf("legacy pipe prompt remains reachable: %q", title)
	}
	if len(descriptor.Fields) != 7 {
		t.Fatalf("LLM add fields=%d want=7", len(descriptor.Fields))
	}
	byKey := map[string]inputFlowField{}
	for _, field := range descriptor.Fields {
		byKey[field.Key] = field
	}
	if byKey["name"].Required || byKey["model"].Required {
		t.Fatalf("optional LLM fields became required: name=%#v model=%#v", byKey["name"], byKey["model"])
	}
	if len(byKey["protocol"].Options) != 2 || len(byKey["auth_mode"].Options) != 3 || len(byKey["discovery"].Options) != 3 {
		t.Fatalf("LLM enum options are incomplete")
	}
	flow := newInputFlowState(descriptor)
	flow.Values["id"] = "acme"
	flow.Values["base_url"] = "https://api.example.com/v1"
	value, err := descriptor.Build(inputFlowDataFor(descriptor, flow))
	if err != nil {
		t.Fatal(err)
	}
	write := value.(application.LLMProviderWriteInput)
	if write.ID != "acme" || write.Config.Name != "" || write.Config.Model != "" || write.Config.Protocol != llm.ProtocolOpenAI || write.Config.AuthMode != llm.AuthNone || write.Config.Discovery != llm.DiscoveryNone {
		t.Fatalf("LLM add input=%#v", write)
	}
}

func TestInputFlowReviewMasksSecretValues(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	descriptor := inputFlowDescriptor{
		Title: "Secret flow", SubmitLabel: "Save",
		Fields: []inputFlowField{
			{Key: "name", Label: "Name", Description: "Display name.", Kind: inputFlowText, Required: true},
			{Key: "token", Label: "Token", Description: "Sensitive token.", Kind: inputFlowSecret, Secret: true, Required: true},
		},
		Build: func(data inputFlowData) (any, error) { return data.Value("name"), nil },
	}
	state := ActionState{
		Route: RouteOperation, Back: RouteHome,
		InputFlow: &inputFlowState{
			FieldIndex: len(descriptor.Fields),
			Values:     map[string]string{"name": "Acme", "token": "super-secret-value"},
		},
	}
	screen, err := ui.inputFlowReviewScreen(owner, state, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	text := screenText(screen)
	if strings.Contains(text, "super-secret-value") {
		t.Fatalf("secret leaked into review: %q", text)
	}
	if !strings.Contains(text, "Provided") {
		t.Fatalf("review does not describe secret state: %q", text)
	}
}

func TestSettingInputFlowUsesFieldMetadata(t *testing.T) {
	field, err := settingInputFlowField(application.SettingResult{
		Spec: config.FieldSpec{
			Key: "mode", Label: "Mode", Description: "Controls the mode.", Guidance: "Choose the safest compatible mode.",
			Kind: config.FieldEnum, Editable: true,
			Values: []config.FieldValueSpec{
				{Value: "safe", Description: "Use guarded behavior."},
				{Value: "fast", Description: "Prefer throughput."},
			},
		},
		Value: "safe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if field.Kind != inputFlowEnum || field.Description != "Controls the mode. Choose the safest compatible mode." || len(field.Options) != 2 {
		t.Fatalf("setting field=%#v", field)
	}
	if field.Options[0].Value != "safe" || field.Options[0].Description != "Use guarded behavior." {
		t.Fatalf("setting options=%#v", field.Options)
	}
}

func TestParseSettingFlowChangesUsesReadableLineSyntax(t *testing.T) {
	changes, err := parseSettingFlowChanges("http.mcp.port=4000\n!optional.key\ntelegram.enabled=true")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].Key != "http.mcp.port" || changes[0].Value != "4000" || changes[1].Key != "optional.key" || !changes[1].Unset || changes[2].Value != "true" {
		t.Fatalf("changes=%#v", changes)
	}
}

func TestInputFlowReplyMovesRootBelowUserReply(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	api := &settingsTestAPI{}
	ui.runtime.api = api

	descriptor, ok := workspaceInputFlow(ActionState{InputKind: inputWorkspaceRegister})
	if !ok {
		t.Fatal("workspace register input flow is unavailable")
	}
	flow := newInputFlowState(descriptor)
	flow.MessageID = 50
	state := ActionState{
		Route: RouteOperation, Back: RouteWorkspaces, InputKind: inputWorkspaceRegister, InputFlow: flow,
	}
	pending := PendingInput{Owner: owner, PromptMessageID: 100, Action: &state}
	message := Message{MessageID: 101, From: &User{ID: owner.UserID}, Chat: Chat{ID: owner.ChatID}, Text: "/home/me/project"}

	if err := ui.handleInputFlowReply(t.Context(), owner, pending, message, PendingInputValue{Text: message.Text}); err != nil {
		t.Fatal(err)
	}
	if len(api.screens) != 1 || !strings.Contains(screenText(api.screens[0]), "Review before applying") {
		t.Fatalf("replacement root was not sent below the reply: %#v", api.screens)
	}
	if !slices.Contains(api.deleted, int64(50)) {
		t.Fatalf("old root message was not deleted: %v", api.deleted)
	}
	if !slices.Contains(api.deleted, int64(100)) {
		t.Fatalf("ForceReply prompt was not deleted: %v", api.deleted)
	}
	if slices.Contains(api.deleted, int64(101)) {
		t.Fatalf("non-secret user reply should remain visible: %v", api.deleted)
	}
}
