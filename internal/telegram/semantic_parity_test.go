package telegram

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestRestoredTelegramDomainRoutesUseCanonicalDispatch(t *testing.T) {
	tests := []struct {
		name   string
		state  ActionState
		values map[capability.ID]any
		want   capability.ID
		text   string
	}{
		{
			name:  "runtime grants",
			state: ActionState{Route: RouteGrants, Back: RouteRequests, Operation: capability.RequestGrantList},
			values: map[capability.ID]any{capability.RequestGrantList: []approval.Request{{
				ID: "req_grant", Status: approval.StatusApproved, WorkspaceID: "ws_1", TargetTool: "run_command",
				Title: "Run command", RuntimeSessionGrant: true, GrantExpiresAt: time.Now().UTC().Add(time.Hour),
			}}},
			want: capability.RequestGrantList,
			text: "Runtime grants",
		},
		{
			name:  "background processes",
			state: ActionState{Route: RouteProcesses, Back: RouteWorkspace, Operation: capability.ProcessList, ResourceID: "ws_1"},
			values: map[capability.ID]any{capability.ProcessList: []shellruntime.ProcessInfo{{
				ID: "proc_1", Command: "echo ok", CWD: "/workspace", StartedAt: time.Now().UTC().Format(time.RFC3339), Running: true,
			}}},
			want: capability.ProcessList,
			text: "Background processes",
		},
		{
			name:   "codegraph workspace",
			state:  ActionState{Route: RouteCodeGraphWS, Back: RouteWorkspace, Operation: capability.IntegrationCodeGraphWorkspaceStatus, ResourceID: "ws_1"},
			values: map[capability.ID]any{capability.IntegrationCodeGraphWorkspaceStatus: map[string]any{"initialized": true}},
			want:   capability.IntegrationCodeGraphWorkspaceStatus,
			text:   "CodeGraph workspace",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dispatcher := &domainTestDispatcher{values: test.values}
			ui, owner := newDomainTestInterface(t, dispatcher)
			screen, err := ui.renderState(t.Context(), owner, test.state)
			if err != nil {
				t.Fatal(err)
			}
			if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != test.want {
				t.Fatalf("canonical dispatch=%#v", dispatcher.calls)
			}
			if !strings.Contains(RichFallback(screen.Rich).Text, test.text) {
				t.Fatalf("screen missing %q: %q", test.text, RichFallback(screen.Rich).Text)
			}
		})
	}
}

