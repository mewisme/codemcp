package page

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/interface/tui/testutil"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/rules"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/skills"
)

func TestWorkspacePageLifecycle(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	extra := filepath.Join(t.TempDir(), "extra")
	for _, path := range []string{project, extra} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	page, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	page.command, page.value = WorkspaceRegister, project
	if err := page.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	items, err := page.manager.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("workspaces=%#v err=%v", items, err)
	}
	id := items[0].ID
	page.command, page.targetID, page.value = WorkspaceAccessAdd, id, extra
	if err := page.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	item, err := page.manager.Get(id)
	if err != nil || len(item.AllowDirs) != 1 {
		t.Fatalf("workspace=%#v err=%v", item, err)
	}
	page.command, page.targetID, page.value = WorkspaceAccessRemove, id, extra
	if err := page.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	item, _ = page.manager.Get(id)
	if len(item.AllowDirs) != 0 {
		t.Fatalf("allow dirs=%v", item.AllowDirs)
	}
	if _, err := page.openCommand(WorkspaceUnregister, id); err != nil {
		t.Fatal(err)
	}
	page.confirm = component.NewConfirmButtons("Delete", "Cancel", true)
	page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	items, err = page.manager.List()
	if err != nil || len(items) != 0 {
		t.Fatalf("workspaces after unregister=%#v err=%v", items, err)
	}
}

func TestWorkspaceEditorRoutesFromKeyAndCommandMessage(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		message tea.Msg
	}{
		{name: "keyboard", message: tea.KeyPressMsg{Code: 'a', Text: "a"}},
		{name: "command-message", message: WorkspaceCommandMsg{Command: WorkspaceRegister}},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := NewWorkspaces(t.Context(), "")
			if err != nil {
				t.Fatal(err)
			}
			_, cmd := page.Update(test.message)
			if cmd == nil {
				t.Fatal("workspace editor route returned no navigation command")
			}
			message, ok := cmd().(NavigateMsg)
			if !ok || strings.Join(message.Path, "/") != "workspaces/register" {
				t.Fatalf("workspace editor navigation=%#v", message)
			}
		})
	}
	page, err := NewWorkspacesRouteAction(t.Context(), "", "", "register")
	if err != nil {
		t.Fatal(err)
	}
	page = runWorkspacePageCmd(t, page, page.Init())
	if page.editor == nil || page.OverlayActive() || !page.InputActive() {
		t.Fatalf("editor=%v overlay=%t input=%t", page.editor != nil, page.OverlayActive(), page.InputActive())
	}
	plain := ansi.Strip(page.View(100, 24))
	for _, want := range []string{"Workspace path", "enter register", "ctrl+o"} {
		if !strings.Contains(strings.ToLower(plain), strings.ToLower(want)) {
			t.Fatalf("workspace editor missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Register Workspace") {
		t.Fatalf("workspace editor retained redundant page title: %q", plain)
	}
}

func TestWorkspaceMutationsSynchronizeRunningRuntime(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	originalReload := reloadWorkspaceRuntime
	calls := 0
	reloadWorkspaceRuntime = func(context.Context) (runtimecontrol.WorkspaceReloadResult, bool, error) {
		calls++
		return runtimecontrol.WorkspaceReloadResult{PID: 4242, Count: calls}, true, nil
	}
	t.Cleanup(func() { reloadWorkspaceRuntime = originalReload })

	workspacePath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspacePath, 0700); err != nil {
		t.Fatal(err)
	}
	workspacePage, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	workspacePage.command, workspacePage.value = WorkspaceRegister, workspacePath
	if err := workspacePage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	items, err := workspacePage.manager.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("workspaces=%#v err=%v", items, err)
	}
	workspaceID := items[0].ID
	relocatedRoot := filepath.Join(filepath.Dir(workspacePath), "relocated")
	if err := os.Rename(workspacePath, relocatedRoot); err != nil {
		t.Fatal(err)
	}
	workspacePage.command, workspacePage.targetID, workspacePage.value = WorkspaceRelocate, workspaceID, relocatedRoot
	if err := workspacePage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	if workspacePage.targetID != workspaceID {
		t.Fatalf("relocate changed stable workspace id: got=%s want=%s", workspacePage.targetID, workspaceID)
	}
	stable, err := workspacePage.manager.Get(workspaceID)
	if err != nil || stable.ID != workspaceID || stable.Path != filepath.Clean(relocatedRoot) {
		t.Fatalf("stable workspace lookup=%#v err=%v", stable, err)
	}
	extra := t.TempDir()
	workspacePage.command, workspacePage.targetID, workspacePage.value = WorkspaceAccessAdd, workspaceID, extra
	if err := workspacePage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	workspacePage.command, workspacePage.targetID, workspacePage.value = WorkspaceAccessRemove, workspaceID, extra
	if err := workspacePage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}

	containerPage, err := NewContainers(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	containerPage.command, containerPage.value = WorkspaceContainerCreate, "Primary"
	if err := containerPage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	containers, err := containerPage.manager.ListContainers()
	if err != nil || len(containers) != 1 {
		t.Fatalf("containers=%#v err=%v", containers, err)
	}
	containerID := containers[0].ID
	containerPage.command, containerPage.targetID, containerPage.value = WorkspaceContainerRename, containerID, "Renamed"
	if err := containerPage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	containerPage.command, containerPage.targetID, containerPage.members = WorkspaceContainerMembers, containerID, []string{workspaceID}
	if err := containerPage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	containerPage.command, containerPage.targetID, containerPage.members = WorkspaceContainerMembers, containerID, nil
	if err := containerPage.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	if calls != 8 {
		t.Fatalf("reload calls after editor mutations = %d, want 8", calls)
	}

	if _, err := containerPage.openCommand(WorkspaceContainerDelete, containerID); err != nil {
		t.Fatal(err)
	}
	containerPage.confirm = component.NewConfirmButtons("Delete", "Cancel", true)
	containerPage.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if containerPage.err != nil {
		t.Fatal(containerPage.err)
	}
	if calls != 9 {
		t.Fatalf("reload calls after container delete = %d, want 9", calls)
	}

	workspacePage.command, workspacePage.targetID = WorkspaceUnregister, workspaceID
	workspacePage.confirm = component.NewConfirmButtons("Delete", "Cancel", true)
	workspacePage.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if workspacePage.err != nil {
		t.Fatal(workspacePage.err)
	}
	if calls != 10 {
		t.Fatalf("reload calls after workspace unregister = %d, want 10", calls)
	}
}

