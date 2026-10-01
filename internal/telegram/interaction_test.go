package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
)

type orderedInteractiveAPI struct {
	interactiveTestAPI
	events  *[]string
	deleted []int64
}

func (api *orderedInteractiveAPI) EditScreen(ctx context.Context, chatID, messageID int64, screen Screen) error {
	if api.events != nil {
		if strings.Contains(screenText(screen), "Working") {
			*api.events = append(*api.events, "working")
		} else {
			*api.events = append(*api.events, "terminal")
		}
	}
	return api.interactiveTestAPI.EditScreen(ctx, chatID, messageID, screen)
}

func (api *orderedInteractiveAPI) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error {
	if api.events != nil {
		*api.events = append(*api.events, "answer")
	}
	return api.interactiveTestAPI.AnswerCallback(ctx, callbackID, text, alert)
}

func (api *orderedInteractiveAPI) DeleteMessage(_ context.Context, _ int64, messageID int64) error {
	api.deleted = append(api.deleted, messageID)
	if api.events != nil {
		*api.events = append(*api.events, "delete")
	}
	return nil
}

type orderedDispatcher struct {
	recordingDispatcher
	events *[]string
}

func (dispatcher *orderedDispatcher) Dispatch(ctx context.Context, request application.DispatchRequest) (application.DispatchResult, error) {
	if dispatcher.events != nil {
		*dispatcher.events = append(*dispatcher.events, "dispatch")
	}
	return dispatcher.recordingDispatcher.Dispatch(ctx, request)
}

func TestButtonTransportValidationAndNativeSerialization(t *testing.T) {
	rows := [][]Button{
		{{Text: "Approve", CallbackData: "signed", Role: ButtonRolePositive}},
		{{Text: "Docs", URL: "https://example.com/docs", Style: ButtonStyleDanger}},
		{{Text: "Copy ID", CopyText: "run_123", Role: ButtonRoleCopy}},
		{{Text: "Open logs", WebAppURL: "https://example.com/logs", Role: ButtonRoleNavigation}},
		{{Text: "Unavailable", Disabled: true, Role: ButtonRoleNeutral}},
	}
	if err := validateKeyboard(rows); err != nil {
		t.Fatal(err)
	}
	markup := screenKeyboard(rows)
	if len(markup.InlineKeyboard) != len(rows) {
		t.Fatalf("serialized rows=%d want=%d", len(markup.InlineKeyboard), len(rows))
	}
	if got := markup.InlineKeyboard[0][0]; got.CallbackData != "signed" || got.Style != string(ButtonStyleSuccess) {
		t.Fatalf("callback button=%#v", got)
	}
	if got := markup.InlineKeyboard[1][0]; got.URL == "" || got.Style != "" {
		t.Fatalf("URL button must stay neutral: %#v", got)
	}
	if got := markup.InlineKeyboard[2][0]; got.CopyText == nil || got.CopyText.Text != "run_123" || got.Style != "" {
		t.Fatalf("copy button=%#v", got)
	}
	if got := markup.InlineKeyboard[3][0]; got.WebApp == nil || got.WebApp.URL != "https://example.com/logs" || got.Style != "" {
		t.Fatalf("WebApp button=%#v", got)
	}
	if got := markup.InlineKeyboard[4][0]; got.Disabled == nil || got.CallbackData != "" || got.URL != "" {
		t.Fatalf("disabled button=%#v", got)
	}
}

func TestButtonTransportRejectsConflictsAndBounds(t *testing.T) {
	tests := []Button{
		{Text: "Conflict", CallbackData: "a", URL: "https://example.com"},
		{Text: "Conflict", CallbackData: "a", CopyText: "b"},
		{Text: "Conflict", URL: "https://example.com", WebAppURL: "https://example.com/app"},
		{Text: "Missing"},
		{Text: "Disabled action", Disabled: true, CallbackData: "a"},
		{Text: "Callback", CallbackData: strings.Repeat("x", MaxCallbackDataBytes+1)},
		{Text: "Copy", CopyText: strings.Repeat("x", MaxCopyTextBytes+1)},
		{Text: "WebApp", WebAppURL: "http://example.com/app"},
		{Text: "Style", CallbackData: "a", Style: ButtonStyle("rainbow")},
	}
	for _, button := range tests {
		if err := validateKeyboard([][]Button{{button}}); err == nil {
			t.Fatalf("button should be rejected: %#v", button)
		}
	}
}

