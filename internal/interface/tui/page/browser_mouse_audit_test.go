package page

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/runtime/activity"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/upstream"
)

func assertBrowserRowTargetMatchesRenderedRow(t *testing.T, view string, row component.Row, targets []component.MouseTarget) {
	t.Helper()
	needle := strings.TrimSpace(row.Title)
	if needle == "" {
		needle = strings.TrimSpace(row.ID)
	}
	rowY := -1
	for index, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, needle) {
			rowY = index
			break
		}
	}
	if rowY < 0 {
		t.Fatalf("browser row %q not rendered: %q", needle, ansi.Strip(view))
	}
	for _, target := range targets {
		if target.ID != "browser.row" {
			continue
		}
		if target.Rect.Y != rowY {
			t.Fatalf("browser row target y=%d want rendered y=%d rect=%#v view=%q", target.Rect.Y, rowY, target.Rect, ansi.Strip(view))
		}
		return
	}
	t.Fatalf("browser row target missing for rendered row y=%d", rowY)
}

func TestBrowserOwnerHitboxesMatchRenderedRowsWithFeedback(t *testing.T) {
	t.Run("logs runtime", func(t *testing.T) {
		page, err := NewLogs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		page.mergeEvents([]runtimeevent.Event{{Sequence: 1, Time: time.Now(), RunID: "run_mouse", Level: "info", Name: "mouse.audit", Message: "runtime row"}})
		for _, feedback := range []error{nil, errors.New("logs feedback")} {
			page.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("logs runtime browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

	t.Run("logs execution", func(t *testing.T) {
		page, err := NewCommandExecutionLogs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		page.view = logsViewBrowser
		page.exec.events = []shellruntime.ExecutionFeedEvent{{Sequence: 1, ExecutionID: "exec_mouse", Type: shellruntime.ExecutionEventStarted, Execution: &shellruntime.ExecutionInfo{ID: "exec_mouse", Tool: "run_command", Command: "echo mouse"}}}
		page.rebuildExecutionBrowser()
		for _, feedback := range []error{nil, errors.New("execution feedback")} {
			page.exec.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("execution browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

	t.Run("logs tool calls", func(t *testing.T) {
		page, err := NewToolCallLogsRoute(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer page.Close()
		page.view = logsViewBrowser
		page.tools.events = []activity.Event{{Sequence: 1, CallID: "call_mouse", Tool: "read_file", Status: "ok", Timestamp: time.Now()}}
		page.rebuildToolCallBrowser()
		for _, feedback := range []error{nil, errors.New("tool feedback")} {
			page.tools.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("tool call browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

	t.Run("runtime", func(t *testing.T) {
		page, err := NewRuntime(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		page.loaded = true
		_ = page.rebuildBrowser("")
		for _, feedback := range []error{nil, errors.New("runtime feedback")} {
			page.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("runtime browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

	t.Run("workspace", func(t *testing.T) {
		prepareConfigPageRoot(t)
		page, err := NewWorkspaces(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		project := filepath.Join(t.TempDir(), "workspace")
		if err := os.MkdirAll(project, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := page.manager.Register(project); err != nil {
			t.Fatal(err)
		}
		if err := page.reload(); err != nil {
			t.Fatal(err)
		}
		for _, feedback := range []error{nil, errors.New("workspace feedback")} {
			page.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("workspace browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

	t.Run("config", func(t *testing.T) {
		prepareConfigPageRoot(t)
		page, err := NewConfig(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		updated, _ := page.Update(page.Init()())
		page = updated.(*ConfigPage)
		for _, feedback := range []error{nil, errors.New("config feedback")} {
			page.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("config browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

	t.Run("tunnel", func(t *testing.T) {
		prepareConfigPageRoot(t)
		page, err := NewTunnelDashboard(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_ = page.View(100, 30)
		for _, target := range page.MouseTargets(0, 0, 10) {
			if target.ID == "browser.row" {
				t.Fatal("active single-tunnel page exposed stale managed-tunnel Browser row target")
			}
		}
	})

	t.Run("mcp", func(t *testing.T) {
		page, manager, _, _ := newMCPPageTestHarness(t, &mcpPageClient{})
		if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs", Enabled: true, Transport: "http", URL: "https://example.test/mcp", Expose: "all"}); err != nil {
			t.Fatal(err)
		}
		if err := page.reload(); err != nil {
			t.Fatal(err)
		}
		for _, feedback := range []error{nil, errors.New("mcp feedback")} {
			page.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("MCP browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

	t.Run("requests", func(t *testing.T) {
		page, err := NewRequests(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		request := approval.Request{ID: "req_mouse", Status: approval.StatusPending, Title: "Allow mouse audit", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
		page.requests = []approval.Request{request}
		page.rebuildBrowser(request.ID)
		for _, feedback := range []error{nil, errors.New("request feedback")} {
			page.err = feedback
			view := page.View(100, 30)
			row, ok := page.browser.Selected()
			if !ok {
				t.Fatal("request browser has no selected row")
			}
			assertBrowserRowTargetMatchesRenderedRow(t, view, row, page.MouseTargets(0, 0, 10))
		}
	})

}
