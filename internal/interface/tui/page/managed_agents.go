package page

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	managedagent "go.mewis.me/codemcp/internal/agent"
	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

const managedAgentPageTimeout = 15 * time.Second

type managedAgentPageResultMsg struct {
	value any
	err   error
}

type ManagedAgentPage struct {
	ctx        context.Context
	resourceID string
	action     string
	value      any
	err        error
	loading    bool
	notice     string
	editor     *component.Editor
	spawn      *managedAgentSpawnFormData
	send       *managedAgentSendFormData
}

func NewManagedAgentsRoute(ctx context.Context, resourceID, action string) (*ManagedAgentPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	page := &ManagedAgentPage{ctx: ctx, resourceID: strings.TrimSpace(resourceID), action: strings.TrimSpace(action)}
	switch page.action {
	case "":
	case "spawn":
		editor, data := newManagedAgentSpawnEditor()
		page.editor, page.spawn = &editor, data
	case "send":
		if page.resourceID == "" {
			return nil, fmt.Errorf("managed agent id is required for send")
		}
		editor, data := newManagedAgentSendEditor(page.resourceID)
		page.editor, page.send = &editor, data
	case "wait", "cancel":
		if page.resourceID == "" {
			return nil, fmt.Errorf("managed agent id is required for %s", page.action)
		}
	default:
		return nil, fmt.Errorf("unsupported managed agent action %q", page.action)
	}
	return page, nil
}

func (page *ManagedAgentPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	if page.editor != nil {
		return page.editor.Init()
	}
	page.loading = true
	return page.loadCmd()
}

func (page *ManagedAgentPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case managedAgentPageResultMsg:
		page.loading = false
		page.value, page.err = msg.value, msg.err
		if page.editor != nil {
			page.editor.SetSubmitting(false)
			page.editor.SetFeedback(page.notice, msg.err)
			if msg.err == nil {
				page.editor.Accept()
				page.editor = nil
			}
		}
		return page, nil
	case component.EditorSubmitMsg:
		if page.editor == nil || page.editor.Submitting() {
			return page, nil
		}
		if err := page.editor.Validate(); err != nil {
			page.editor.SetFeedback("", err)
			return page, nil
		}
		page.editor.SetSubmitting(true)
		return page, page.submitCmd()
	case component.EditorCancelMsg:
		page.editor = nil
		page.notice = "Edit cancelled"
		return page, page.loadCmd()
	case tea.KeyPressMsg:
		if page.editor == nil && msg.String() == "r" {
			page.loading = true
			return page, page.loadCmd()
		}
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return page, cmd
	}
	return page, nil
}

func (page *ManagedAgentPage) View(width, height int) string {
	if page == nil {
		return ""
	}
	if page.editor != nil {
		page.editor.Resize(width, height)
		return page.editor.View()
	}
	if page.loading {
		return component.StateView(component.PageLoading, "Loading managed agents", "")
	}
	if page.err != nil {
		return component.StateView(component.PageError, "Managed agents unavailable", page.err.Error())
	}
	data, err := json.MarshalIndent(page.value, "", "  ")
	if err != nil {
		return component.StateView(component.PageError, "Managed agent result unavailable", err.Error())
	}
	title := "Managed Agents"
	if page.resourceID != "" {
		title = "Managed Agent · " + page.resourceID
	}
	body := component.PageTitleNotice(title, page.notice, width) + "\n" + component.RenderCodeBlock(string(data), "json", width)
	help := component.DefaultHelp(width, component.Binding([]string{"r"}, "r", "refresh"))
	return component.BottomHelp(body, help, width, height)
}

func (page *ManagedAgentPage) OverlayActive() bool { return false }
func (page *ManagedAgentPage) InputActive() bool   { return page != nil && page.editor != nil }
func (page *ManagedAgentPage) Dirty() bool {
	return page != nil && page.editor != nil && page.editor.Dirty()
}

func (page *ManagedAgentPage) loadCmd() tea.Cmd {
	if page == nil {
		return nil
	}
	ctx := page.ctx
	resourceID, action := page.resourceID, page.action
	return func() tea.Msg {
		var value any
		var err error
		switch {
		case action == "wait":
			var snapshot managedagent.Snapshot
			err = managedAgentPageRequest(ctx, http.MethodPost, "/agents/wait", application.ManagedAgentWaitInput{
				AgentID: resourceID, TimeoutMS: int(managedagent.MaxWaitDuration / time.Millisecond),
			}, &snapshot)
			value = snapshot
		case action == "cancel":
			var snapshot managedagent.Snapshot
			err = managedAgentPageRequest(ctx, http.MethodPost, "/agents/cancel", application.ManagedAgentIDInput{AgentID: resourceID}, &snapshot)
			value = snapshot
		case resourceID != "":
			var snapshot managedagent.Snapshot
			err = managedAgentPageRequest(ctx, http.MethodGet, "/agents/get?id="+url.QueryEscape(resourceID), nil, &snapshot)
			value = snapshot
		default:
			var snapshots []managedagent.Snapshot
			err = managedAgentPageRequest(ctx, http.MethodGet, "/agents", nil, &snapshots)
			value = snapshots
		}
		return managedAgentPageResultMsg{value: value, err: err}
	}
}

func (page *ManagedAgentPage) submitCmd() tea.Cmd {
	if page == nil {
		return nil
	}
	ctx := page.ctx
	if page.spawn != nil {
		input := page.spawn.Input()
		page.notice = "Managed agent spawned"
		return func() tea.Msg {
			var snapshot managedagent.Snapshot
			err := managedAgentPageRequest(ctx, http.MethodPost, "/agents/spawn", input, &snapshot)
			return managedAgentPageResultMsg{value: snapshot, err: err}
		}
	}
	if page.send != nil {
		input := page.send.Input(page.resourceID)
		page.notice = "Managed agent follow-up sent"
		return func() tea.Msg {
			var snapshot managedagent.Snapshot
			err := managedAgentPageRequest(ctx, http.MethodPost, "/agents/send", input, &snapshot)
			return managedAgentPageResultMsg{value: snapshot, err: err}
		}
	}
	return nil
}

func managedAgentPageRequest(ctx context.Context, method, path string, input, output any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, managedAgentPageTimeout)
	defer cancel()
	_, err := runtimecontrol.Request(callCtx, method, path, input, output)
	return err
}