func TestSemanticButtonRoleOverridesDecoration(t *testing.T) {
	tests := []struct {
		button Button
		want   ButtonStyle
	}{
		{Button{Text: "Back", CallbackData: "a", Role: ButtonRoleNavigation, Style: ButtonStyleDanger}, ""},
		{Button{Text: "Delete", CallbackData: "a", Role: ButtonRoleDestructive}, ButtonStyleDanger},
		{Button{Text: "Enable", CallbackData: "a", Role: ButtonRolePositive}, ButtonStyleSuccess},
		{Button{Text: "Retry", CallbackData: "a", Role: ButtonRolePrimary}, ButtonStylePrimary},
		{Button{Text: "Clear view", CallbackData: "a", Role: ButtonRoleNeutral, Style: ButtonStyleDanger}, ""},
		{SetupPrivateChatButton("signed"), ButtonStylePrimary},
	}
	for _, test := range tests {
		if got := semanticButtonStyle(test.button); got != test.want {
			t.Fatalf("style(%#v)=%q want=%q", test.button, got, test.want)
		}
	}
}

func TestButtonCapabilityFallbackDoesNotRewriteAuthority(t *testing.T) {
	rows := [][]Button{{
		{Text: "Delete", CallbackData: "signed", Role: ButtonRoleDestructive},
		{Text: "Copy", CopyText: "safe", Role: ButtonRoleCopy},
		{Text: "Disabled", Disabled: true},
		{Text: "WebApp", WebAppURL: "https://example.com/app"},
	}}
	markup := screenKeyboardWithCapabilities(rows, keyboardCapabilities{})
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("fallback keyboard=%#v", markup.InlineKeyboard)
	}
	button := markup.InlineKeyboard[0][0]
	if button.CallbackData != "signed" || button.Style != "" {
		t.Fatalf("callback authority changed under fallback: %#v", button)
	}
	if native := screenKeyboard(rows); len(native.InlineKeyboard) != 2 || len(native.InlineKeyboard[0]) != 3 || len(native.InlineKeyboard[1]) != 1 {
		t.Fatalf("native supported affordances were dropped: %#v", native.InlineKeyboard)
	}
}

func TestKeyboardRowsReduceColumnsForLongLabels(t *testing.T) {
	rows := [][]Button{{
		{Text: "A fairly long action", CallbackData: "a"},
		{Text: "Another long action", CallbackData: "b"},
		{Text: "Short", CallbackData: "c"},
	}}
	responsive := responsiveKeyboardRows(rows)
	if len(responsive) != 2 || len(responsive[0]) != 1 || len(responsive[1]) != 2 {
		t.Fatalf("long labels were not given enough horizontal space: %#v", responsive)
	}
	compact := responsiveKeyboardRows([][]Button{{
		{Text: "« Back", CallbackData: "b"},
		{Text: "⌂ Home", CallbackData: "h"},
		{Text: "↻ Refresh", CallbackData: "r"},
	}})
	if len(compact) != 1 || len(compact[0]) != 3 {
		t.Fatalf("compact navigation was split unnecessarily: %#v", compact)
	}
}