func TestRestoredTelegramMutationActionsRemainReachable(t *testing.T) {
	grant := approval.Request{
		ID: "req_grant", Status: approval.StatusApproved, WorkspaceID: "ws_1", TargetTool: "run_command",
		RuntimeSessionGrant: true, GrantExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.RequestGrantList: []approval.Request{grant},
		capability.ProcessView: shellruntime.ProcessInfo{
			ID: "proc_1", Command: "echo ok", CWD: "/workspace", StartedAt: time.Now().UTC().Format(time.RFC3339), Running: false,
		},
		capability.IntegrationCodeGraphWorkspaceStatus: map[string]any{"initialized": true},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)

	grantScreen, err := ui.grantDetailScreen(t.Context(), owner, ActionState{Route: RouteGrant, Back: RouteGrants, ResourceID: grant.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramScreenOperations(t, ui, owner, grantScreen, capability.RequestGrantRevoke)

	processScreen, err := ui.processDetailScreen(t.Context(), owner, ActionState{Route: RouteProcess, Back: RouteProcesses, ResourceID: "proc_1", ParentID: "ws_1"})
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramScreenOperations(t, ui, owner, processScreen, capability.ProcessClear)

	codeGraphScreen, err := ui.codeGraphWorkspaceScreen(t.Context(), owner, ActionState{Route: RouteCodeGraphWS, Back: RouteWorkspace, ResourceID: "ws_1"})
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramScreenOperations(t, ui, owner, codeGraphScreen, capability.IntegrationCodeGraphWorkspaceInit, capability.IntegrationCodeGraphWorkspaceSync)
}

func TestTelegramNavigationKeepsRequiredActionsInsideButtonBudgets(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.WorkspaceShow: application.WorkspaceView{ID: "ws_1", Path: "/workspace", Available: true},
		capability.TunnelList:    []tunnel.Metadata{},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)

	workspace, err := ui.workspaceDetailScreen(t.Context(), owner, ActionState{Route: RouteWorkspace, Back: RouteWorkspaces, ResourceID: "ws_1"})
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramScreenOperations(t, ui, owner, workspace, capability.ProcessList, capability.IntegrationCodeGraphWorkspaceStatus)

	tunnels, err := ui.managedTunnelListScreen(t.Context(), owner, ActionState{Route: RouteManagedTunnels, Back: RouteNetwork})
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramScreenOperations(t, ui, owner, tunnels, capability.TunnelCreate, capability.TunnelAdminKeyStatus, capability.TunnelConfigRead)

	configTools, err := ui.configToolsScreen(owner, ActionState{Route: RouteSettings, Back: RouteHome, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	assertTelegramScreenOperations(t, ui, owner, configTools,
		capability.ConfigPatch, capability.ConfigExport, capability.ConfigSnapshotRead,
		capability.ConfigVerify, capability.ConfigPath, capability.NotificationStatus,
	)
}

func TestSettingsPaginationKeepsResourcesAndPageControlsVisible(t *testing.T) {
	items := make([]application.SettingResult, 0, 7)
	for index := 0; index < 7; index++ {
		key := fmt.Sprintf("test.setting.%d", index)
		items = append(items, application.SettingResult{Spec: config.FieldSpec{Key: key, Label: key}, Value: "value"})
	}
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	screen, err := ui.settingListScreen(owner, ActionState{Route: RouteSettings, Back: RouteHome}, items, "Settings", "")
	if err != nil {
		t.Fatal(err)
	}
	labels := telegramButtonLabels(screen)
	for _, want := range []string{"test.setting.0", "test.setting.1", "test.setting.2", "Older", "Config tools"} {
		if !labels[want] {
			t.Fatalf("required setting navigation button %q missing: %#v", want, labels)
		}
	}
}

func TestUpstreamOAuthIsDiscoverableAndAuthorizationResultIsTokenFree(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.UpstreamServerShow: upstream.Server{
			ID: "up_1", Name: "Upstream", Transport: "http", URL: "https://mcp.example.test",
			Auth: upstream.AuthConfig{Type: "oauth"},
		},
	}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.upstreamDetailScreen(t.Context(), owner, ActionState{Route: RouteUpstream, Back: RouteUpstreams, ResourceID: "up_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].Operation != capability.UpstreamServerShow {
		t.Fatalf("upstream canonical dispatch=%#v", dispatcher.calls)
	}

	assertTelegramScreenOperations(t, ui, owner, screen, capability.UpstreamAuthStatus, capability.UpstreamAuthLogin, capability.UpstreamAuthLogout)

	const rawToken = "oauth-access-token-must-not-render"
	spec, _ := capability.Lookup(capability.UpstreamAuthLogin)
	result, handled, err := ui.networkOperationResultScreen(owner, ActionState{
		Route: RouteOperation, Back: RouteUpstream, Operation: capability.UpstreamAuthLogin, ResourceID: "up_1",
	}, spec, mcpoauth.FlowSession{
		ID: rawToken, AuthorizationURL: "https://auth.example.test/authorize?client_id=codemcp",
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	fallback := RichFallback(result.Rich).Text
	if strings.Contains(fallback, rawToken) {
		t.Fatalf("OAuth session identifier leaked into Telegram message: %q", fallback)
	}
	foundURL := false
	for _, row := range result.Keyboard {
		for _, button := range row {
			if button.URL == "https://auth.example.test/authorize?client_id=codemcp" {
				foundURL = true
			}
		}
	}
	if !foundURL {
		t.Fatalf("OAuth authorization URL action missing: %#v", result.Keyboard)
	}
}

func TestLogsScreenSeparatesSessionClearFromPersistedJournalClear(t *testing.T) {
	ui, owner := newDomainTestInterface(t, &domainTestDispatcher{})
	screen, err := ui.logsMiniAppScreen(owner)
	if err != nil {
		t.Fatal(err)
	}
	fallback := RichFallback(screen.Rich).Text
	if !strings.Contains(fallback, "Clear view") || !strings.Contains(fallback, "Clear persisted logs") {
		t.Fatalf("clear semantics are not explicit: %q", fallback)
	}
	assertTelegramScreenOperations(t, ui, owner, screen, capability.LogsPath, capability.LogsClear)
}

func TestAuthRotationUsesProtectedPresentationAndOmitsCredentialFromOrdinaryScreen(t *testing.T) {
	api := &settingsTestAPI{}
	runtime := &Runtime{api: api, generation: 7, health: Health{Running: true, Enabled: true, AuthorizationConfigured: true}}
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime, Dispatcher: &domainTestDispatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 7}
	const token = "generated-auth-token-secret"
	screen, handled, err := ui.settingsOperationResultScreen(t.Context(), owner, ActionState{
		Route: RouteOperation, Back: RouteAuth, Operation: capability.AuthMCPRotate,
	}, capability.Spec{}, application.AuthRotationResult{Token: token})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(api.richOptions) != 1 || !api.richOptions[0].ProtectContent {
		t.Fatalf("rotation secret options=%#v", api.richOptions)
	}
	if strings.Contains(RichFallback(screen.Rich).Text, token) {
		t.Fatalf("ordinary rotation result leaked generated credential")
	}
}

func assertTelegramScreenOperations(t *testing.T, ui *Interface, owner ViewOwner, screen Screen, wants ...capability.ID) {
	t.Helper()
	operations := map[capability.ID]bool{}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			if button.CallbackData == "" {
				continue
			}
			ref, err := ui.callbacks.Decode(button.CallbackData)
			if err != nil {
				t.Fatal(err)
			}
			value, err := ui.states.Get(ref.Token, owner)
			if err != nil {
				t.Fatal(err)
			}
			if state, ok := value.(ActionState); ok && state.Operation != "" {
				operations[state.Operation] = true
			}
		}
	}
	for _, want := range wants {
		if !operations[want] {
			t.Fatalf("operation %s is not discoverable from Telegram screen", want)
		}
	}
}

func telegramButtonLabels(screen Screen) map[string]bool {
	labels := map[string]bool{}
	for _, row := range screen.Keyboard {
		for _, button := range row {
			labels[button.Text] = true
		}
	}
	return labels
}
