package page

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/interface/tui/testutil"
	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestMigratedEditorsResponsiveMatrix(t *testing.T) {
	type editorCase struct {
		name string
		make func() component.Editor
	}
	cases := []editorCase{
		{name: "workspace-context", make: func() component.Editor {
			editor, _ := newWorkspaceContextEditor(projectcontext.DefaultOptions())
			return editor
		}},
		{name: "mcp-server", make: func() component.Editor { editor, _ := newMCPServerEditor(upstream.Server{}, true); return editor }},
		{name: "mcp-oauth", make: func() component.Editor { editor, _ := newMCPOAuthEditor(); return editor }},
		{name: "tunnel-runtime", make: func() component.Editor {
			editor, _ := newTunnelRuntimeEditor(application.TunnelDashboard{})
			return editor
		}},
		{name: "tunnel-admin", make: func() component.Editor {
			editor, _ := newTunnelAdminEditor(application.TunnelAdminStatus{})
			return editor
		}},
		{name: "managed-tunnel", make: func() component.Editor { editor, _ := newManagedTunnelEditor(tunnel.Metadata{}, true); return editor }},
		{name: "managed-configure", make: func() component.Editor { editor, _ := newManagedConfigureEditor(false); return editor }},
		{name: "config-import", make: func() component.Editor { editor, _ := newConfigBundleEditor(false); return editor }},
		{name: "config-export", make: func() component.Editor { editor, _ := newConfigBundleEditor(true); return editor }},
		{name: "runtime-install", make: func() component.Editor { editor, _ := newInstallEditor(); return editor }},
		{name: "runtime-update", make: func() component.Editor { editor, _ := newUpdateEditor(); return editor }},
		{name: "request-create", make: func() component.Editor { editor, _ := newRequestCreateEditor(); return editor }},
		{name: "request-resolve", make: func() component.Editor {
			editor, _ := newRequestResolveEditor(approval.Request{ID: "req_responsive", TargetTool: "run_command"}, true)
			return editor
		}},
		{name: "logs-filter", make: func() component.Editor {
			editor, _ := newLogsFilterEditor(application.LogsQueryOptions{}, logger.VisibilityDefault)
			return editor
		}},
	}
	sizes := [][2]int{{24, 10}, {40, 16}, {60, 20}, {80, 24}, {100, 30}, {120, 40}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, size := range sizes {
				editor := test.make()
				editor.Resize(size[0], size[1])
				view := editor.View()
				testutil.AssertLinesFit(t, view, size[0])
				if got := lipgloss.Height(view); got > size[1] {
					t.Fatalf("size=%dx%d height=%d want <=%d", size[0], size[1], got, size[1])
				}
			}
		})
	}
}

func TestMajorPagesResponsiveMatrixBeforeRootFrame(t *testing.T) {
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	if err := configformat.SetRootPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	type pageCase struct {
		name string
		make func() (Model, error)
	}
	cases := []pageCase{
		{name: "workspaces", make: func() (Model, error) { return NewWorkspaces(ctx, "") }},
		{name: "mcp", make: func() (Model, error) { return NewMCP(ctx, "") }},
		{name: "tunnel", make: func() (Model, error) { return NewTunnelDashboard(ctx) }},
		{name: "requests", make: func() (Model, error) { return NewRequests(ctx, "") }},
		{name: "logs", make: func() (Model, error) { return NewLogs(ctx) }},
		{name: "config", make: func() (Model, error) { return NewConfig(ctx) }},
		{name: "instruction", make: func() (Model, error) { return NewInstruction(ctx) }},
		{name: "runtime", make: func() (Model, error) { return NewRuntime(ctx) }},
		{name: "about", make: func() (Model, error) { return NewAbout(ctx) }},
	}
	sizes := [][2]int{{24, 10}, {40, 16}, {60, 20}, {80, 24}, {100, 30}, {120, 40}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			page, err := test.make()
			if err != nil {
				t.Fatal(err)
			}
			for _, size := range sizes {
				updated, _ := page.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				page = updated
				view := page.View(size[0], size[1])
				testutil.AssertLinesFit(t, view, size[0])
				if got := lipgloss.Height(view); got > size[1] {
					t.Fatalf("size=%dx%d height=%d want <=%d", size[0], size[1], got, size[1])
				}
			}
		})
	}
}