func TestBoundedActionGroupsAndLabels(t *testing.T) {
	groups := ActionGroups{
		Primary:     []Button{{Text: "One", CallbackData: "1"}, {Text: "Two", CallbackData: "2"}, {Text: "Three", CallbackData: "3"}, {Text: "Four", CallbackData: "4"}},
		Secondary:   []Button{{Text: "Details", CallbackData: "d"}},
		Destructive: []Button{{Text: "Delete", CallbackData: "x", Role: ButtonRoleDestructive}},
		Navigation:  []Button{{Text: "Back", CallbackData: "b", Role: ButtonRoleNavigation}},
	}
	rows := BoundedActionGroups(groups)
	if len(rows) != 5 || len(rows[0]) != maxActionButtonsPerRow || len(rows[1]) != 1 {
		t.Fatalf("action rows=%#v", rows)
	}
	if rows[0][0].Text != "One" || rows[1][0].Text != "Four" || rows[2][0].Text != "Details" || rows[3][0].Text != "Delete" || rows[4][0].Text != "Back" {
		t.Fatalf("action group order=%#v", rows)
	}
	longLabel := strings.Repeat("界", 80)
	if action := CompactActionLabel(longLabel); action != longLabel {
		t.Fatalf("action label was truncated: %q", action)
	}
	if resource := CompactResourceLabel(longLabel); resource != longLabel {
		t.Fatalf("resource label was truncated: %q", resource)
	}
	if button, ok := CopyValueButton("Copy ID", strings.Repeat("x", MaxCopyTextBytes+1)); ok || button.CopyText != "" {
		t.Fatalf("oversized copy value was exposed: %#v", button)
	}
	if button, part := DisabledAction("Unavailable", "Requires canonical adapter", false); button.Text != "" || !strings.Contains(part.Text, "Requires canonical adapter") {
		t.Fatalf("disabled fallback button=%#v part=%#v", button, part)
	}
	resourceRows := ResourceRows(
		Button{Text: strings.Repeat("Resource ", 12), CallbackData: "resource_1", Style: ButtonStyleDanger},
		Button{Text: "Second", CallbackData: "resource_2", URL: "https://example.com"},
	)
	if len(resourceRows) != 2 || len(resourceRows[0]) != 1 || len(resourceRows[1]) != 1 {
		t.Fatalf("resource rows=%#v", resourceRows)
	}
	for _, row := range resourceRows {
		button := row[0]
		if button.Role != ButtonRoleResource || button.Style != "" || button.URL != "" {
			t.Fatalf("resource row is not neutral navigation: %#v", button)
		}
	}
	if resourceRows[0][0].Text != strings.TrimSpace(strings.Repeat("Resource ", 12)) {
		t.Fatalf("resource row label was truncated: %#v", resourceRows[0][0])
	}
}

func TestInputAndWorkingScreensNeverExposeClose(t *testing.T) {
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 2}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 2}
	state := ActionState{Route: RouteStatus, Back: RouteHome, Operation: capability.StatusOverview}
	prompt, err := ui.inputPromptScreen(owner, state, "Enter value", "Send the requested value")
	if err != nil {
		t.Fatal(err)
	}
	failure, err := ui.inputFailureScreen(owner, state, errors.New("invalid value"))
	if err != nil {
		t.Fatal(err)
	}
	working := workingScreen(state)
	for name, screen := range map[string]Screen{"prompt": prompt, "failure": failure, "working": working} {
		for _, row := range screen.Keyboard {
			for _, button := range row {
				if button.Text == "Close" {
					t.Fatalf("%s screen exposed Close: %#v", name, screen.Keyboard)
				}
			}
		}
	}
	if prompt.Keyboard[0][0].Text != "Cancel" || failure.Keyboard[0][0].Text != "Retry" {
		t.Fatalf("input controls prompt=%#v failure=%#v", prompt.Keyboard, failure.Keyboard)
	}
}