func TestWorkspaceMutationReportsRuntimeReloadFailure(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	originalReload := reloadWorkspaceRuntime
	reloadWorkspaceRuntime = func(context.Context) (runtimecontrol.WorkspaceReloadResult, bool, error) {
		return runtimecontrol.WorkspaceReloadResult{}, true, fmt.Errorf("sentinel reload failure")
	}
	t.Cleanup(func() { reloadWorkspaceRuntime = originalReload })
	page, err := NewContainers(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	page.command, page.value = WorkspaceContainerCreate, "Primary"
	err = page.applyWorkspaceEditor()
	if err == nil || !strings.Contains(err.Error(), "workspace registry saved but running runtime reload failed") || !strings.Contains(err.Error(), "sentinel reload failure") {
		t.Fatalf("error = %v", err)
	}
	containers, listErr := page.manager.ListContainers()
	if listErr != nil || len(containers) != 1 {
		t.Fatalf("persisted containers=%#v err=%v", containers, listErr)
	}
}

func TestWorkspaceContainerLifecycleAndMembership(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	managerPage, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 2)
	for _, name := range []string{"one", "two"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		item, err := managerPage.manager.Register(path)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item.ID)
	}
	page, err := NewContainers(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	page.command, page.value = WorkspaceContainerCreate, "Primary"
	if err := page.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	containers, err := page.manager.ListContainers()
	if err != nil || len(containers) != 1 {
		t.Fatalf("containers=%#v err=%v", containers, err)
	}
	id := containers[0].ID
	page.command, page.targetID, page.value = WorkspaceContainerRename, id, "Renamed"
	if err := page.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	page.command, page.targetID, page.members = WorkspaceContainerMembers, id, append([]string(nil), ids...)
	if err := page.applyWorkspaceEditor(); err != nil {
		t.Fatal(err)
	}
	container, err := page.manager.GetContainer(id)
	if err != nil || len(container.WorkspaceIDs) != 2 || container.Name != "Renamed" {
		t.Fatalf("container=%#v err=%v", container, err)
	}
	if _, err := page.openCommand(WorkspaceContainerDelete, id); err != nil {
		t.Fatal(err)
	}
	page.confirm = component.NewConfirmButtons("Delete", "Cancel", true)
	page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	containers, err = page.manager.ListContainers()
	if err != nil || len(containers) != 0 {
		t.Fatalf("containers after delete=%#v err=%v", containers, err)
	}
	workspaces, err := page.manager.List()
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("workspace records changed by container delete: %#v err=%v", workspaces, err)
	}
}

