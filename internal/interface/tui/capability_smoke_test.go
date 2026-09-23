package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/interface/tui/action"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
)

func TestCapabilitySmokeReadNavigation(t *testing.T) {
	model := NewModel(Route{Kind: RouteHome})
	cmd, err := model.actions.Execute(t.Context(), "app.go.about", action.Context{Route: string(RouteHome)})
	if err != nil || cmd == nil {
		t.Fatalf("execute err=%v cmd=%v", err, cmd)
	}
	updated, _ := model.Update(cmd())
	model = updated.(Model)
	if model.router.Current().Kind != RouteAbout || model.currentPage == nil {
		t.Fatalf("route=%s page=%T", model.router.Current().Kind, model.currentPage)
	}
}

func TestCapabilitySmokeWriteThroughActionPageAndApplication(t *testing.T) {
	withCapabilityTestRoot(t)
	if _, err := application.Initialize(application.InitOptions{Format: configformat.JSON, FormatSelected: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.SetConfigField(t.Context(), "server.allow_unauthenticated_loopback", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := application.SetAuthEnabled(t.Context(), "mcp", false); err != nil {
		t.Fatal(err)
	}
	model := NewModel(Route{Kind: RouteRuntime})
	cmd, err := model.actions.Execute(t.Context(), "auth.mcp.enable", action.Context{Route: string(RouteRuntime)})
	if err != nil || cmd == nil {
		t.Fatalf("execute err=%v cmd=%v", err, cmd)
	}
	updated, operation := model.Update(cmd())
	model = updated.(Model)
	if operation == nil {
		t.Fatal("write action produced no operation")
	}
	queue := []tea.Cmd{operation}
	for steps := 0; steps < 16 && len(queue) > 0; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		message := next()
		if batch, ok := message.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		updated, follow := model.Update(message)
		model = updated.(Model)
		if follow != nil {
			queue = append(queue, follow)
		}
		if loaded, err := config.Load(); err == nil && loaded.Auth.MCPEnabled {
			return
		}
	}
	loaded, err := config.Load()
	if err != nil || !loaded.Auth.MCPEnabled {
		t.Fatalf("auth enabled=%t err=%v", loaded.Auth.MCPEnabled, err)
	}
}

func TestCapabilitySmokeDestructiveClearRequiresConfirmAndMutatesJournal(t *testing.T) {
	root := withCapabilityTestRoot(t)
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Sequence: 1, Time: time.Now(), RunID: "run", Level: "info", Name: "smoke", Message: "smoke"}); err != nil {
		t.Fatal(err)
	}
	model := NewModel(Route{Kind: RouteLogs})
	cmd, err := model.actions.Execute(t.Context(), "logs.clear", action.Context{Route: string(RouteLogs)})
	if err != nil || cmd == nil {
		t.Fatalf("execute err=%v cmd=%v", err, cmd)
	}
	updated, follow := model.Update(cmd())
	model = updated.(Model)
	if follow != nil {
		t.Fatalf("clear should wait for confirmation, got cmd=%v", follow)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model = updated.(Model)
	updated, clearCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if clearCmd == nil {
		t.Fatal("confirmed clear produced no operation")
	}
	_ = clearCmd()
	info, err := application.LoadLogsInfo()
	if err != nil || info.Files != 0 {
		t.Fatalf("logs info=%#v err=%v", info, err)
	}
}

func withCapabilityTestRoot(t *testing.T) string {
	t.Helper()
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	return root
}
