package telegram

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
)

type settingsTestAPI struct {
	screens     []Screen
	pickerCalls []int
	richOptions []RichMessageOptions
	deleted     []int64
	pickerErr   error
}

func (*settingsTestAPI) GetMe(context.Context) (User, error) {
	return User{ID: 1, Username: "bot"}, nil
}
func (*settingsTestAPI) SendMessage(context.Context, int64, string) error { return nil }
func (api *settingsTestAPI) SendScreen(_ context.Context, _ int64, screen Screen) error {
	api.screens = append(api.screens, screen)
	return nil
}
func (*settingsTestAPI) EditScreen(context.Context, int64, int64, Screen) error     { return nil }
func (*settingsTestAPI) AnswerCallback(context.Context, string, string, bool) error { return nil }
func (api *settingsTestAPI) SendUserPicker(_ context.Context, _ int64, requestID int, _ string) (int64, error) {
	api.pickerCalls = append(api.pickerCalls, requestID)
	if api.pickerErr != nil {
		return 0, api.pickerErr
	}
	return 99, nil
}
func (api *settingsTestAPI) SendRichMessage(_ context.Context, _ int64, _ Screen, options RichMessageOptions) (int64, error) {
	api.richOptions = append(api.richOptions, options)
	return 100, nil
}
func (*settingsTestAPI) SendChatAction(context.Context, int64, string) error { return nil }
func (api *settingsTestAPI) DeleteMessage(_ context.Context, _ int64, messageID int64) error {
	api.deleted = append(api.deleted, messageID)
	return nil
}

func TestSettingDetailUsesCanonicalCapabilityMetadata(t *testing.T) {
	writable := config.FieldSpec{
		Key: "example.value", Label: "Example", Description: "Canonical example",
		Kind: config.FieldString, ApplicationOwner: "settings", Writable: true,
		Clearable: true, Rotatable: true, Revealable: true, Verifiable: true, Secret: true,
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.ConfigGet: application.SettingResult{Spec: writable, Value: "sec********alue"},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.settingDetailScreen(t.Context(), owner, ActionState{Route: RouteSetting, ResourceID: writable.Key})
	if err != nil {
		t.Fatal(err)
	}
	labels := keyboardLabels(screen.Keyboard)
	for _, want := range []string{"Set", "Clear", "Rotate", "Reveal", "Verify"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("metadata-driven setting control %q missing: %s", want, labels)
		}
	}

	readonly := writable
	readonly.Key = "example.readonly"
	readonly.Writable, readonly.Clearable, readonly.Rotatable, readonly.Revealable, readonly.Verifiable = false, false, false, false, false
	dispatcher.values[capability.ConfigGet] = application.SettingResult{Spec: readonly, Value: "derived"}
	screen, err = ui.settingDetailScreen(t.Context(), owner, ActionState{Route: RouteSetting, ResourceID: readonly.Key})
	if err != nil {
		t.Fatal(err)
	}
	labels = keyboardLabels(screen.Keyboard)
	for _, forbidden := range []string{"Set", "Clear", "Rotate", "Reveal", "Verify"} {
		if strings.Contains(labels, forbidden) {
			t.Fatalf("read-only setting exposed %q: %s", forbidden, labels)
		}
	}
}