func TestContainerOverviewListsMembers(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	page, err := NewContainers(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	items := map[string]string{}
	for _, name := range []string{"zeta", "alpha", "middle"} {
		path := filepath.Join(base, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		item, err := page.manager.Register(path)
		if err != nil {
			t.Fatal(err)
		}
		items[name] = item.ID
	}
	container, err := page.manager.CreateContainer("Primary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.manager.AddWorkspacesToContainer(container.ID, []string{items["zeta"], items["middle"], items["alpha"]}); err != nil {
		t.Fatal(err)
	}
	detail, err := NewContainersRoute(t.Context(), container.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	detail = runWorkspacePageCmd(t, detail, detail.Init())
	plain := ansi.Strip(detail.View(140, 30))
	if !strings.Contains(plain, "Members") {
		t.Fatalf("member section missing: %q", plain)
	}
	compactPlain := strings.Join(strings.Fields(plain), "")
	last := -1
	for _, name := range []string{"alpha", "middle", "zeta"} {
		path := filepath.Join(base, name)
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		value := name + " · " + items[name] + " · " + path
		index := strings.Index(compactPlain, strings.Join(strings.Fields(value), ""))
		if index < 0 {
			t.Fatalf("member %q missing: %q", value, plain)
		}
		if index <= last {
			t.Fatalf("members not sorted by path: %q", plain)
		}
		last = index
	}

	empty, err := page.manager.CreateContainer("Empty")
	if err != nil {
		t.Fatal(err)
	}
	emptyDetail, err := NewContainersRoute(t.Context(), empty.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	emptyDetail = runWorkspacePageCmd(t, emptyDetail, emptyDetail.Init())
	emptyView := ansi.Strip(emptyDetail.View(100, 24))
	if strings.Contains(emptyView, "Members") || !strings.Contains(emptyView, "0 workspaces") {
		t.Fatalf("empty container overview=%q", emptyView)
	}
}

func TestWorkspaceDetailDeletionKeepsDetailUntilParentNavigation(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("workspace unregister", func(t *testing.T) {
		page, err := NewWorkspacesRoute(t.Context(), item.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := page.openCommand(WorkspaceUnregister, item.ID); err != nil {
			t.Fatal(err)
		}
		page.confirm = component.NewConfirmButtons("Delete", "Cancel", true)
		cmd := page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil || page.resourceID != item.ID {
			t.Fatalf("navigation=%v resource=%q", cmd != nil, page.resourceID)
		}
		if got := ansi.Strip(page.View(100, 24)); !strings.Contains(got, "Root") || strings.Contains(got, "Overview") {
			t.Fatalf("intermediate detail render=%q", got)
		}
		message, ok := cmd().(NavigateMsg)
		if !ok || strings.Join(message.Path, "/") != "workspaces" || !message.Replace {
			t.Fatalf("navigation=%#v", message)
		}
	})

	item, err = list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	container, err := list.manager.CreateContainer("Primary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := list.manager.AddWorkspaceToContainer(container.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	page, err := NewContainersRoute(t.Context(), container.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.openCommand(WorkspaceContainerDelete, container.ID); err != nil {
		t.Fatal(err)
	}
	page.confirm = component.NewConfirmButtons("Delete", "Cancel", true)
	cmd := page.updateConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || page.resourceID != container.ID {
		t.Fatalf("navigation=%v resource=%q", cmd != nil, page.resourceID)
	}
	if got := ansi.Strip(page.View(100, 24)); !strings.Contains(got, "Primary") {
		t.Fatalf("intermediate container detail render=%q", got)
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "containers" || !message.Replace {
		t.Fatalf("navigation=%#v", message)
	}
	workspaces, err := page.manager.List()
	if err != nil || len(workspaces) != 1 || workspaces[0].ID != item.ID {
		t.Fatalf("workspace records changed by container delete: %#v err=%v", workspaces, err)
	}
}

func TestWorkspaceBrowserHelpStaysAboveAppFooterWithFeedback(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	page, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	page.notice = "Workspace updated"
	plain := ansi.Strip(page.View(100, 24))
	lines := strings.Split(plain, "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 0 || !strings.Contains(lines[last], "? more") {
		t.Fatalf("workspace help line=%d view=%q", last, plain)
	}
	if !strings.Contains(lines[0], "Workspaces") || !strings.Contains(lines[0], "Containers") || !strings.Contains(lines[0], "Workspace updated") {
		t.Fatalf("workspace title/notice invalid: %q", lines[0])
	}
	if !strings.Contains(plain, "enter open") || !strings.Contains(plain, "←/→ tabs") || strings.Contains(plain, "c containers") {
		t.Fatalf("workspace list help invalid: %q", plain)
	}
	notice, help := strings.Index(plain, "Workspace updated"), strings.LastIndex(plain, "? more")
	if notice < 0 || help < 0 || notice >= help {
		t.Fatalf("feedback/help order invalid: notice=%d help=%d view=%q", notice, help, plain)
	}
}

func TestWorkspaceAndContainersRemainTabbedParentPages(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	page, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := page.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	if cmd == nil {
		t.Fatal("containers tab navigation returned no command")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "containers" || !message.Replace {
		t.Fatalf("containers navigation=%#v", message)
	}
	containers, err := NewContainers(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	plain := ansi.Strip(containers.View(100, 24))
	if !strings.Contains(plain, "Workspaces") || !strings.Contains(plain, "Containers") || !strings.Contains(plain, "←/→ tabs") {
		t.Fatalf("containers tab page=%q", plain)
	}
	_, cmd = containers.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	if cmd == nil {
		t.Fatal("workspaces tab navigation returned no command")
	}
	message, ok = cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "workspaces" || !message.Replace {
		t.Fatalf("workspaces navigation=%#v", message)
	}
}

func TestWorkspaceBrowserOpenNavigatesToResourceChild(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	page, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := page.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.reload(); err != nil {
		t.Fatal(err)
	}
	_, cmd := page.Update(component.BrowserOpenMsg{Row: component.Row{ID: item.ID}})
	if cmd == nil {
		t.Fatal("resource open returned no navigation command")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "workspaces/"+item.ID {
		t.Fatalf("resource navigation=%#v", message)
	}
}

func TestWorkspaceBrowserMouseClickUsesRenderedRowPosition(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	page, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := page.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.reload(); err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(page.View(100, 24))
	rowY := -1
	for index, line := range strings.Split(view, "\n") {
		if strings.Contains(line, item.ID) {
			rowY = index
			break
		}
	}
	if rowY < 0 {
		t.Fatalf("workspace row %q not rendered: %q", item.ID, view)
	}
	targets := page.MouseTargets(0, 0, 10)
	dispatch := component.DispatchMouse(targets, tea.MouseClickMsg(tea.Mouse{X: 1, Y: rowY, Button: tea.MouseLeft}))
	if dispatch == nil {
		rows := make([]component.Rect, 0)
		for _, target := range targets {
			if target.ID == "browser.row" {
				rows = append(rows, target.Rect)
			}
		}
		t.Fatalf("rendered workspace row at y=%d has no mouse target; row targets=%#v view=%q", rowY, rows, view)
	}
	updated, open := page.Update(dispatch())
	page = updated.(*WorkspacePage)
	if open == nil {
		t.Fatal("workspace row click did not open selected row")
	}
	_, navigate := page.Update(open())
	if navigate == nil {
		t.Fatal("workspace row open did not navigate")
	}
	message, ok := navigate().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "workspaces/"+item.ID {
		t.Fatalf("workspace mouse navigation=%#v", message)
	}
}

func TestWorkspaceDetailUsesFullChildPageAndNestedSections(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project, extra := filepath.Join(t.TempDir(), "project"), filepath.Join(t.TempDir(), "extra")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extra, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := list.manager.AddAllowDir(item.ID, extra); err != nil {
		t.Fatal(err)
	}
	detail, err := NewWorkspacesRoute(t.Context(), item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.OverlayActive() {
		t.Fatal("resource detail incorrectly reports overlay active")
	}
	plain := ansi.Strip(detail.View(100, 24))
	if !strings.Contains(plain, filepath.Base(item.Path)) || !strings.Contains(plain, "p context") || !strings.Contains(plain, "a access") || !strings.Contains(plain, "v containers") || strings.Contains(plain, "Overview") {
		t.Fatalf("workspace detail=%q", plain)
	}
	if strings.Contains(plain, "Overview   Access") || strings.Contains(plain, "╭") {
		t.Fatalf("workspace detail retained tab/modal chrome: %q", plain)
	}
	_, relocateCmd := detail.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if relocateCmd == nil {
		t.Fatal("relocate navigation returned no command")
	}
	relocateTrigger := relocateCmd()
	updatedDetail, relocateNavigate := detail.Update(relocateTrigger)
	detail = updatedDetail.(*WorkspacePage)
	if relocateNavigate == nil {
		t.Fatalf("relocate trigger=%#v returned no navigation command", relocateTrigger)
	}
	relocateMessage, ok := relocateNavigate().(NavigateMsg)
	if !ok || strings.Join(relocateMessage.Path, "/") != "workspaces/"+item.ID+"/relocate" {
		t.Fatalf("relocate navigation=%#v", relocateMessage)
	}
	_, contextCmd := detail.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if contextCmd == nil {
		t.Fatal("context child navigation returned no command")
	}
	contextMessage, ok := contextCmd().(NavigateMsg)
	if !ok || strings.Join(contextMessage.Path, "/") != "workspaces/"+item.ID+"/context" {
		t.Fatalf("context navigation=%#v", contextMessage)
	}
	_, cmd := detail.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd == nil {
		t.Fatal("access child navigation returned no command")
	}
	message, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(message.Path, "/") != "workspaces/"+item.ID+"/access" {
		t.Fatalf("access navigation=%#v", message)
	}
	access, err := NewWorkspacesRoute(t.Context(), item.ID, "access")
	if err != nil {
		t.Fatal(err)
	}
	if got := ansi.Strip(access.View(100, 24)); !strings.Contains(got, filepath.Base(extra)) || strings.Contains(got, "a access") {
		t.Fatalf("workspace access child=%q", got)
	}
}

func TestWorkspaceRelocateEditorUsesFinalEnterSubmit(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	page, err := NewWorkspacesRouteAction(t.Context(), item.ID, "", "relocate")
	if err != nil {
		t.Fatal(err)
	}
	page = runWorkspacePageCmd(t, page, page.Init())
	if page.value != item.Path {
		t.Fatalf("relocate editor value=%q want=%q", page.value, item.Path)
	}
	plain := strings.ToLower(ansi.Strip(page.View(100, 24)))
	for _, want := range []string{"rebind this workspace", "new workspace path", "enter relocate"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("relocate editor missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "relocate workspace") {
		t.Fatalf("relocate editor retained redundant page title: %q", plain)
	}
}

func TestContainerMembersPickerUsesCompactFilterableLayout(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	page, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	ids := make([]string, 0, 24)
	for index := 0; index < 24; index++ {
		path := filepath.Join(parent, fmt.Sprintf("project-%02d", index))
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		item, err := page.manager.Register(path)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item.ID)
	}
	container, err := page.manager.CreateContainer("Primary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.manager.AddWorkspacesToContainer(container.ID, ids[:2]); err != nil {
		t.Fatal(err)
	}
	page, err = NewContainersRouteAction(t.Context(), container.ID, "workspaces", "edit")
	if err != nil {
		t.Fatal(err)
	}
	page = runWorkspacePageCmd(t, page, page.Init())
	plain := ansi.Strip(page.View(120, 30))
	if !strings.Contains(plain, "2 selected / 24 available") {
		t.Fatalf("member count missing: %q", plain)
	}
	first, err := page.manager.Get(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, filepath.Base(first.Path)+" · "+first.ID) {
		t.Fatalf("compact member label missing: %q", plain)
	}
	if strings.Contains(plain, parent+string(filepath.Separator)) {
		t.Fatalf("absolute workspace paths leaked into member options: %q", plain)
	}
	if strings.Contains(plain, "╭") || strings.Contains(plain, "╮") {
		t.Fatalf("member editor unexpectedly rendered modal chrome: %q", plain)
	}
	if got := workspaceMemberPickerHeight(100, 30); got != 16 {
		t.Fatalf("large member picker height=%d want=16", got)
	}
}

func TestWorkspaceAndContainerCopySelectedID(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	workspacePage, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	workspaceItem, err := workspacePage.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspacePage.reload(); err != nil {
		t.Fatal(err)
	}
	container, err := workspacePage.manager.CreateContainer("Primary")
	if err != nil {
		t.Fatal(err)
	}
	previous := copyWorkspaceID
	t.Cleanup(func() { copyWorkspaceID = previous })
	var copied string
	copyWorkspaceID = func(value string) error { copied = value; return nil }

	for _, test := range []struct {
		name string
		open func() (*WorkspacePage, error)
		id   string
	}{{"workspace", func() (*WorkspacePage, error) { return NewWorkspacesRoute(t.Context(), workspaceItem.ID, "") }, workspaceItem.ID}, {"container", func() (*WorkspacePage, error) { return NewContainersRoute(t.Context(), container.ID, "") }, container.ID}} {
		t.Run(test.name, func(t *testing.T) {
			copied = ""
			page, err := test.open()
			if err != nil {
				t.Fatal(err)
			}
			updated, cmd := page.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
			page = updated.(*WorkspacePage)
			_ = runWorkspacePageCmd(t, page, cmd)
			if copied != test.id {
				t.Fatalf("copied=%q want=%q", copied, test.id)
			}
		})
	}
}

func TestWorkspaceProjectContextBuildUsesVolatileSession(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	session := NewWorkspaceContextSession()
	page, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context", session)
	if err != nil {
		t.Fatal(err)
	}
	if !page.InputActive() || page.contextData == nil {
		t.Fatalf("context input=%t data=%v", page.InputActive(), page.contextData != nil)
	}
	plain := ansi.Strip(page.View(110, 30))
	for _, want := range []string{"Scope", "Budgets", "Include"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("context editor missing %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "Project Context · "+item.ID) {
		t.Fatalf("context editor retained redundant page title: %q", plain)
	}
	if !strings.Contains(plain, "enter next") {
		t.Fatalf("project context editor does not advertise Enter navigation: %q", plain)
	}
	sourcePath := filepath.Join(project, "AGENTS.md")
	page.contextBuild = func(_ context.Context, workspaceID string, options projectcontext.Options) (projectcontext.Result, error) {
		if workspaceID != item.ID || options != session.Options {
			t.Fatalf("build workspace=%q options=%#v session=%#v", workspaceID, options, session.Options)
		}
		return projectcontext.Result{
			Root: project, WorkspaceID: item.ID,
			InstructionContext: instructioncontext.InstructionContext{
				Root: project, WorkspaceID: item.ID, ToolProfile: instructioncontext.ToolProfile{Name: "full", Count: 77}, InstructionsText: "# Context Document\n\nUse compact code.", InstructionTruncated: true,
				ProjectMemory: instructioncontext.ProjectMemoryBundle{Sections: []instructioncontext.Section{{Path: sourcePath, Kind: instructioncontext.SectionProject, Content: "# AGENTS\n\nSource body.", LoadedBytes: 22}}},
				AutoMemory:    instructioncontext.AutoMemorySnapshot{Loaded: true, Content: "## general\n\n- remember compact code", Bytes: 36, Entries: 1, Truncated: true},
				GlobalContext: "# Global Context\n\nShared policy.",
				GlobalRules:   []rules.Rule{{Path: "managed://global-rule", Source: "managed", Content: "# Global Rule\n\nAlways apply."}},
				Rules:         []rules.Rule{{Path: filepath.Join(project, ".agents", "rules", "project.md"), Source: "agents", Content: "# Project Rule\n\nProject only."}},
				Skills:        []skills.Skill{{Name: "review", Description: "Review changes", Path: filepath.Join(project, ".agents", "skills", "review", "SKILL.md"), Source: "agents"}},
				Sources:       []instructioncontext.SourceSnapshot{{Provider: "claude", Kind: "context", Paths: []string{sourcePath}, Count: 1, Enabled: true, Loaded: true}},
			},
			Summary: projectcontext.Summary{InstructionBytes: 1234, MemoryBytes: 456, Rules: 2, Skills: 1},
		}, nil
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*WorkspacePage)
	if cmd == nil || !page.contextBuilding || session.Result != nil {
		t.Fatalf("build cmd=%v building=%t result=%v", cmd, page.contextBuilding, session.Result != nil)
	}
	buildMsg := workspaceContextBuildMessage(t, cmd)
	updated, navigation := page.Update(buildMsg)
	page = updated.(*WorkspacePage)
	if navigation == nil || page.contextBuilding || session.Result == nil || session.Options != defaultWorkspaceContextOptions() {
		t.Fatalf("finished build navigation=%v building=%t session=%#v", navigation != nil, page.contextBuilding, session)
	}
	if page.Dirty() {
		t.Fatal("successful project context build retained a dirty draft")
	}
	navigate, ok := navigation().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "workspaces/"+item.ID+"/context-preview" {
		t.Fatalf("preview navigation=%#v", navigate)
	}
	preview, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context-preview", session)
	if err != nil {
		t.Fatal(err)
	}
	previewView := ansi.Strip(preview.View(110, 30))
	for _, want := range []string{"Rendered", "Sources", "JSON", "Use compact code.", "2 rules", "1 skills", "truncated"} {
		if !strings.Contains(previewView, want) {
			t.Fatalf("preview missing %q: %q", want, previewView)
		}
	}
	if strings.Contains(previewView, "Project Context Preview") || strings.Contains(previewView, "Rendered Context") {
		t.Fatalf("preview retained redundant page title: %q", previewView)
	}
	updated, _ = preview.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	preview = updated.(*WorkspacePage)
	sourcesView := ansi.Strip(preview.View(110, 30))
	for _, want := range []string{"User-level Sources", "Claude", "Context · 1 · included", "Global Context", "Auto Memory", "Project/User Instruction Files · 1", "Global Rules · 1", "Rules · 1"} {
		if !strings.Contains(sourcesView, want) {
			t.Fatalf("sources preview missing %q: %q", want, sourcesView)
		}
	}
	if strings.Contains(sourcesView, "Context Sources") {
		t.Fatalf("sources preview retained redundant local title: %q", sourcesView)
	}
	foundSkills := false
	for _, node := range preview.contextPreview.sources.AllNodes() {
		if strings.Contains(node.Value(), "Skills · 1") {
			foundSkills = true
			break
		}
	}
	if !foundSkills {
		t.Fatal("skills summary node missing from context source tree")
	}
	foundSource := false
	for _, node := range preview.contextPreview.sources.AllNodes() {
		value, ok := node.GivenValue().(workspaceContextPreviewNode)
		if ok && value.Kind == workspaceContextPreviewPath && value.Path == sourcePath {
			preview.contextPreview.sources.SetYOffset(node.YOffset())
			foundSource = true
			break
		}
	}
	if !foundSource {
		t.Fatalf("source node %q not found", sourcePath)
	}
	updated, _ = preview.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	preview = updated.(*WorkspacePage)
	if preview.contextPreview.sourceViewer == nil || !preview.InputActive() {
		t.Fatalf("source viewer active=%t input=%t", preview.contextPreview.sourceViewer != nil, preview.InputActive())
	}
	sourceView := ansi.Strip(preview.View(110, 30))
	displaySourcePath := sourcePath
	if resolved, err := filepath.EvalSymlinks(sourcePath); err == nil {
		displaySourcePath = resolved
	}
	compactSourceView := strings.Join(strings.Fields(sourceView), "")
	for _, want := range []string{"Source", displaySourcePath, "AGENTS", "Source body."} {
		if !strings.Contains(compactSourceView, strings.Join(strings.Fields(want), "")) {
			t.Fatalf("source viewer missing %q: %q", want, sourceView)
		}
	}
	narrowSourceView := ansi.Strip(preview.View(36, 18))
	for _, line := range strings.Split(narrowSourceView, "\n") {
		if width := lipgloss.Width(line); width > 36 {
			t.Fatalf("narrow source viewer line width=%d: %q", width, line)
		}
	}
	updated, _ = preview.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	preview = updated.(*WorkspacePage)
	if preview.contextPreview.sourceViewer != nil {
		t.Fatal("source viewer did not close")
	}
	preview.View(80, 20)
	var jsonTabMsg tea.Msg
	for _, target := range preview.MouseTargets(2, 3, 10) {
		if target.ID != "workspace.context.tab" {
			continue
		}
		message := target.Handle(component.MouseEvent{Button: tea.MouseLeft})
		if tab, ok := message.(workspaceContextPreviewTabMsg); ok && tab.Tab == workspaceContextPreviewJSON {
			jsonTabMsg = message
			break
		}
	}
	if jsonTabMsg == nil {
		t.Fatal("JSON preview tab mouse target not found")
	}
	updated, _ = preview.Update(jsonTabMsg)
	preview = updated.(*WorkspacePage)
	if strings.Contains(preview.contextPreview.json.Content(), "```") || !strings.HasPrefix(strings.TrimSpace(preview.contextPreview.json.Content()), "{") {
		t.Fatalf("json preview is not raw formatted JSON: %q", preview.contextPreview.json.Content())
	}
	jsonView := ansi.Strip(preview.View(110, 30))
	for _, want := range []string{"workspace_id", item.ID, "instruction_context"} {
		if !strings.Contains(jsonView, want) {
			t.Fatalf("json preview missing %q: %q", want, jsonView)
		}
	}
	fresh, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context-preview", NewWorkspaceContextSession())
	if err != nil {
		t.Fatal(err)
	}
	if got := ansi.Strip(fresh.View(110, 30)); !strings.Contains(got, "not built") || strings.Contains(got, "Rendered Context") {
		t.Fatalf("fresh session unexpectedly reused preview: %q", got)
	}
}

func TestWorkspaceProjectContextEditorRejectsInvalidBudgetWithoutSubmitting(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	page, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context", NewWorkspaceContextSession())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = page.Update(component.EditorSectionMsg{Index: 1})
	for range 2 {
		updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		page = updated.(*WorkspacePage)
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	page = updated.(*WorkspacePage)
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*WorkspacePage)
	if cmd != nil || page.contextBuilding || page.contextEditor == nil {
		t.Fatalf("invalid budget submitted: cmd=%v building=%t editor=%v", cmd != nil, page.contextBuilding, page.contextEditor != nil)
	}
	if plain := strings.ToLower(ansi.Strip(page.View(100, 30))); !strings.Contains(plain, "max memory entries must be between") {
		t.Fatalf("validation feedback missing: %q", plain)
	}
}

func TestWorkspaceProjectContextEditorRetainsDraftOnBuildFailure(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	session := NewWorkspaceContextSession()
	page, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context", session)
	if err != nil {
		t.Fatal(err)
	}
	_ = page.Init()
	updated, _ := page.Update(tea.KeyPressMsg{Code: 'd', Text: "draft"})
	page = updated.(*WorkspacePage)
	page.contextBuild = func(_ context.Context, _ string, options projectcontext.Options) (projectcontext.Result, error) {
		if options.Path != "draft" {
			t.Fatalf("draft path=%q", options.Path)
		}
		return projectcontext.Result{}, fmt.Errorf("build failed")
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*WorkspacePage)
	if cmd == nil || !page.contextBuilding || page.Dirty() {
		t.Fatalf("build start cmd=%v building=%t dirty=%t", cmd != nil, page.contextBuilding, page.Dirty())
	}
	updated, navigation := page.Update(workspaceContextBuildMessage(t, cmd))
	page = updated.(*WorkspacePage)
	if navigation != nil || page.contextBuilding || page.contextEditor == nil || page.contextData.Path != "draft" || !page.Dirty() || session.Result != nil {
		t.Fatalf("failed build navigation=%v building=%t editor=%v path=%q dirty=%t result=%v", navigation != nil, page.contextBuilding, page.contextEditor != nil, page.contextData.Path, page.Dirty(), session.Result != nil)
	}
	if plain := ansi.Strip(page.View(100, 30)); !strings.Contains(plain, "build failed") {
		t.Fatalf("build failure feedback missing: %q", plain)
	}
}

func TestWorkspaceContextSourceContentUsesLoadedDataAndRejectsSymlinks(t *testing.T) {
	virtualPath := filepath.Join(t.TempDir(), "virtual.md")
	result := projectcontext.Result{InstructionContext: instructioncontext.InstructionContext{ProjectMemory: instructioncontext.ProjectMemoryBundle{Sections: []instructioncontext.Section{{Path: virtualPath, Content: "loaded source"}}}}}
	content, err := workspaceContextSourceContent(result, virtualPath)
	if err != nil || content != "loaded source" {
		t.Fatalf("loaded source content=%q err=%v", content, err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "source.md")
	if err := os.WriteFile(file, []byte("disk source"), 0600); err != nil {
		t.Fatal(err)
	}
	content, err = workspaceContextSourceContent(projectcontext.Result{}, file)
	if err != nil || content != "disk source" {
		t.Fatalf("disk source content=%q err=%v", content, err)
	}
	link := filepath.Join(dir, "source-link.md")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceContextSourceContent(projectcontext.Result{}, link); err == nil {
		t.Fatal("symlink source was accepted")
	}
}

func TestWorkspaceProjectContextBuildCanBeCancelled(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	session := NewWorkspaceContextSession()
	page, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context", session)
	if err != nil {
		t.Fatal(err)
	}
	page.contextBuild = func(ctx context.Context, _ string, _ projectcontext.Options) (projectcontext.Result, error) {
		<-ctx.Done()
		return projectcontext.Result{}, ctx.Err()
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*WorkspacePage)
	if cmd == nil || !page.contextBuilding {
		t.Fatalf("build did not start: cmd=%v building=%t", cmd, page.contextBuilding)
	}
	buildID := page.contextBuildID
	updated, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	page = updated.(*WorkspacePage)
	if page.contextBuilding || page.contextBuildID == buildID || session.Result != nil || page.Notice() != "Project Context build cancelled" {
		t.Fatalf("cancel state building=%t id=%d result=%v notice=%q", page.contextBuilding, page.contextBuildID, session.Result != nil, page.Notice())
	}
}

func TestWorkspaceProjectContextCloseCancelsInFlightBuild(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	session := NewWorkspaceContextSession()
	page, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context", session)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := make(chan struct{})
	page.contextBuild = func(ctx context.Context, _ string, _ projectcontext.Options) (projectcontext.Result, error) {
		<-ctx.Done()
		close(cancelled)
		return projectcontext.Result{}, ctx.Err()
	}
	updated, cmd := page.Update(component.EditorSubmitMsg{})
	page = updated.(*WorkspacePage)
	if cmd == nil || !page.contextBuilding {
		t.Fatalf("build did not start: cmd=%v building=%t", cmd != nil, page.contextBuilding)
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("build command message=%T", cmd())
	}
	for _, next := range batch {
		if next != nil {
			go next()
		}
	}
	buildID := page.contextBuildID
	page.Close()
	if page.contextBuilding || page.contextBuildID == buildID || page.contextCancel != nil || session.Result != nil {
		t.Fatalf("close state building=%t id=%d cancel=%v result=%v", page.contextBuilding, page.contextBuildID, page.contextCancel != nil, session.Result != nil)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("build context was not cancelled by Close")
	}
}

func TestWorkspaceProjectContextPreviewTabPersistsInSession(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	result := projectcontext.Result{Root: project, WorkspaceID: item.ID, InstructionContext: instructioncontext.InstructionContext{Root: project, WorkspaceID: item.ID, InstructionsText: "# Context"}}
	session := NewWorkspaceContextSession()
	session.Result = &result
	preview, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context-preview", session)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := preview.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	preview = updated.(*WorkspacePage)
	if preview.contextPreview.tab != workspaceContextPreviewJSON || session.PreviewTab != "json" {
		t.Fatalf("tab=%d session=%q", preview.contextPreview.tab, session.PreviewTab)
	}
	rebuilt, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context-preview", session)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.contextPreview.tab != workspaceContextPreviewJSON {
		t.Fatalf("rebuilt tab=%d want=%d", rebuilt.contextPreview.tab, workspaceContextPreviewJSON)
	}
}

func TestWorkspaceProjectContextPreviewResponsiveLayouts(t *testing.T) {
	defer configformat.SetRootPath("")
	if err := configformat.SetRootPath(filepath.Join(t.TempDir(), "config")); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project-with-a-long-name-for-responsive-preview")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := NewWorkspaces(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := list.manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	result := projectcontext.Result{
		Root: project, WorkspaceID: item.ID,
		InstructionContext: instructioncontext.InstructionContext{
			Root: project, WorkspaceID: item.ID,
			InstructionsText: "# Responsive Project Context\n\n" + strings.Repeat("Long markdown content for width verification. ", 20),
			GlobalContext:    strings.Repeat("global-context-", 20),
			Sources:          []instructioncontext.SourceSnapshot{{Provider: "agents", Kind: "context", Paths: []string{filepath.Join(project, "AGENTS.md")}, Count: 1, Enabled: true, Loaded: true}},
		},
		Summary: projectcontext.Summary{InstructionBytes: 4096, MemoryBytes: 1024, Rules: 2, Skills: 1},
	}
	session := NewWorkspaceContextSession()
	session.Result = &result
	preview, err := NewWorkspacesRouteWithContextSession(t.Context(), item.ID, "context-preview", session)
	if err != nil {
		t.Fatal(err)
	}
	for _, tab := range []tea.KeyPressMsg{{Code: '1', Text: "1"}, {Code: '2', Text: "2"}, {Code: '3', Text: "3"}} {
		updated, _ := preview.Update(tab)
		preview = updated.(*WorkspacePage)
		for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 40}, {24, 10}} {
			testutil.AssertLinesFit(t, preview.View(size[0], size[1]), size[0])
		}
	}
	updated, _ := preview.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	preview = updated.(*WorkspacePage)
	preview.View(24, 10)
	updated, _ = preview.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	preview = updated.(*WorkspacePage)
	if got := preview.contextPreview.json.XOffset(); got != 0 {
		t.Fatalf("wrapped JSON horizontal offset=%d want 0", got)
	}
}

func workspaceContextBuildMessage(t *testing.T, cmd tea.Cmd) workspaceContextBuildMsg {
	t.Helper()
	message := cmd()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		t.Fatalf("build command message=%T", message)
	}
	for _, next := range batch {
		if next == nil {
			continue
		}
		if result, ok := next().(workspaceContextBuildMsg); ok {
			return result
		}
	}
	t.Fatal("workspace context build message not found")
	return workspaceContextBuildMsg{}
}

func runWorkspacePageCmd(t *testing.T, page *WorkspacePage, cmd tea.Cmd) *WorkspacePage {
	t.Helper()
	if cmd == nil {
		return page
	}
	message := cmd()
	if batch, ok := message.(tea.BatchMsg); ok {
		for _, next := range batch {
			page = runWorkspacePageCmd(t, page, next)
		}
		return page
	}
	updated, next := page.Update(message)
	return runWorkspacePageCmd(t, updated.(*WorkspacePage), next)
}
