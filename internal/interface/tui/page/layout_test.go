package page

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBrowserPageHintsStayOnBottomRow(t *testing.T) {
	const width, height = 100, 24

	prepareConfigPageRoot(t)
	configPage, err := NewConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := configPage.Update(configPage.Init()())
	configPage = updated.(*ConfigPage)
	assertPageBottomHint(t, "config", configPage.View(width, height), height, "? more")
	configPage.notice = "Config saved"
	configView := configPage.View(width, height)
	assertPageBottomHint(t, "config with feedback", configView, height, "? more")
	assertPageTitleNotice(t, "config", configView, "Configuration", configPage.notice)

	logsPage, err := NewLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer logsPage.Close()
	assertPageBottomHint(t, "logs runtime", logsPage.View(width, height), height, "? more")
	logsPage.notice = "Reconnecting"
	logsView := logsPage.View(width, height)
	assertPageBottomHint(t, "logs runtime with feedback", logsView, height, "? more")
	assertPageTitleNotice(t, "logs", logsView, "Runtime", logsPage.notice)
	assertPageHeaderGap(t, "logs", logsView)
	logsPage.notice = ""
	execPage, err := NewCommandExecutionLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer execPage.Close()
	assertPageBottomHint(t, "logs command execution", execPage.View(width, height), height, "? more")

	runtimePage, err := NewRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	runtimePage.loaded = true
	runtimePage.rebuildBrowser("")
	assertPageBottomHint(t, "runtime", runtimePage.View(width, height), height, "r refresh")
	runtimePage.notice = "Runtime updated"
	runtimeView := runtimePage.View(width, height)
	assertPageBottomHint(t, "runtime with feedback", runtimeView, height, "r refresh")
	assertPageTitleNotice(t, "runtime", runtimeView, "Runtime & System", runtimePage.notice)

	instructionPage, _ := newTestInstructionPage(t)
	instructionView := instructionPage.View(width, height)
	assertPageHeaderGap(t, "instruction", instructionView)
	rulesPage, err := NewInstructionRoute(t.Context(), "rules")
	if err != nil {
		t.Fatal(err)
	}
	assertPageBottomHint(t, "instruction rules", rulesPage.View(width, height), height, "? more")

	workspacePage, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	workspaceView := workspacePage.View(width, height)
	assertSinglePageHeaderGap(t, "workspaces", workspaceView)
	assertPageBottomHint(t, "workspaces", workspaceView, height, "? more")

	requestsPage, err := NewRequests(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	requestsView := requestsPage.View(width, height)
	assertSinglePageHeaderGap(t, "requests", requestsView)
	assertPageBottomHint(t, "requests", requestsView, height, "? more")
}

func assertPageBottomHint(t *testing.T, name, view string, height int, marker string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) != height {
		t.Fatalf("%s height=%d want=%d view=%q", name, len(lines), height, ansi.Strip(view))
	}
	if !strings.Contains(lines[height-1], marker) {
		t.Fatalf("%s bottom row missing %q: %q", name, marker, lines[height-1])
	}
}

func assertPageTitleNotice(t *testing.T, name, view, title, notice string) {
	t.Helper()
	line := strings.Split(ansi.Strip(view), "\n")[0]
	if !strings.Contains(line, title) || !strings.Contains(line, "· "+notice) {
		t.Fatalf("%s title notice missing: %q", name, line)
	}
}

func assertPageHeaderGap(t *testing.T, name, view string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("%s missing empty row between header and content: %q", name, ansi.Strip(view))
	}
}

func assertSinglePageHeaderGap(t *testing.T, name, view string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[1]) != "" || strings.TrimSpace(lines[2]) == "" {
		t.Fatalf("%s must have exactly one empty row between header and content: %q", name, ansi.Strip(view))
	}
}
