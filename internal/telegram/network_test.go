package telegram

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/integrations/cftunnel"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestCFTunnelScreenExposesManagedAssetLifecycleWithoutSecondTunnelAuthority(t *testing.T) {
	status := cftunnel.Status{
		Version: "v0.0.1", Platform: "linux/amd64", Source: cftunnel.SourceUnavailable,
		ManagedSupported: true, Consumer: "Telegram Logs Mini App",
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.IntegrationCFStatus: status}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.integrationScreen(t.Context(), owner, ActionState{Route: RouteIntegration, Back: RouteIntegrations, ResourceID: "cf"})
	if err != nil {
		t.Fatal(err)
	}
	text := RichFallback(screen.Rich).Text
	for _, want := range []string{"Cloudflare Quick Tunnel", "v0.0.1", "Telegram Logs Mini App", "Secure MCP Tunnel remains separate"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cf-tunnel screen missing %q: %q", want, text)
		}
	}
	labels := keyboardLabels(screen.Keyboard)
	for _, want := range []string{"Probe", "Install managed", "Back", "Home"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("cf-tunnel keyboard missing %q: %s", want, labels)
		}
	}
	if strings.Contains(labels, "Remove managed") || strings.Contains(labels, "Update managed") {
		t.Fatalf("unavailable cf-tunnel exposed managed-only actions: %s", labels)
	}
}

func TestCFTunnelScreenExposesUpdateAndManagedOnlyRemoveWhenInstalled(t *testing.T) {
	status := cftunnel.Status{
		Version: "v0.0.1", Platform: "linux/amd64", Source: cftunnel.SourceManaged, Path: "/managed/cf-tunnel",
		Verified: true, ManagedSupported: true, ManagedInstalled: true, Consumer: "Telegram Logs Mini App",
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.IntegrationCFStatus: status}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.integrationScreen(t.Context(), owner, ActionState{Route: RouteIntegration, Back: RouteIntegrations, ResourceID: "cf"})
	if err != nil {
		t.Fatal(err)
	}
	labels := keyboardLabels(screen.Keyboard)
	for _, want := range []string{"Probe", "Update managed", "Remove managed"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("managed cf-tunnel keyboard missing %q: %s", want, labels)
		}
	}
	if strings.Contains(labels, "Install managed") {
		t.Fatalf("managed cf-tunnel unexpectedly exposes Install: %s", labels)
	}
}

func TestTunnelScreenUsesSingleSafeProfileAndKeepsAllKeyControlsReachable(t *testing.T) {
	view := application.TunnelView{
		Enabled: true, Configured: true, ID: "tunnel_one", RuntimeKeyConfigured: true, RuntimeKeyPreview: "run********cret",
		Status: tunnel.Status{Enabled: true, Running: true, Ready: true, ID: "tunnel_one"},
		Admin: application.TunnelAdminStatus{
			Enabled: true, KeyConfigured: true, KeyPreview: "adm********cret", Configured: true, Verified: true,
			Scope:  tunnel.AdminScope{WorkspaceID: "ws_one"},
			Access: tunnel.AdminAccess{Read: true, Manage: true},
		},
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.TunnelStatus: view}}
	ui, owner := newDomainTestInterface(t, dispatcher)
	screen, err := ui.tunnelScreen(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	fallback := RichFallback(screen.Rich).Text
	for _, want := range []string{"OpenAI Secure MCP Tunnel", "tunnel_one", "workspace:ws_one"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("tunnel screen missing %q: %q", want, fallback)
		}
	}
	if len(screen.Keyboard) != 4 {
		t.Fatalf("tunnel keyboard rows=%#v", screen.Keyboard)
	}
	labels := keyboardLabels(screen.Keyboard)
	for _, want := range []string{"Disable", "Sync metadata", "Runtime config", "Set admin scope", "Disable admin", "Set admin key", "Verify admin", "Remove admin key", "Back", "Home"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("tunnel keyboard missing %q: %s", want, labels)
		}
	}
}