func TestInputLifecycleEditsOnePromptAndDeletesSecretInput(t *testing.T) {
	api := &orderedInteractiveAPI{}
	runtime := &Runtime{api: api, generation: 3, health: Health{Running: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 3}
	state := ActionState{Route: RouteSettings, Back: RouteHome}
	if err := ui.editInputPrompt(t.Context(), owner, 10, state, "Token", "Enter value"); err != nil {
		t.Fatal(err)
	}
	if err := ui.editInputFailure(t.Context(), owner, 10, state, errors.New("invalid value")); err != nil {
		t.Fatal(err)
	}
	result := Screen{Text: "Saved", Keyboard: [][]Button{{{Text: "Close", Disabled: true}}}}
	if err := ui.completeInput(t.Context(), owner, 10, 11, true, result); err != nil {
		t.Fatal(err)
	}
	if len(api.edited) != 3 || !strings.Contains(screenText(api.edited[0]), "Input required") || !strings.Contains(screenText(api.edited[1]), "Input failed") || screenText(api.edited[2]) != "Saved" {
		t.Fatalf("input lifecycle screens=%#v", api.edited)
	}
	if len(api.deleted) != 1 || api.deleted[0] != 11 {
		t.Fatalf("secret input deletion=%v", api.deleted)
	}
}

func TestMutationCallbackAcknowledgesBeforeWorkingDispatchAndTerminalEdit(t *testing.T) {
	events := []string{}
	api := &orderedInteractiveAPI{events: &events}
	dispatcher := &orderedDispatcher{recordingDispatcher: recordingDispatcher{result: "purged"}, events: &events}
	runtime := &Runtime{
		api: api, generation: 4,
		health: Health{Running: true, AuthorizationConfigured: true},
	}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: dispatcher})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 4}
	button, err := ui.stateButton(owner, "Confirm", CallbackConfirm, ActionState{
		Route: "workspace.purge", Back: RouteHome, Operation: capability.WorkspacePurge, Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ui.handleCallback(t.Context(), Update{CallbackQuery: &CallbackQuery{
		ID: "cb_order", From: User{ID: 42}, Data: button.CallbackData,
		Message: &Message{MessageID: 9, Chat: Chat{ID: 42, Type: "private"}},
	}})
	want := []string{"answer", "working", "dispatch", "terminal"}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("callback lifecycle=%v want=%v", events, want)
	}
	if len(api.edited) != 2 || !strings.Contains(screenText(api.edited[0]), "Working") || !strings.Contains(screenText(api.edited[1]), "Success") {
		t.Fatalf("edited screens=%#v", api.edited)
	}
}

func TestCloseDismissesOnlyEligibleTerminalMessage(t *testing.T) {
	events := []string{}
	api := &orderedInteractiveAPI{events: &events}
	runtime := &Runtime{
		api: api, generation: 5,
		health: Health{Running: true, AuthorizationConfigured: true},
	}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 5}
	button, err := ui.closeButton(owner, ActionState{Route: "terminal", Back: RouteHome})
	if err != nil {
		t.Fatal(err)
	}
	ui.handleCallback(t.Context(), Update{CallbackQuery: &CallbackQuery{
		ID: "cb_close", From: User{ID: 42}, Data: button.CallbackData,
		Message: &Message{MessageID: 21, Chat: Chat{ID: 42, Type: "private"}},
	}})
	if strings.Join(events, "|") != "answer|delete" || len(api.deleted) != 1 || api.deleted[0] != 21 {
		t.Fatalf("close events=%v deleted=%v", events, api.deleted)
	}
}

func TestPaginationUsesNumberedButtonsAndMarksCurrentPage(t *testing.T) {
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 7}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	first, err := ui.paginationKeyboard(owner, ActionState{Route: RouteStatus, Back: RouteHome, Page: 0}, 25, 8)
	if err != nil {
		t.Fatal(err)
	}
	last, err := ui.paginationKeyboard(owner, ActionState{Route: RouteStatus, Back: RouteHome, Page: 3}, 25, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(first[0]) != 4 || first[0][0].Text != "( 1 )" || !first[0][0].Disabled || first[0][1].Text != "2" || first[0][1].Disabled {
		t.Fatalf("first pagination=%#v", first[0])
	}
	if len(last[0]) != 4 || last[0][0].Text != "1" || last[0][3].Text != "( 4 )" || !last[0][3].Disabled {
		t.Fatalf("last pagination=%#v", last[0])
	}
}

func TestStateButtonEncodingFailureReleasesAllocatedViewState(t *testing.T) {
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 8}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 8}
	if _, err := ui.stateButton(owner, "Invalid", CallbackAction(strings.Repeat("a", MaxCallbackDataBytes)), ActionState{Route: RouteHome}); err == nil {
		t.Fatal("expected oversized callback encoding to fail")
	}
	if len(ui.states.entries) != 0 {
		t.Fatalf("view state leaked after encoding failure: %d entries", len(ui.states.entries))
	}
}