func TestTelemetryScreenNeverRendersEndpointOrIdentityValue(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	screen, err := ui.telemetryScreen(owner, application.TelemetryStatus{
		PersistedEnabled: true, EffectiveEnabled: false, Source: "environment",
		EnvironmentOverride: true, EndpointAvailable: true, EndpointHost: "secret.telemetry.example",
		Product: "codemcp", IdentityPresent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := RichFallback(screen.Rich).Text
	if strings.Contains(text, "secret.telemetry.example") {
		t.Fatalf("telemetry endpoint leaked: %q", text)
	}
	for _, want := range []string{"Configured", "Effective", "Source", "Transport available", "Identity present"} {
		if !strings.Contains(text, want) {
			t.Fatalf("telemetry status missing %q: %q", want, text)
		}
	}
}

func TestTypeSafeScreenUsesCanonicalMaskedCredentialPreview(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.IntegrationTypeSafeStatus: application.TypeSafeStatus{
			Enabled: true, APIKeyConfigured: true, State: application.TypeSafeReady, Model: "jev", TimeoutMS: 3000,
		},
		capability.ConfigGet: application.SettingResult{
			Spec:  config.FieldSpec{Key: "integrations.typesafe.api_key", Label: "TypeSafe API key", Secret: true},
			Value: "sk-t********cret",
		},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.integrationScreen(t.Context(), owner, ActionState{Route: RouteIntegration, ResourceID: "typesafe"})
	if err != nil {
		t.Fatal(err)
	}
	text := RichFallback(screen.Rich).Text
	if !strings.Contains(text, "sk-t********cret") || strings.Contains(text, "configured") {
		t.Fatalf("TypeSafe credential presentation=%q", text)
	}
}

func TestIntegrationsScreenKeepsEveryIntegrationReachable(t *testing.T) {
	dispatcher := &domainTestDispatcher{errors: map[capability.ID]error{
		capability.ConfigList:                 context.Canceled,
		capability.IntegrationRTKStatus:       context.Canceled,
		capability.IntegrationCodeGraphStatus: context.Canceled,
		capability.IntegrationCFStatus:        context.Canceled,
		capability.IntegrationTypeSafeStatus:  context.Canceled,
		capability.TelemetryStatus:            context.Canceled,
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.integrationsScreen(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	wantRows := [][]string{
		{"Ponytail", "Caveman", "RTK"},
		{"CodeGraph", "Cloudflare Quick Tunnel", "TypeSafe"},
		{"Telemetry"},
		{"Back", "Home"},
	}
	if len(screen.Keyboard) != len(wantRows) {
		t.Fatalf("integration keyboard rows=%d want=%d: %#v", len(screen.Keyboard), len(wantRows), screen.Keyboard)
	}
	for rowIndex, want := range wantRows {
		row := screen.Keyboard[rowIndex]
		if len(row) != len(want) {
			t.Fatalf("integration keyboard row %d len=%d want=%d: %#v", rowIndex, len(row), len(want), row)
		}
		for buttonIndex, label := range want {
			if row[buttonIndex].Text != label {
				t.Fatalf("integration keyboard[%d][%d]=%q want=%q", rowIndex, buttonIndex, row[buttonIndex].Text, label)
			}
			if rowIndex < len(wantRows)-1 && row[buttonIndex].Role != ButtonRoleResource {
				t.Fatalf("integration resource button role=%q want=%q", row[buttonIndex].Role, ButtonRoleResource)
			}
		}
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		t.Fatalf("integration keyboard invalid: %v", err)
	}
}

func TestUserSelectionStoreRejectsForeignAndReplay(t *testing.T) {
	store := NewUserSelectionStore(time.Minute)
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	if err := store.Put(owner, 77, "42"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Match(ViewOwner{ChatID: 42, UserID: 43, Generation: 7}, 77); ok {
		t.Fatal("foreign user consumed picker response")
	}
	if _, ok := store.Match(ViewOwner{ChatID: 42, UserID: 42, Generation: 8}, 77); ok {
		t.Fatal("foreign runtime generation consumed picker response")
	}
	if state, ok := store.Match(owner, 77); !ok || state.CurrentRaw != "42" {
		t.Fatalf("valid picker response state=%#v ok=%v", state, ok)
	}
	if _, ok := store.Match(owner, 77); ok {
		t.Fatal("replayed picker response was accepted")
	}
}

func TestBeginUserPickerBindsNativeRequestToOwnerAndGeneration(t *testing.T) {
	api := &settingsTestAPI{}
	runtime := &Runtime{api: api, generation: 7, health: Health{Running: true, Enabled: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: &domainTestDispatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	if err := ui.beginUserPicker(t.Context(), owner, ActionState{ExpectedVersion: "42"}); err != nil {
		t.Fatal(err)
	}
	if len(api.pickerCalls) != 1 {
		t.Fatalf("native picker calls=%#v", api.pickerCalls)
	}
	requestID := api.pickerCalls[0]
	if _, ok := ui.userSelections.Match(ViewOwner{ChatID: 42, UserID: 42, Generation: 8}, requestID); ok {
		t.Fatal("picker response from stale runtime generation was accepted")
	}
	if state, ok := ui.userSelections.Match(owner, requestID); !ok || state.CurrentRaw != "42" {
		t.Fatalf("bound picker state=%#v ok=%v", state, ok)
	}
}

func TestUserPickerFallsBackToBoundManualConfirmation(t *testing.T) {
	api := &settingsTestAPI{pickerErr: context.Canceled}
	runtime := &Runtime{api: api, generation: 7, health: Health{Running: true, Enabled: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: &domainTestDispatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	state := ActionState{
		Route: RouteOperation, Back: RouteAuthorizedUsers, Operation: capability.ConfigSet,
		ResourceID: "telegram.allowed_user_ids", ExpectedVersion: "42", InputKind: inputTelegramUserPicker,
	}
	if err := ui.beginUserPicker(t.Context(), owner, state); err != nil {
		t.Fatal(err)
	}
	if len(api.pickerCalls) != 1 || len(api.richOptions) != 1 {
		t.Fatalf("picker calls=%#v rich prompts=%#v", api.pickerCalls, api.richOptions)
	}
	pending, ok := ui.inputs.Match(owner, Message{
		MessageID: 101, Chat: Chat{ID: 42, Type: "private"}, From: &User{ID: 42}, Text: "99",
		ReplyToMessage: &Message{MessageID: 100},
	})
	if !ok || pending.Action == nil || pending.Action.InputKind != inputTelegramUserManual || !pending.Action.ForceConfirm {
		t.Fatalf("manual fallback pending=%#v ok=%v", pending, ok)
	}
}

func TestUsersSharedOnlyCreatesConfirmationProposal(t *testing.T) {
	api := &settingsTestAPI{}
	runtime := &Runtime{
		api: api, generation: 7,
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	if err := ui.userSelections.Put(owner, 88, "42"); err != nil {
		t.Fatal(err)
	}
	ui.handleUsersShared(t.Context(), Update{Message: &Message{
		Chat: Chat{ID: 42, Type: "private"}, From: &User{ID: 42}, MessageID: 10,
		UsersShared: &UsersShared{RequestID: 88, Users: []SharedUser{{UserID: 99, Username: "candidate"}}},
	}})
	if len(dispatcher.calls) != 0 {
		t.Fatalf("user selection mutated settings before confirmation: %#v", dispatcher.calls)
	}
	if len(api.screens) != 1 || !strings.Contains(RichFallback(api.screens[0].Rich).Text, "Selection is only a proposal") {
		t.Fatalf("proposal confirmation screen=%#v", api.screens)
	}
	ui.handleUsersShared(t.Context(), Update{Message: &Message{
		Chat: Chat{ID: 42, Type: "private"}, From: &User{ID: 42}, MessageID: 11,
		UsersShared: &UsersShared{RequestID: 88, Users: []SharedUser{{UserID: 99}}},
	}})
	if len(api.screens) != 1 || len(dispatcher.calls) != 0 {
		t.Fatal("replayed UsersShared produced a second proposal or mutation")
	}
}

func TestSecretSettingResultUsesProtectedMessage(t *testing.T) {
	api := &settingsTestAPI{}
	runtime := &Runtime{api: api, generation: 7, health: Health{Running: true, Enabled: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: &domainTestDispatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	_, handled, err := ui.settingsOperationResultScreen(t.Context(), owner, ActionState{Back: RouteSettings, SecretInput: true}, capability.Spec{}, application.SettingResult{
		Spec: config.FieldSpec{Key: "secret.key", Label: "Secret", Secret: true}, Value: "prefix********suffix",
	})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(api.richOptions) != 1 || !api.richOptions[0].ProtectContent {
		t.Fatalf("secret result options=%#v", api.richOptions)
	}
}

func TestSecretSettingInputIsProtectedAndReplyIsDeleted(t *testing.T) {
	api := &settingsTestAPI{}
	runtime := &Runtime{api: api, generation: 7, health: Health{Running: true, Enabled: true, AuthorizationConfigured: true}}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.ConfigSet: application.SettingResult{
			Spec:  config.FieldSpec{Key: "telegram.token", Label: "Telegram bot token", Secret: true},
			Value: "123********xyz",
		},
	}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	state := ActionState{
		Route: RouteOperation, Back: RouteAuth, Operation: capability.ConfigSet,
		ResourceID: "telegram.token", InputKind: inputSettingSet, SecretInput: true,
	}
	if err := ui.beginActionInput(t.Context(), owner, state); err != nil {
		t.Fatal(err)
	}
	if len(api.richOptions) != 1 || !api.richOptions[0].ProtectContent {
		t.Fatalf("secret input prompt options=%#v", api.richOptions)
	}
	if !ui.handleActionInput(t.Context(), Update{Message: &Message{
		MessageID: 101, Chat: Chat{ID: 42, Type: "private"}, From: &User{ID: 42}, Text: "raw-secret-value",
		ReplyToMessage: &Message{MessageID: 100},
	}}) {
		t.Fatal("secret input was not consumed")
	}
	if !slices.Contains(api.deleted, int64(101)) {
		t.Fatalf("secret reply was not deleted: %#v", api.deleted)
	}
}

func TestWritableManagedSecretInventoryUsesProtectedTelegramInputState(t *testing.T) {
	api := &settingsTestAPI{}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{}}
	runtime := &Runtime{api: api, generation: 7, health: Health{Running: true, Enabled: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	count := 0
	for _, spec := range config.Settings() {
		if !spec.Secret || !spec.Writable {
			continue
		}
		count++
		key := spec.Key
		if spec.Selector != nil {
			key = strings.Replace(spec.Selector.Template, "<id>", "openrouter", 1)
			spec.Key = key
		}
		configured := true
		dispatcher.values[capability.ConfigGet] = application.SettingResult{Spec: spec, Value: "prefix********suffix", Configured: &configured}
		screen, err := ui.settingDetailScreen(t.Context(), owner, ActionState{Route: RouteSettings, Back: RouteSettings, ResourceID: key})
		if err != nil {
			t.Fatalf("setting detail %s: %v", key, err)
		}
		var set Button
		for _, row := range screen.Keyboard {
			for _, button := range row {
				if button.Text == "Set" {
					set = button
				}
			}
		}
		if set.CallbackData == "" {
			t.Fatalf("managed secret %s has no set action", key)
		}
		ref, err := ui.callbacks.Decode(set.CallbackData)
		if err != nil {
			t.Fatalf("decode %s set action: %v", key, err)
		}
		stored, err := ui.states.Get(ref.Token, owner)
		if err != nil {
			t.Fatalf("load %s set state: %v", key, err)
		}
		state, ok := stored.(ActionState)
		if !ok || !state.SecretInput || state.InputKind != inputSettingSet || state.ResourceID != key {
			t.Fatalf("managed secret %s state=%#v", key, stored)
		}
	}
	if count == 0 {
		t.Fatal("writable managed-secret inventory unexpectedly empty")
	}

	for _, key := range []string{"auth.mcp_token", "auth.admin_token"} {
		spec, ok := config.SettingByKey(key)
		if !ok || spec.Writable || !spec.Rotatable || spec.ValueRole != config.SettingValueGenerated {
			t.Fatalf("generated credential metadata drift for %s: %#v", key, spec)
		}
	}
}

func TestRemovingLastAuthorizedUserAtomicallyDisablesTelegram(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.ConfigGet: application.SettingResult{
			Spec:  config.FieldSpec{Key: "telegram.allowed_user_ids", Label: "Telegram authorized users"},
			Value: "42",
		},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.authorizedUsersScreen(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	var remove Button
	for _, row := range screen.Keyboard {
		for _, button := range row {
			if button.Text == "Remove 42" {
				remove = button
			}
		}
	}
	if remove.CallbackData == "" {
		t.Fatal("last-user remove action missing")
	}
	ref, err := ui.callbacks.Decode(remove.CallbackData)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := ui.states.Get(ref.Token, owner)
	if err != nil {
		t.Fatal(err)
	}
	state := stored.(ActionState)
	input := state.Input.(application.ConfigSetInput)
	if input.Action != "apply" || len(input.Changes) != 2 || input.Changes[0].Key != "telegram.allowed_user_ids" || input.Changes[1].Key != "telegram.enabled" || input.Changes[1].Value != "false" {
		t.Fatalf("last-user removal input=%#v", input)
	}
}