func TestUpstreamDetailIsRedactedProgressiveAndMutationStateIsVersionBound(t *testing.T) {
	server := upstream.Server{
		ID: "prod", Name: "Prod", Enabled: true, Transport: "http",
		URL:     "https://example.com/mcp?token=url-secret",
		Headers: map[string]string{"Authorization": "Bearer header-secret", "X-Region": "us"},
		Env:     map[string]string{"API_KEY": "env-secret", "MODE": "prod"},
		Auth:    upstream.AuthConfig{Type: "auto"}, Expose: "all", ToolPrefix: "prod",
	}
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{capability.UpstreamServerShow: server}}
	ui, owner := newDomainTestInterface(t, dispatcher)

	compact, err := ui.upstreamDetailScreen(t.Context(), owner, ActionState{Route: RouteUpstream, ResourceID: server.ID})
	if err != nil {
		t.Fatal(err)
	}
	compactText := RichFallback(compact.Rich).Text
	if strings.Contains(compactText, "Configuration") || strings.Contains(compactText, "header-secret") || strings.Contains(compactText, "env-secret") {
		t.Fatalf("compact upstream screen leaked detail: %q", compactText)
	}

	detail, err := ui.upstreamDetailScreen(t.Context(), owner, ActionState{Route: RouteUpstream, ResourceID: server.ID, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	detailText := RichFallback(detail.Rich).Text
	for _, secret := range []string{"header-secret", "env-secret", "url-secret"} {
		if strings.Contains(detailText, secret) {
			t.Fatalf("upstream detail leaked %q: %q", secret, detailText)
		}
	}
	for _, want := range []string{"Authorization", "API_KEY", "Configuration"} {
		if !strings.Contains(detailText, want) {
			t.Fatalf("upstream detail missing safe metadata %q: %q", want, detailText)
		}
	}
	if len(detail.Keyboard) != 4 || detail.Keyboard[3][0].Text != "« Back" || detail.Keyboard[3][1].Text != "⌂ Home" {
		t.Fatalf("upstream keyboard navigation=%#v", detail.Keyboard)
	}
	expected := application.UpstreamFingerprint(server)
	foundBoundMutation := false
	for _, row := range detail.Keyboard {
		for _, button := range row {
			if button.Text != "Remove" {
				continue
			}
			ref, decodeErr := ui.callbacks.Decode(button.CallbackData)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			value, stateErr := ui.states.Get(ref.Token, owner)
			if stateErr != nil {
				t.Fatal(stateErr)
			}
			state := value.(ActionState)
			input := state.Input.(application.UpstreamIDInput)
			foundBoundMutation = input.ExpectedFingerprint == expected
		}
	}
	if !foundBoundMutation {
		t.Fatal("upstream mutation callback was not bound to the rendered resource version")
	}
}

func TestTunnelRuntimeInputFlowProtectsCredentialAndBuildsTypedInput(t *testing.T) {
	dispatcher := &domainTestDispatcher{values: map[capability.ID]any{
		capability.TunnelStatus: application.TunnelView{
			Enabled: true, ID: "tunnel_old", OrganizationID: "org_old", RuntimeKeyConfigured: true,
		},
	}}
	ui, _ := newDomainTestInterface(t, dispatcher)
	descriptor, handled, err := ui.networkInputFlow(t.Context(), ActionState{InputKind: inputTunnelConfigure})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	var keyField *inputFlowField
	for index := range descriptor.Fields {
		if descriptor.Fields[index].Key == "api_key" {
			keyField = &descriptor.Fields[index]
			break
		}
	}
	if keyField == nil || !keyField.Secret || keyField.Kind != inputFlowSecret {
		t.Fatalf("runtime key field=%#v", keyField)
	}
	flow := newInputFlowState(descriptor)
	flow.Values["id"] = "tunnel_x"
	flow.Values["organization_id"] = "org_x"
	flow.Values["api_key"] = "runtime-key"
	value, err := descriptor.Build(inputFlowDataFor(descriptor, flow))
	if err != nil {
		t.Fatal(err)
	}
	input := value.(application.TunnelConfigureInput)
	if input.Runtime == nil || input.Runtime.APIKey == nil || *input.Runtime.APIKey != "runtime-key" || input.Runtime.OrganizationID == nil || *input.Runtime.OrganizationID != "org_x" {
		t.Fatalf("tunnel runtime input=%#v", input.Runtime)
	}
}

func keyboardLabels(rows [][]Button) string {
	labels := []string{}
	for _, row := range rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	return strings.Join(labels, "|")
}