func TestPublishedCommandRegistryMatchesProductionRouter(t *testing.T) {
	ui, err := NewInterface(InterfaceOptions{Runtime: &Runtime{generation: 9}})
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, item := range ui.router.snapshot(true) {
		routes[item.key] = true
	}
	for _, command := range Commands() {
		if !routes[command.Name] {
			t.Fatalf("published command /%s is not registered in the production router", command.Name)
		}
	}
}

func TestCommandEntrySendsTopLevelScreenWhileInlineCallbackEdits(t *testing.T) {
	api := &interactiveTestAPI{}
	runtime := &Runtime{
		api: api, generation: 11,
		config: config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}},
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
	}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: &recordingDispatcher{result: application.StatusOverview{}}})
	if err != nil {
		t.Fatal(err)
	}
	ui.Handle(t.Context(), Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/home"}})
	if len(api.sent) != 1 || len(api.edited) != 0 {
		t.Fatalf("command delivery sent=%d edited=%d", len(api.sent), len(api.edited))
	}
	status := api.sent[0].Keyboard[0][0]
	ui.Handle(t.Context(), Update{CallbackQuery: &CallbackQuery{
		ID: "cb_status", From: User{ID: 42}, Data: status.CallbackData,
		Message: &Message{MessageID: 13, Chat: Chat{ID: 42, Type: "private"}},
	}})
	if len(api.sent) != 1 || len(api.edited) != 1 {
		t.Fatalf("callback delivery sent=%d edited=%d", len(api.sent), len(api.edited))
	}
}

