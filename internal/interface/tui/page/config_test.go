package page

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/interface/tui/testutil"
)

func TestConfigPageLoadsAndNeverRendersSecrets(t *testing.T) {
	root := prepareConfigPageRoot(t)
	page, err := NewConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cmd := page.Init()
	if cmd == nil {
		t.Fatal("config page init returned no load command")
	}
	updated, _ := page.Update(cmd())
	page = updated.(*ConfigPage)
	if !page.loaded || page.overview.Root != root || page.overview.Source.Format != configformat.JSON {
		t.Fatalf("overview=%#v loaded=%t", page.overview, page.loaded)
	}
	page.overview.Config.HTTP.MCP.Auth.TokenHash = "MCP_HASH_SECRET"
	page.overview.Config.HTTP.Admin.Auth.TokenHash = "ADMIN_HASH_SECRET"
	page.overview.Config.Tunnel.APIKey = "TUNNEL_RUNTIME_SECRET"
	page.overview.Config.Tunnel.Admin.Key = "TUNNEL_ADMIN_SECRET"
	page.rebuildBrowser("")
	view := page.View(100, 32)
	rows := page.configRows()
	var all strings.Builder
	for _, row := range rows {
		all.WriteString(row.ID + " " + row.Title + " " + row.Description + " " + row.Meta + "\n")
	}
	model := all.String()
	for _, secret := range []string{"MCP_HASH_SECRET", "ADMIN_HASH_SECRET", "TUNNEL_RUNTIME_SECRET", "TUNNEL_ADMIN_SECRET"} {
		if strings.Contains(view, secret) || strings.Contains(model, secret) {
			t.Fatalf("config page leaked %s", secret)
		}
	}
	for _, want := range []string{"Runtime & Network", "Access & Security", "Shell & Execution", "Integrations", "Tunnel", "Storage & Maintenance", "configured"} {
		if !strings.Contains(model, want) {
			t.Fatalf("config rows missing %q: %q", want, model)
		}
	}
}

func TestConfigDashboardContainsExactlySixDomains(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	rows := page.configRows()
	if len(rows) != 6 {
		t.Fatalf("domains=%d rows=%#v", len(rows), rows)
	}
	for _, row := range rows {
		if strings.Contains(row.ID, ".") {
			t.Fatalf("flat config field leaked into dashboard: %#v", row)
		}
	}
}

func TestConfigIntegrationsDomainCoversAllFirstPartyIntegrations(t *testing.T) {
	prepareConfigPageRoot(t)
	page, err := NewConfigRoute(t.Context(), "integrations")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	view := ansi.Strip(page.View(110, 38))
	for _, want := range []string{"Ponytail active", "Caveman active", "RTK enabled", "RTK executable", "CodeGraph enabled", "CodeGraph executable"} {
		if !strings.Contains(view, want) {
			t.Fatalf("integrations domain missing %q: %q", want, view)
		}
	}
}

func TestConfigPageTitleStartsAtWorkspaceTitlePosition(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	lines := strings.Split(ansi.Strip(page.View(100, 32)), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "Configuration") || strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("config title lines=%q", lines[:min(2, len(lines))])
	}
}

