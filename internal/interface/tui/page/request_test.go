package page

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

func TestRequestsPageRefreshModesAndDeepLink(t *testing.T) {
	now := time.Now().UTC()
	pending := approval.Request{ID: "req_pending_full", Status: approval.StatusPending, WorkspaceID: "ws_a", Source: "tunnel", TargetTool: "run_command", Title: "Allow update", Command: "cm update", Arguments: []byte(`{"workspace_id":"ws_a","command":"cm update"}`), GuardReason: "guarded", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	approved := approval.Request{ID: "req_approved_full", Status: approval.StatusApproved, WorkspaceID: "ws_b", Source: "tunnel", TargetTool: "run_command", Title: "Allow install", Command: "cm install", Arguments: []byte(`{"workspace_id":"ws_b","command":"cm install"}`), CreatedAt: now.Add(-time.Minute), ExpiresAt: now, ResolvedAt: now, RetryUntil: now.Add(time.Minute)}
	server := newRequestPageServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/requests":
			_ = json.NewEncoder(w).Encode([]approval.Request{pending, approved})
		case "/requests/view":
			switch r.URL.Query().Get("id") {
			case "req_pending", pending.ID:
				_ = json.NewEncoder(w).Encode(pending)
			case approved.ID:
				_ = json.NewEncoder(w).Encode(approved)
			default:
				t.Fatalf("view id=%q", r.URL.Query().Get("id"))
			}
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	})
	defer server.Close()

	page, err := NewRequests(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd := page.Init(); cmd == nil || !page.loading {
		t.Fatalf("init cmd=%v loading=%t", cmd, page.loading)
	}
	updated, _ := page.Update(page.refreshCmd()())
	page = updated.(*RequestsPage)
	if len(page.requests) != 2 || page.mode != requestModePending {
		t.Fatalf("requests=%#v mode=%d", page.requests, page.mode)
	}
	if selected, ok := page.browser.Selected(); !ok || selected.ID != pending.ID {
		t.Fatalf("pending selected=%#v ok=%t", selected, ok)
	}
	page.setMode(requestModeHistory)
	if selected, ok := page.browser.Selected(); !ok || selected.ID != approved.ID {
		t.Fatalf("history selected=%#v ok=%t", selected, ok)
	}
	if content := page.View(100, 28); !strings.Contains(content, "Pending") || !strings.Contains(content, "History") || strings.Contains(content, pending.Title) {
		t.Fatalf("history view=%q", content)
	}

	deep, err := NewRequests(t.Context(), "req_pending")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = deep.Update(deep.refreshCmd()())
	deep = updated.(*RequestsPage)
	if deep.resourceID != pending.ID || deep.OverlayActive() || deep.mode != requestModeAll {
		t.Fatalf("deep resource=%q overlay=%t mode=%d", deep.resourceID, deep.OverlayActive(), deep.mode)
	}
	view := ansi.Strip(deep.View(100, 28))
	for _, expected := range []string{pending.WorkspaceID, pending.TargetTool, "c command", "v arguments", "g guard", "? more"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("deep view missing %q: %q", expected, view)
		}
	}
	if strings.Contains(view, "Overview") {
		t.Fatalf("request child repeated breadcrumb title: %q", view)
	}
	if strings.Contains(view, "cm update") {
		t.Fatalf("overview still renders raw command: %q", view)
	}
	updated, _ = deep.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	deep = updated.(*RequestsPage)
	expanded := ansi.Strip(deep.View(100, 28))
	if !strings.Contains(expanded, "approve") || !strings.Contains(expanded, "deny") || !strings.Contains(expanded, "less") {
		t.Fatalf("expanded request footer=%q", expanded)
	}
	if strings.Contains(view, "Overview   Arguments") || strings.Contains(view, "╭") {
		t.Fatalf("deep view retained tab/modal chrome: %q", view)
	}
	_, cmd := deep.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if cmd == nil {
		t.Fatal("arguments child navigation returned no command")
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "requests/all/"+pending.ID+"/arguments" {
		t.Fatalf("arguments navigation=%#v", navigate)
	}
	_, cmd = deep.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if cmd == nil {
		t.Fatal("command child navigation returned no command")
	}
	navigate, ok = cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "requests/all/"+pending.ID+"/command" {
		t.Fatalf("command navigation=%#v", navigate)
	}

	command, err := NewRequestsRoute(t.Context(), pending.ID, "command")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = command.Update(command.refreshCmd()())
	command = updated.(*RequestsPage)
	commandView := ansi.Strip(command.View(100, 28))
	if command.codeViewer == nil || command.codeViewer.Content() != "cm update" || !strings.Contains(commandView, "cm update") {
		t.Fatalf("command child=%q viewer=%#v", commandView, command.codeViewer)
	}

	arguments, err := NewRequestsRoute(t.Context(), pending.ID, "arguments")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = arguments.Update(arguments.refreshCmd()())
	arguments = updated.(*RequestsPage)
	argumentsView := ansi.Strip(arguments.View(100, 28))
	if arguments.codeViewer == nil || !strings.Contains(arguments.codeViewer.Content(), `"command": "cm update"`) || !strings.Contains(argumentsView, `"command": "cm update"`) || strings.Contains(argumentsView, "v arguments") {
		t.Fatalf("arguments child=%q", argumentsView)
	}

	history, err := NewRequestsRoute(t.Context(), approved.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = history.Update(history.refreshCmd()())
	history = updated.(*RequestsPage)
	historyView := ansi.Strip(history.View(100, 28))
	if strings.Contains(historyView, "a approve") || strings.Contains(historyView, "d deny") {
		t.Fatalf("resolved detail exposed resolution actions: %q", historyView)
	}
}

func TestRequestMutationNoticeRendersBesidePageTitle(t *testing.T) {
	page, err := NewRequests(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	page.notice = "Request approved"
	line := strings.Split(ansi.Strip(page.View(100, 24)), "\n")[0]
	if !strings.Contains(line, "Pending") || !strings.Contains(line, "History") || !strings.Contains(line, "All") || !strings.Contains(line, "Request approved") {
		t.Fatalf("request title notice=%q", line)
	}
}

func TestRequestsPageUsesTabsAndModeAwareNavigation(t *testing.T) {
	now := time.Now().UTC()
	request := approval.Request{ID: "req_history", Status: approval.StatusDenied, WorkspaceID: "ws_a", TargetTool: "run_command", Title: "Denied update", CreatedAt: now.Add(-time.Minute), ExpiresAt: now, ResolvedAt: now}
	page, err := NewRequestsRouteMode(t.Context(), "history", "", "")
	if err != nil {
		t.Fatal(err)
	}
	page.requests = []approval.Request{request}
	page.rebuildBrowser(request.ID)
	plain := ansi.Strip(page.View(100, 24))
	if !strings.Contains(plain, "Pending") || !strings.Contains(plain, "History") || !strings.Contains(plain, "All") || strings.Contains(plain, "1 pending") || strings.Contains(plain, "2 history") || strings.Contains(plain, "3 all") {
		t.Fatalf("tab view=%q", plain)
	}
	updated, cmd := page.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	page = updated.(*RequestsPage)
	if cmd == nil {
		t.Fatal("right tab navigation returned no command")
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "requests/all" || !navigate.Replace {
		t.Fatalf("right navigation=%#v", navigate)
	}
	_, cmd = page.Update(component.BrowserOpenMsg{Row: component.Row{ID: request.ID}})
	if cmd == nil {
		t.Fatal("history detail navigation returned no command")
	}
	navigate, ok = cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "requests/history/"+request.ID || navigate.Replace {
		t.Fatalf("detail navigation=%#v", navigate)
	}
}

func TestRequestsPagePreservesExpandedBrowserHelpAcrossRefresh(t *testing.T) {
	now := time.Now().UTC()
	request := approval.Request{ID: "req_pending", Status: approval.StatusPending, WorkspaceID: "ws_a", TargetTool: "run_command", Title: "Allow update", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	page, _ := NewRequests(t.Context(), "")
	page.requests = []approval.Request{request}
	page.rebuildBrowser(request.ID)
	updated, _ := page.browser.Update(tea.KeyPressMsg{Code: '?'})
	page.browser = updated.(component.Browser)
	if !page.browser.HelpExpanded() {
		t.Fatal("browser help did not expand")
	}
	page.rebuildBrowser(request.ID)
	if !page.browser.HelpExpanded() {
		t.Fatal("browser help collapsed after request refresh rebuild")
	}
}

func TestRequestsPageDetailRefreshPreservesScrollOffset(t *testing.T) {
	now := time.Now().UTC()
	request := approval.Request{ID: "req_scroll_refresh", Status: approval.StatusPending, WorkspaceID: "ws_a", TargetTool: "run_command", Title: "Allow update", CreatedAt: now, ExpiresAt: now.Add(time.Minute), Reason: strings.Repeat("long detail content ", 80)}
	page, _ := NewRequestsRouteMode(t.Context(), "all", request.ID, "")
	page.requests = []approval.Request{request}
	page.width, page.height = 40, 12
	page.syncDetail()
	for range 6 {
		updated, _ := page.detail.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		page.detail = updated
	}
	before := page.detail.YOffset()
	if before == 0 {
		t.Fatal("detail did not scroll before refresh")
	}
	request.ExpiresAt = request.ExpiresAt.Add(time.Second)
	page.requests = []approval.Request{request}
	page.syncDetail()
	if after := page.detail.YOffset(); after != before {
		t.Fatalf("detail refresh reset scroll: before=%d after=%d", before, after)
	}
}

func TestRequestsPageResolutionUsesRoutedEditorWithoutConfirmField(t *testing.T) {
	now := time.Now().UTC()
	request := approval.Request{ID: "req_pending", Status: approval.StatusPending, WorkspaceID: "ws_a", TargetTool: "run_command", Title: "Allow update", Arguments: []byte(`{"command":"cm update"}`), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	resolveCalls := 0
	server := newRequestPageServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/requests":
			_ = json.NewEncoder(w).Encode([]approval.Request{request})
		case "/requests/view":
			_ = json.NewEncoder(w).Encode(request)
		case "/requests/approve":
			resolveCalls++
			var input struct {
				ID           string `json:"id"`
				Reason       string `json:"reason"`
				AllowSimilar bool   `json:"allow_similar"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.ID != request.ID || input.Reason != "reviewed" || input.AllowSimilar {
				t.Fatalf("input=%#v", input)
			}
			resolved := request
			resolved.Status, resolved.Reason, resolved.ResolvedBy = approval.StatusApproved, input.Reason, "cli"
			resolved.ResolvedAt, resolved.RetryUntil = time.Now().UTC(), time.Now().UTC().Add(time.Minute)
			_ = json.NewEncoder(w).Encode(resolved)
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	})
	defer server.Close()
	page, _ := NewRequests(t.Context(), "")
	updated, _ := page.Update(page.refreshCmd()())
	page = updated.(*RequestsPage)

	cmd := page.handleCommand(RequestApprove, request.ID)
	if cmd == nil || page.resolveForm != nil || page.editor != nil || page.overlay != requestOverlayNone {
		t.Fatalf("route cmd=%v form=%#v editor=%v overlay=%d", cmd != nil, page.resolveForm, page.editor != nil, page.overlay)
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "requests/pending/"+request.ID+"/approve" {
		t.Fatalf("navigation=%#v", navigate)
	}
	resolvePage, _ := NewRequestsRouteAction(t.Context(), "pending", request.ID, "", "approve")
	updated, initEditor := resolvePage.Update(resolvePage.refreshCmd()())
	resolvePage = updated.(*RequestsPage)
	if initEditor == nil || resolvePage.editor == nil || resolvePage.resolveForm == nil || resolvePage.OverlayActive() {
		t.Fatalf("editor=%v form=%#v init=%v overlay=%t", resolvePage.editor != nil, resolvePage.resolveForm, initEditor != nil, resolvePage.OverlayActive())
	}
	resolvePage.resolveForm.Reason = "reviewed"
	resolve := resolvePage.submitResolveForm()
	if resolve == nil || resolvePage.overlay != requestOverlayOperation || !resolvePage.editor.Submitting() {
		t.Fatalf("resolve=%v overlay=%d submitting=%t", resolve != nil, resolvePage.overlay, resolvePage.editor.Submitting())
	}
	updated, follow := resolvePage.Update(resolve())
	resolvePage = updated.(*RequestsPage)
	if follow == nil || resolvePage.editor != nil || resolveCalls != 1 {
		t.Fatalf("follow=%v editor=%v calls=%d", follow != nil, resolvePage.editor != nil, resolveCalls)
	}
	request.Status = approval.StatusApproved
	if cmd := page.handleCommand(RequestDeny, request.ID); cmd == nil {
		page.upsertRequest(request)
		if cmd = page.handleCommand(RequestDeny, request.ID); cmd != nil || page.err == nil || !strings.Contains(page.err.Error(), "cannot be resolved") {
			t.Fatalf("resolved request deny cmd=%v err=%v", cmd, page.err)
		}
	}
}

func TestRequestsPageCreatesSyntheticTestRequest(t *testing.T) {
	now := time.Now().UTC()
	created := approval.Request{ID: "req_test_created", Status: approval.StatusPending, WorkspaceID: "ws_demo", Source: "cli-dummy", TargetTool: "run_command", Title: "Allow test command", Arguments: []byte(`{"workspace_id":"ws_demo","command":"echo hello","dummy":true}`), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	createCalls := 0
	server := newRequestPageServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/requests/create-dummy":
			createCalls++
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input["workspace_id"] != "ws_demo" || input["title"] != "Allow test command" || input["command"] != "echo hello" {
				t.Fatalf("input=%#v", input)
			}
			_ = json.NewEncoder(w).Encode(created)
		case "/requests":
			_ = json.NewEncoder(w).Encode([]approval.Request{created})
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	})
	defer server.Close()
	page, _ := NewRequestsRouteAction(t.Context(), "", "", "", "create-test")
	if page.editor == nil || page.createForm == nil || page.overlay != requestOverlayNone {
		t.Fatalf("create form=%#v editor=%v overlay=%d", page.createForm, page.editor != nil, page.overlay)
	}
	if page.createForm.WorkspaceID != "ws_dummy" || page.createForm.Title != "Allow test command" || page.createForm.Command != "echo test approval" {
		t.Fatalf("create defaults=%#v", page.createForm)
	}
	page.createForm.WorkspaceID = "ws_demo"
	page.createForm.Command = "echo hello"
	create := page.submitCreateTestForm()
	if create == nil || page.overlay != requestOverlayOperation {
		t.Fatalf("create=%v overlay=%d", create, page.overlay)
	}
	updated, _ := page.Update(create())
	page = updated.(*RequestsPage)
	request, ok := page.findRequest(created.ID)
	if !ok || request.ID != created.ID || createCalls != 1 || page.editor != nil {
		t.Fatalf("created=%#v ok=%t calls=%d editor=%v", request, ok, createCalls, page.editor != nil)
	}
}

func TestRequestsPageTestRequestShortcutNavigatesToEditor(t *testing.T) {
	page, _ := NewRequests(t.Context(), "")
	updated, cmd := page.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	page = updated.(*RequestsPage)
	if cmd == nil || page.editor != nil || page.createForm != nil {
		t.Fatalf("shortcut cmd=%v editor=%v form=%#v", cmd != nil, page.editor != nil, page.createForm)
	}
	navigate, ok := cmd().(NavigateMsg)
	if !ok || strings.Join(navigate.Path, "/") != "requests/create-test" {
		t.Fatalf("navigation=%#v", navigate)
	}
	editorPage, _ := NewRequestsRouteAction(t.Context(), "", "", "", "create-test")
	if initEditor := editorPage.editor.Init(); initEditor != nil {
		updated, _ = editorPage.Update(initEditor())
		editorPage = updated.(*RequestsPage)
	}
	plain := ansi.Strip(editorPage.View(100, 24))
	for _, want := range []string{"Workspace ID", "Title", "Command", "echo test approval"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("create form missing %q: %q", want, plain)
		}
	}
}

func TestRequestsPageResolveCancellationIgnoresLateResult(t *testing.T) {
	now := time.Now().UTC()
	request := approval.Request{ID: "req_pending", Status: approval.StatusPending, WorkspaceID: "ws_a", TargetTool: "run_command", Title: "Allow update", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := newRequestPageServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/requests/view":
			_ = json.NewEncoder(w).Encode(request)
			return
		case "/requests/approve":
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
		started <- struct{}{}
		<-release
		resolved := request
		resolved.Status = approval.StatusApproved
		_ = json.NewEncoder(w).Encode(resolved)
	})
	defer server.Close()
	page, _ := NewRequests(t.Context(), "")
	page.requests = []approval.Request{request}
	page.rebuildBrowser(request.ID)
	page.action = "approve"
	if err := page.initResolveEditor(request, true); err != nil {
		t.Fatal(err)
	}
	resolve := page.submitResolveForm()
	result := make(chan tea.Msg, 1)
	go func() { result <- resolve() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("approval request did not start")
	}
	updated, _ := page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	page = updated.(*RequestsPage)
	if page.overlay != requestOverlayNone || !page.operationCancelled {
		t.Fatalf("overlay=%d cancelled=%t", page.overlay, page.operationCancelled)
	}
	select {
	case message := <-result:
		updated, _ = page.Update(message)
		page = updated.(*RequestsPage)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("cancelled approval request did not return")
	}
	close(release)
	if page.operationCancelled || page.err != nil || !strings.Contains(page.notice, "cancelled") {
		t.Fatalf("cancelled=%t err=%v notice=%q", page.operationCancelled, page.err, page.notice)
	}
}

func TestRequestsPageRejectsStaleResolutionBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		current func(approval.Request) approval.Request
		want    string
	}{
		{name: "resolved", current: func(value approval.Request) approval.Request { value.Status = approval.StatusApproved; return value }, want: "approved"},
		{name: "expired", current: func(value approval.Request) approval.Request {
			value.ExpiresAt = time.Now().Add(-time.Second)
			return value
		}, want: "expired"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			initial := approval.Request{ID: "req_stale", Status: approval.StatusPending, WorkspaceID: "ws_a", TargetTool: "run_command", Title: "Allow update", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
			current := test.current(initial)
			resolveCalls := 0
			server := newRequestPageServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/requests/view":
					_ = json.NewEncoder(w).Encode(current)
				case "/requests/approve":
					resolveCalls++
					_ = json.NewEncoder(w).Encode(current)
				default:
					t.Fatalf("unexpected path=%s", r.URL.Path)
				}
			})
			defer server.Close()
			page, _ := NewRequests(t.Context(), "")
			page.action = "approve"
			if err := page.initResolveEditor(initial, true); err != nil {
				t.Fatal(err)
			}
			page.resolveForm.Reason = "keep this draft"
			resolve := page.submitResolveForm()
			if resolve == nil || !page.editor.Submitting() {
				t.Fatalf("resolve=%v submitting=%t", resolve != nil, page.editor.Submitting())
			}
			updated, follow := page.Update(resolve())
			page = updated.(*RequestsPage)
			view := ansi.Strip(page.View(42, 18))
			if follow != nil || resolveCalls != 0 || page.editor == nil || page.resolveForm == nil || page.resolveForm.Reason != "keep this draft" || page.editor.Submitting() || !strings.Contains(view, test.want) {
				t.Fatalf("follow=%v calls=%d editor=%v draft=%#v submitting=%t view=%q", follow != nil, resolveCalls, page.editor != nil, page.resolveForm, page.editor.Submitting(), view)
			}
		})
	}
}

func TestRequestsEditorsWrapAtNarrowWidths(t *testing.T) {
	create, _ := NewRequestsRouteAction(t.Context(), "", "", "", "create-test")
	if init := create.editor.Init(); init != nil {
		updated, _ := create.Update(init())
		create = updated.(*RequestsPage)
	}
	request := approval.Request{ID: "req_wrap", Status: approval.StatusPending, TargetTool: "run_command", ExpiresAt: time.Now().Add(time.Minute)}
	resolve, _ := NewRequests(t.Context(), "")
	resolve.action = "deny"
	if err := resolve.initResolveEditor(request, false); err != nil {
		t.Fatal(err)
	}
	if init := resolve.editor.Init(); init != nil {
		updated, _ := resolve.Update(init())
		resolve = updated.(*RequestsPage)
	}
	for name, page := range map[string]*RequestsPage{"create": create, "resolve": resolve} {
		t.Run(name, func(t *testing.T) {
			view := page.View(30, 18)
			for _, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got > 30 {
					t.Fatalf("line width=%d want <=30: %q", got, ansi.Strip(line))
				}
			}
		})
	}
}

func TestRequestArgumentsRenderExactValues(t *testing.T) {
	request := approval.Request{Arguments: []byte(`{"command":"printf 'a:b'","workspace_id":"ws_a","nested":{"enabled":true}}`)}
	view := requestArguments(request)
	for _, expected := range []string{`"command": "printf 'a:b'"`, `"workspace_id": "ws_a"`, `"enabled": true`} {
		if !strings.Contains(view, expected) {
			t.Fatalf("arguments missing %q: %q", expected, view)
		}
	}
}

func TestRequestRowsSearchExactCommandSeparatelyFromTitle(t *testing.T) {
	page, _ := NewRequests(t.Context(), "")
	page.requests = []approval.Request{{ID: "req_search", Status: approval.StatusPending, Title: "Update CodeMCP", Command: "cm update --channel beta"}}
	rows := page.requestRows()
	if len(rows) != 1 || !strings.Contains(rows[0].Search, "cm update --channel beta") || strings.Contains(rows[0].Title, "cm update") {
		t.Fatalf("row=%#v", rows)
	}
}

func newRequestPageServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		handler(w, r)
	}))
	previous := configformat.RootPath()
	t.Cleanup(func() { _ = configformat.SetRootPath(previous) })
	root := filepath.Join(t.TempDir(), "config")
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := runtimecontrol.State{PID: os.Getpid(), Address: parsed.Host, Token: "runtime-secret", ConfigRoot: root}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runtimecontrol.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	return server
}