func TestStaleVersionCallbackAlertsAndRendersRecoveryNavigation(t *testing.T) {
	api := &interactiveTestAPI{}
	runtime := &Runtime{
		api: api, generation: 10,
		health: Health{Running: true, AuthorizationConfigured: true},
	}
	dispatcher := &recordingDispatcher{result: "mutated"}
	ui, err := NewInterface(InterfaceOptions{
		Runtime: runtime, Dispatcher: dispatcher,
		VersionResolver: func(context.Context, capability.ID, string) (string, error) { return "v2", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 10}
	button, err := ui.stateButton(owner, "Delete", CallbackConfirm, ActionState{
		Route: "resource.update", Back: RouteHome, Operation: capability.WorkspacePurge,
		ResourceID: "ws_a", ExpectedVersion: "v1", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ui.handleCallback(t.Context(), Update{CallbackQuery: &CallbackQuery{
		ID: "cb_stale", From: User{ID: 42}, Data: button.CallbackData,
		Message: &Message{MessageID: 7, Chat: Chat{ID: 42, Type: "private"}},
	}})
	if len(dispatcher.calls) != 0 {
		t.Fatalf("stale callback dispatched=%#v", dispatcher.calls)
	}
	if len(api.answers) != 1 || !strings.Contains(api.answers[0], "stale") {
		t.Fatalf("stale callback answer=%#v", api.answers)
	}
	if len(api.edited) != 1 || !strings.Contains(screenText(api.edited[0]), "Stale control") || len(api.edited[0].Keyboard) != 1 || len(api.edited[0].Keyboard[0]) != 2 {
		t.Fatalf("stale recovery screen=%#v", api.edited)
	}
	if api.edited[0].Keyboard[0][0].Text != "« Back" || api.edited[0].Keyboard[0][1].Text != "⌂ Home" {
		t.Fatalf("stale recovery controls=%#v", api.edited[0].Keyboard)
	}
}

func TestReferenceButtonHierarchyRemainsRepresentableWithoutRejectedArchitecture(t *testing.T) {
	runtime := &Runtime{generation: 3, health: Health{Enabled: true, TokenConfigured: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: &recordingDispatcher{result: application.StatusOverview{}}})
	if err != nil {
		t.Fatal(err)
	}
	home, err := ui.homeScreen(ViewOwner{ChatID: 42, UserID: 42, Generation: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"Status", "System"}, {"Requests", "Completions"}, {"Workspaces", "Network"},
		{"Upstreams", "Integrations"}, {"LLM", "Instructions"}, {"Settings", "Auth"}, {"Logs"}, {"Help", "↻ Refresh"},
	}
	got := make([][]string, 0, len(home.Keyboard))
	for _, row := range home.Keyboard {
		labels := make([]string, 0, len(row))
		for _, button := range row {
			labels = append(labels, button.Text)
		}
		got = append(got, labels)
	}
	if len(got) != len(want) {
		t.Fatalf("home hierarchy=%#v", got)
	}
	for index := range want {
		if strings.Join(got[index], "|") != strings.Join(want[index], "|") {
			t.Fatalf("home row %d=%v want=%v", index, got[index], want[index])
		}
	}
	help, err := ui.commandsScreen(ViewOwner{ChatID: 42, UserID: 42, Generation: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"Summary", "Details", "Back / Close", "Copy", "Refresh"} {
		if !strings.Contains(screenText(help), phrase) {
			t.Fatalf("help missing %q: %s", phrase, screenText(help))
		}
	}

	parity := []struct {
		name   string
		button Button
		style  ButtonStyle
	}{
		{"setup", SetupPrivateChatButton("setup"), ButtonStylePrimary},
		{"approval-positive", Button{Text: "Approve", CallbackData: "a", Role: ButtonRolePositive}, ButtonStyleSuccess},
		{"approval-deny", Button{Text: "Deny", CallbackData: "d", Role: ButtonRoleDestructive}, ButtonStyleDanger},
		{"workspace", Button{Text: "Details", CallbackData: "w", Role: ButtonRoleView}, ""},
		{"container", Button{Text: "Refresh", CallbackData: "c", Role: ButtonRoleNavigation}, ""},
		{"execution", Button{Text: "Copy ID", CopyText: "exec_1", Role: ButtonRoleCopy}, ""},
		{"process", Button{Text: "Stop", CallbackData: "p", Role: ButtonRoleDestructive}, ButtonStyleDanger},
		{"auth-credential", Button{Text: "Copy", CopyText: "safe-redacted", Role: ButtonRoleCopy}, ""},
		{"settings-config", Button{Text: "Configure", CallbackData: "s", Role: ButtonRoleConfigure}, ""},
		{"tunnel", Button{Text: "Start", CallbackData: "t", Role: ButtonRolePositive}, ButtonStyleSuccess},
		{"network", Button{Text: "Stop", CallbackData: "n", Role: ButtonRoleDestructive}, ButtonStyleDanger},
		{"upstream", Button{Text: "Enable", CallbackData: "u", Role: ButtonRolePositive}, ButtonStyleSuccess},
		{"integration", Button{Text: "Disable", CallbackData: "i", Role: ButtonRoleDestructive}, ButtonStyleDanger},
		{"system", Button{Text: "Refresh", CallbackData: "sys", Role: ButtonRoleNavigation}, ""},
		{"instruction", Button{Text: "Details", CallbackData: "ins", Role: ButtonRoleView}, ""},
		{"tool", Button{Text: "Clear view", CallbackData: "tool", Role: ButtonRoleNeutral}, ""},
		{"persisted-clear", Button{Text: "Clear", CallbackData: "clear", Role: ButtonRoleDestructive}, ButtonStyleDanger},
		{"completion", Button{Text: "Close", CallbackData: "close", Role: ButtonRoleNavigation}, ""},
		{"error-retry", Button{Text: "Retry", CallbackData: "retry", Role: ButtonRolePrimary}, ButtonStylePrimary},
		{"terminal", Button{Text: "Close", CallbackData: "terminal", Role: ButtonRoleNavigation}, ""},
	}
	for _, item := range parity {
		if got := semanticButtonStyle(item.button); got != item.style {
			t.Fatalf("reference parity %s style=%q want=%q", item.name, got, item.style)
		}
	}
}