func TestConfigResourceUsesFullChildDetailPage(t *testing.T) {
	prepareConfigPageRoot(t)
	page, err := NewConfigRoute(t.Context(), "http.mcp.port")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	if page.OverlayActive() {
		t.Fatal("config detail incorrectly reports overlay active")
	}
	view := ansi.Strip(page.View(100, 28))
	for _, want := range []string{"http.mcp.port", "Value", "Default", "State", "default", "sets the TCP port for the MCP HTTP server", "e edit", "r refresh"} {
		if !strings.Contains(view, want) {
			t.Fatalf("config detail missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "MCP HTTP port") {
		t.Fatalf("config child repeated breadcrumb title: %q", view)
	}
	if strings.Contains(view, "╭") {
		t.Fatalf("config detail retained modal chrome: %q", view)
	}
	_, cmd := page.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmd == nil {
		t.Fatal("config edit detail action returned no command")
	}
	message, ok := cmd().(ConfigCommandMsg)
	if !ok || message.Command != ConfigEdit || message.ResourceID != "http.mcp.port" {
		t.Fatalf("config edit action=%#v", message)
	}
}

func TestConfigReadOnlyResourceHidesEditAction(t *testing.T) {
	prepareConfigPageRoot(t)
	page, err := NewConfigRoute(t.Context(), "http.mcp.auth.token_hash")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	view := ansi.Strip(page.View(100, 28))
	if !strings.Contains(view, "managed") || !strings.Contains(view, "Guidance") || !strings.Contains(view, "r refresh") || strings.Contains(view, "e edit") || strings.Contains(view, "MCP credential") {
		t.Fatalf("read-only config detail=%q", view)
	}
}

func TestConfigFieldDetailNeverRendersSecretOrHash(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfigRoute(t.Context(), "http.mcp.auth.token_hash")
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	page.overview.Config.HTTP.MCP.Auth.TokenHash = "FIELD_DETAIL_SECRET"
	page.syncDetail()
	view := ansi.Strip(page.View(100, 30))
	if strings.Contains(view, "FIELD_DETAIL_SECRET") {
		t.Fatalf("field detail leaked secret: %q", view)
	}
	for _, want := range []string{"configured", "managed", "Guidance", "auth token workflow"} {
		if !strings.Contains(view, want) {
			t.Fatalf("field detail missing %q: %q", want, view)
		}
	}
}

func TestConfigBrowserOpenNavigatesToFieldChild(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	_, cmd := page.Update(component.BrowserOpenMsg{Row: component.Row{ID: "runtime"}})
	if cmd == nil {
		t.Fatal("config browser open returned no navigation command")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "config/runtime" {
		t.Fatalf("config navigation=%#v", message)
	}
}

func TestConfigDomainRowsAreSectionScopedAndShowState(t *testing.T) {
	prepareConfigPageRoot(t)
	page, err := NewConfigRoute(t.Context(), "shell")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	page.overview.Config.Shell.Path = []string{t.TempDir()}
	page.rebuildBrowser("")
	rows := page.configRows()
	if len(rows) == 0 {
		t.Fatal("shell domain has no rows")
	}
	foundPath := false
	for _, row := range rows {
		spec, ok := config.FieldByKey(row.ID)
		if !ok || spec.Section != config.FieldSectionShell {
			t.Fatalf("non-shell field in shell domain: %#v", row)
		}
		if row.ID == "shell.path" {
			foundPath = strings.Contains(row.Meta, "custom")
		}
	}
	if !foundPath {
		t.Fatalf("shell path row missing custom state: %#v", rows)
	}
	view := ansi.Strip(page.View(100, 30))
	if !strings.Contains(view, "Executable search paths") || !strings.Contains(view, "e edit") || !strings.Contains(view, "/ filter") || strings.Contains(view, "Shell & Execution") {
		t.Fatalf("shell domain view=%q", view)
	}
}

func TestConfigAccessDomainNeverRendersCredentialSecrets(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfigRoute(t.Context(), "access")
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	page.overview.Config.HTTP.MCP.Auth.TokenHash = "DOMAIN_MCP_SECRET"
	page.overview.Config.HTTP.Admin.Auth.TokenHash = "DOMAIN_ADMIN_SECRET"
	page.rebuildBrowser("")
	view := ansi.Strip(page.View(100, 30))
	if strings.Contains(view, "DOMAIN_MCP_SECRET") || strings.Contains(view, "DOMAIN_ADMIN_SECRET") {
		t.Fatalf("access domain leaked credential: %q", view)
	}
	if !strings.Contains(view, "configured") || !strings.Contains(view, "managed") {
		t.Fatalf("access domain missing managed credential state: %q", view)
	}
}

func TestConfigDomainOpenNavigatesToLegacyCompatibleFieldRoute(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfigRoute(t.Context(), "runtime")
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	_, cmd := page.Update(component.BrowserOpenMsg{Row: component.Row{ID: "http.mcp.port"}})
	if cmd == nil {
		t.Fatal("domain field open returned no navigation")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "config/http.mcp.port" {
		t.Fatalf("field navigation=%#v", message)
	}
}

func TestConfigStoragePageCentralizesMaintenanceActions(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfigRoute(t.Context(), "storage")
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	rows := page.configRows()
	if len(rows) != 5 {
		t.Fatalf("maintenance actions=%d rows=%#v", len(rows), rows)
	}
	wantIDs := []string{"verify", "migrate", "migrate-secrets", "export", "import"}
	for index, want := range wantIDs {
		if rows[index].ID != want {
			t.Fatalf("maintenance row %d=%q want=%q", index, rows[index].ID, want)
		}
	}
	view := ansi.Strip(page.View(100, 34))
	for _, want := range []string{"Format", "Config", "Root", "Initialized", "Runtime sync", "Persisted", "Runtime", "enter run"} {
		if !strings.Contains(view, want) {
			t.Fatalf("storage view missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "Storage & Maintenance") {
		t.Fatalf("storage child repeated breadcrumb title: %q", view)
	}
}

func TestConfigRuntimeSyncStatusRendersPendingAndFingerprints(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfigRoute(t.Context(), "storage")
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	page.overview.RuntimeRunning = true
	page.overview.RuntimeSync = application.ConfigRuntimeSync{State: application.ConfigRuntimePending, PersistedFingerprint: "1234567890abcdef", RuntimeFingerprint: "fedcba0987654321"}
	view := ansi.Strip(page.View(100, 34))
	for _, want := range []string{"changes pending", "1234567890ab", "fedcba098765"} {
		if !strings.Contains(view, want) {
			t.Fatalf("runtime sync view missing %q: %q", want, view)
		}
	}
}

func TestConfigSaveMarksAutoReloadedRuntimeCurrentImmediately(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	page.overview.RuntimeRunning = true
	page.overview.RuntimeSync.State = application.ConfigRuntimeCurrent
	mutation := page.overview.Config
	mutation.HTTP.MCP.Port++
	page.finishOperation(configOperationMsg{command: ConfigEdit, mutation: application.ConfigMutationResult{Config: mutation, RuntimeReloaded: true}})
	if page.overview.RuntimeSync.State != application.ConfigRuntimeCurrent {
		t.Fatalf("sync state=%q", page.overview.RuntimeSync.State)
	}
	if page.overview.RuntimeSync.PersistedFingerprint == "" || page.overview.RuntimeSync.RuntimeFingerprint != page.overview.RuntimeSync.PersistedFingerprint {
		t.Fatalf("fingerprints persisted=%q runtime=%q", page.overview.RuntimeSync.PersistedFingerprint, page.overview.RuntimeSync.RuntimeFingerprint)
	}
}

func TestConfigStorageOpenRunsMaintenanceAction(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfigRoute(t.Context(), "storage")
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	_, cmd := page.Update(component.BrowserOpenMsg{Row: component.Row{ID: "export"}})
	if cmd == nil || page.overlay != configOverlayNone {
		t.Fatalf("export cmd=%v overlay=%d", cmd != nil, page.overlay)
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "config/storage/export" {
		t.Fatalf("export navigation=%#v", navigate)
	}
}

func TestConfigRootDoesNotExposeMaintenanceShortcuts(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	for _, key := range []string{"v", "m", "c", "x", "i"} {
		_, handled := page.handleKey(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
		if handled {
			t.Fatalf("root still handles maintenance shortcut %q", key)
		}
	}
}

func TestConfigGlobalSearchIndexesAllFieldsWithoutSecrets(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	page.overview.Config.Tunnel.APIKey = "SEARCH_RUNTIME_SECRET"
	page.overview.Config.Tunnel.Admin.Key = "SEARCH_ADMIN_SECRET"
	rows := page.searchRows()
	if len(rows) != len(config.Fields()) {
		t.Fatalf("search rows=%d fields=%d", len(rows), len(config.Fields()))
	}
	joined := fmt.Sprintf("%#v", rows)
	if strings.Contains(joined, "SEARCH_RUNTIME_SECRET") || strings.Contains(joined, "SEARCH_ADMIN_SECRET") {
		t.Fatalf("search index leaked secret: %s", joined)
	}
	found := false
	for _, row := range rows {
		if row.ID == "shell.path" {
			found = strings.Contains(row.Search, "Executable search paths") && strings.Contains(row.Search, "shell.path") && strings.Contains(row.Description, "Shell & Execution")
		}
	}
	if !found {
		t.Fatal("search index missing shell path metadata")
	}
}

func TestConfigSearchOpensAndNavigatesToField(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	_, handled := page.handleKey(tea.KeyPressMsg{Code: 's'})
	if !handled || !page.searching || !page.browser.InputActive() {
		t.Fatalf("search handled=%t searching=%t input=%t", handled, page.searching, page.browser.InputActive())
	}
	_, cmd := page.Update(component.BrowserOpenMsg{Row: component.Row{ID: "shell.path"}})
	if cmd == nil {
		t.Fatal("search result returned no navigation")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "config/shell.path" {
		t.Fatalf("search navigation=%#v", message)
	}
}

func TestConfigPageReadOnlyGuidance(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	if cmd, err := page.openCommand(ConfigEdit, "http.mcp.auth.token_hash"); err != nil || cmd != nil {
		t.Fatalf("read-only edit cmd=%v err=%v", cmd != nil, err)
	}
	if page.overlay != configOverlayNone || !strings.Contains(page.notice, "auth token") {
		t.Fatalf("overlay=%d notice=%q", page.overlay, page.notice)
	}
}

func TestConfigPageEditIsRoutedAndOperationFailureKeepsEditor(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	cmd, err := page.openCommand(ConfigEdit, "http.mcp.port")
	if err != nil || cmd == nil {
		t.Fatal(err)
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "config/http.mcp.port/edit" {
		t.Fatalf("edit navigation=%#v", navigate)
	}
	edit, err := NewConfigRouteAction(t.Context(), "http.mcp.port", "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	updated, initEditor := edit.Update(edit.Init()())
	edit = updated.(*ConfigPage)
	if edit.editor == nil || edit.fieldForm == nil || initEditor == nil || edit.OverlayActive() {
		t.Fatalf("editor=%v data=%v init=%v overlay=%t", edit.editor != nil, edit.fieldForm != nil, initEditor != nil, edit.OverlayActive())
	}
	edit.fieldForm.Raw = "draft-value"
	edit.editor.SetSubmitting(true)
	follow := edit.finishOperation(configOperationMsg{command: ConfigEdit, err: fmt.Errorf("save failed")})
	if follow != nil || edit.editor == nil || edit.fieldForm.Raw != "draft-value" || edit.editor.Submitting() || !strings.Contains(ansi.Strip(edit.View(52, 20)), "save failed") {
		t.Fatalf("failure follow=%v editor=%v draft=%q submitting=%t view=%q", follow != nil, edit.editor != nil, edit.fieldForm.Raw, edit.editor.Submitting(), ansi.Strip(edit.View(52, 20)))
	}
}

func TestConfigSuccessfulEditorSaveCommitsDraftBeforeNavigation(t *testing.T) {
	prepareConfigPageRoot(t)
	page, err := NewConfigRouteAction(t.Context(), "shell.path", "", "edit")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	page.fieldForm.Raw = "/opt/tools\n/usr/local/bin"
	if !page.Dirty() {
		t.Fatal("config editor was not dirty before save")
	}
	mutation := page.overview.Config
	mutation.Shell.Path = []string{"/opt/tools", "/usr/local/bin"}
	follow := page.finishOperation(configOperationMsg{command: ConfigEdit, mutation: application.ConfigMutationResult{Config: mutation}})
	if follow == nil || page.Dirty() {
		t.Fatalf("successful config save follow=%v dirty=%t", follow != nil, page.Dirty())
	}
}

func TestConfigPageCancellationIgnoresLateResult(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	cmd := page.startOperation(ConfigVerify, "Verifying", func(context.Context) configOperationMsg {
		return configOperationMsg{command: ConfigVerify, verify: config.VerifyResult{Format: configformat.JSON, Files: 99}}
	})
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	page = updated.(*ConfigPage)
	if page.overlay != configOverlayNone {
		t.Fatalf("overlay=%d", page.overlay)
	}
	updated, follow := page.Update(cmd())
	page = updated.(*ConfigPage)
	if follow != nil || !strings.Contains(page.notice, "cancellation requested") || strings.Contains(page.notice, "99") {
		t.Fatalf("follow=%v notice=%q", follow != nil, page.notice)
	}
}

func TestConfigPageOldOperationCannotOverwriteNewOperation(t *testing.T) {
	prepareConfigPageRoot(t)
	page, _ := NewConfig(t.Context())
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	old := page.startOperation(ConfigVerify, "Old", func(context.Context) configOperationMsg {
		return configOperationMsg{command: ConfigVerify, verify: config.VerifyResult{Format: configformat.JSON, Files: 1}}
	})
	page.cancelOperation()
	current := page.startOperation(ConfigVerify, "Current", func(context.Context) configOperationMsg {
		return configOperationMsg{command: ConfigVerify, verify: config.VerifyResult{Format: configformat.JSON, Files: 2}}
	})
	updated, staleFollow := page.Update(old())
	page = updated.(*ConfigPage)
	if staleFollow != nil || page.overlay != configOverlayOperation {
		t.Fatalf("stale follow=%v overlay=%d", staleFollow != nil, page.overlay)
	}
	updated, follow := page.Update(current())
	page = updated.(*ConfigPage)
	if follow == nil || !strings.Contains(page.notice, "2 structured files") {
		t.Fatalf("follow=%v notice=%q", follow != nil, page.notice)
	}
}

func TestConfigEditorsUseExplicitActionsPickerAndImportConfirmation(t *testing.T) {
	importEditor, importData := newConfigBundleEditor(false)
	if importData.Force || importData.Path != "codemcp-config.json" || importEditor.Init() == nil {
		t.Fatalf("import defaults=%#v init=%v", importData, importEditor.Init() != nil)
	}
	_, exportData := newConfigBundleEditor(true)
	if exportData.Force || exportData.Path != "codemcp-config.json" {
		t.Fatalf("export defaults=%#v", exportData)
	}
	prepareConfigPageRoot(t)
	page, err := NewConfigRouteAction(t.Context(), "", "storage", "import")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := page.Update(page.Init()())
	page = updated.(*ConfigPage)
	if page.editor == nil || page.bundleForm == nil {
		t.Fatalf("import editor=%v data=%v", page.editor != nil, page.bundleForm != nil)
	}
	bundle := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(bundle, []byte("bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	page.bundleForm.Path = bundle
	_, cmd := page.Update(component.EditorSubmitMsg{})
	if cmd != nil || page.overlay != configOverlayConfirm || page.editor == nil {
		t.Fatalf("import submit cmd=%v overlay=%d editor=%v", cmd != nil, page.overlay, page.editor != nil)
	}
	testutil.AssertLinesFit(t, page.View(40, 16), 40)
}

func TestConfigPatchAndTelegramSetupUseNativeProtectedEditors(t *testing.T) {
	prepareConfigPageRoot(t)
	patchPage, err := NewConfigRouteAction(t.Context(), "", "", "patch")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := patchPage.Update(patchPage.Init()())
	patchPage = updated.(*ConfigPage)
	if patchPage.editor == nil || patchPage.patchForm == nil {
		t.Fatalf("patch editor=%v form=%v", patchPage.editor != nil, patchPage.patchForm != nil)
	}
	patchPage.patchForm.Changes = `[{"Key":"http.mcp.enabled","Value":"false"}]`
	cmd := patchPage.submitConfigEditor()
	if cmd == nil || patchPage.overlay != configOverlayOperation || !patchPage.editor.Submitting() {
		t.Fatalf("patch command=%v overlay=%d submitting=%t", cmd != nil, patchPage.overlay, patchPage.editor.Submitting())
	}
	msg, ok := cmd().(configOperationMsg)
	if !ok || msg.err != nil || msg.command != ConfigPatch || len(msg.apply.Results) != 1 {
		t.Fatalf("patch result=%#v", msg)
	}

	telegramPage, err := NewConfigRouteAction(t.Context(), "", "", "telegram-setup")
	if err != nil {
		t.Fatal(err)
	}
	updated, editorInit := telegramPage.Update(telegramPage.Init()())
	telegramPage = updated.(*ConfigPage)
	if telegramPage.editor == nil || telegramPage.telegramForm == nil {
		t.Fatalf("telegram editor=%v form=%v", telegramPage.editor != nil, telegramPage.telegramForm != nil)
	}
	if editorInit != nil {
		updated, _ = telegramPage.Update(editorInit())
		telegramPage = updated.(*ConfigPage)
	}
	const token = "123456:raw-telegram-secret"
	for _, char := range token {
		updated, _ = telegramPage.Update(tea.KeyPressMsg{Code: char, Text: string(char)})
		telegramPage = updated.(*ConfigPage)
	}
	updated, _ = telegramPage.Update(component.FormMouseMsg{Group: 0, Field: 1, Click: true})
	telegramPage = updated.(*ConfigPage)
	for _, char := range "42" {
		updated, _ = telegramPage.Update(tea.KeyPressMsg{Code: char, Text: string(char)})
		telegramPage = updated.(*ConfigPage)
	}
	if telegramPage.telegramForm.Token != token || telegramPage.telegramForm.UserID != "42" {
		t.Fatalf("telegram form values token=%q user=%q", telegramPage.telegramForm.Token, telegramPage.telegramForm.UserID)
	}
	if rendered := ansi.Strip(telegramPage.View(72, 24)); strings.Contains(rendered, token) {
		t.Fatalf("telegram token leaked from protected editor: %q", rendered)
	}
	cmd = telegramPage.submitConfigEditor()
	if cmd == nil || telegramPage.overlay != configOverlayOperation || !telegramPage.editor.Submitting() {
		t.Fatalf("telegram command=%v overlay=%d submitting=%t", cmd != nil, telegramPage.overlay, telegramPage.editor.Submitting())
	}
	msg, ok = cmd().(configOperationMsg)
	if !ok || msg.err != nil || msg.command != ConfigTelegramSetup || len(msg.telegram.AuthorizedUsers) != 1 || msg.telegram.AuthorizedUsers[0] != 42 {
		t.Fatalf("telegram result=%#v", msg)
	}
	if encoded := fmt.Sprintf("%#v", msg); strings.Contains(encoded, token) {
		t.Fatalf("telegram setup result leaked raw token: %s", encoded)
	}
}

func prepareConfigPageRoot(t *testing.T) string {
	t.Helper()
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HTTP.MCP.Auth.TokenHash = "mcp-hash"
	cfg.HTTP.Admin.Auth.TokenHash = "admin-hash"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	overview, err := application.LoadConfigOverview(context.Background())
	if err != nil || overview.RuntimeRunning {
		t.Fatalf("overview=%#v err=%v", overview, err)
	}
	return root
}
