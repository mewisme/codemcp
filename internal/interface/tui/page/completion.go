package page

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type completionSubscribeMsg struct {
	sub      *application.CompletionSubscription
	snapshot application.CompletionStateSnapshot
	err      error
}

type completionEventMsg struct {
	event agentcompletion.Event
	err   error
}

type completionViewMsg struct {
	record agentcompletion.Record
	err    error
}

type CompletionPage struct {
	ctx        context.Context
	cancel     context.CancelFunc
	resourceID string
	sub        *application.CompletionSubscription
	records    []agentcompletion.Record
	record     agentcompletion.Record
	loaded     bool
	loading    bool
	err        error
}

func NewCompletionsRoute(ctx context.Context, resourceID string) (*CompletionPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pageCtx, cancel := context.WithCancel(ctx)
	return &CompletionPage{ctx: pageCtx, cancel: cancel, resourceID: strings.TrimSpace(resourceID)}, nil
}

func (page *CompletionPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	page.loading = true
	if page.resourceID != "" {
		return page.viewCmd()
	}
	return page.subscribeCmd()
}

func (page *CompletionPage) OverlayActive() bool { return false }
func (page *CompletionPage) InputActive() bool   { return false }

func (page *CompletionPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case completionSubscribeMsg:
		page.loading = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		if page.sub != nil {
			_ = page.sub.Close()
		}
		page.sub = msg.sub
		page.records = append([]agentcompletion.Record(nil), msg.snapshot.Records...)
		page.loaded, page.err = true, nil
		return page, page.nextEventCmd()
	case completionEventMsg:
		if msg.err != nil {
			if page.ctx.Err() == nil {
				page.err = msg.err
			}
			return page, nil
		}
		page.upsert(msg.event.Record)
		page.loaded, page.err = true, nil
		return page, page.nextEventCmd()
	case completionViewMsg:
		page.loading = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.record, page.loaded, page.err = msg.record, true, nil
		return page, nil
	case tea.KeyPressMsg:
		if msg.String() == "r" {
			page.loading = true
			page.err = nil
			if page.resourceID != "" {
				return page, page.viewCmd()
			}
			if page.sub != nil {
				_ = page.sub.Close()
				page.sub = nil
			}
			return page, page.subscribeCmd()
		}
	}
	return page, nil
}

func (page *CompletionPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Completion history unavailable", "")
	}
	help := component.DefaultHelp(width, component.Binding([]string{"r"}, "r", "refresh"))
	if !page.loaded && page.loading {
		return component.BottomHelp(component.StateView(component.PageLoading, "Loading agent completion history", ""), help, width, height)
	}
	if page.err != nil {
		return component.BottomHelp(component.PageTitle("Agent Completions", width)+"\n"+component.BannerWidth(page.err.Error(), component.ToneDanger, width), help, width, height)
	}
	if page.resourceID != "" {
		return component.BottomHelp(page.renderRecord(width, page.record), help, width, height)
	}
	if len(page.records) == 0 {
		return component.BottomHelp(component.PageTitle("Agent Completions", width)+"\n"+component.StateView(component.PageEmpty, "No accepted agent completions", ""), help, width, height)
	}
	lines := []string{component.PageTitle("Agent Completions", width), component.Divider(width)}
	start := 0
	if len(page.records) > 20 {
		start = len(page.records) - 20
	}
	for index := len(page.records) - 1; index >= start; index-- {
		record := page.records[index]
		lines = append(lines,
			fmt.Sprintf("%s  %s  %s", completionPageStatus(record.Status), record.ID, record.Title),
			fmt.Sprintf("  workspace %s · %s", record.WorkspaceID, completionPageTime(record.CreatedAt)),
		)
		if strings.TrimSpace(record.Summary) != "" {
			lines = append(lines, "  "+record.Summary)
		}
		if index > start {
			lines = append(lines, "")
		}
	}
	return component.BottomHelp(strings.Join(lines, "\n"), help, width, height)
}

func (page *CompletionPage) Close() {
	if page == nil {
		return
	}
	if page.cancel != nil {
		page.cancel()
	}
	if page.sub != nil {
		_ = page.sub.Close()
		page.sub = nil
	}
}

func (page *CompletionPage) subscribeCmd() tea.Cmd {
	ctx := page.ctx
	return func() tea.Msg {
		sub, snapshot, err := application.SubscribeCompletions(ctx, "", 50)
		return completionSubscribeMsg{sub: sub, snapshot: snapshot, err: err}
	}
}

func (page *CompletionPage) nextEventCmd() tea.Cmd {
	sub := page.sub
	if sub == nil {
		return nil
	}
	return func() tea.Msg {
		event, err := sub.Next()
		return completionEventMsg{event: event, err: err}
	}
}

func (page *CompletionPage) viewCmd() tea.Cmd {
	ctx, id := page.ctx, page.resourceID
	return func() tea.Msg {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		record, err := application.ViewCompletion(readCtx, id)
		return completionViewMsg{record: record, err: err}
	}
}

func (page *CompletionPage) upsert(record agentcompletion.Record) {
	for index := range page.records {
		if page.records[index].ID == record.ID {
			page.records[index] = record
			return
		}
	}
	page.records = append(page.records, record)
	if len(page.records) > 50 {
		page.records = append([]agentcompletion.Record(nil), page.records[len(page.records)-50:]...)
	}
}

func (page *CompletionPage) renderRecord(width int, record agentcompletion.Record) string {
	lines := []string{
		component.PageTitle("Agent Completion", width),
		component.Divider(width),
		detailFields(
			[2]string{"ID", record.ID},
			[2]string{"Status", string(record.Status)},
			[2]string{"Workspace", record.WorkspaceID},
			[2]string{"Agent", record.AgentID},
			[2]string{"Sequence", fmt.Sprint(record.Sequence)},
			[2]string{"Source", record.Source},
			[2]string{"Created", completionPageTime(record.CreatedAt)},
		),
		component.Divider(width),
		record.Title,
	}
	if strings.TrimSpace(record.Summary) != "" {
		lines = append(lines, record.Summary)
	}
	if record.SupersedesID != "" {
		lines = append(lines, "", "Supersedes: "+record.SupersedesID)
	}
	return strings.Join(lines, "\n")
}

func completionPageStatus(status agentcompletion.Status) string {
	switch status {
	case agentcompletion.StatusCompleted:
		return "completed"
	case agentcompletion.StatusPartial:
		return "partial"
	case agentcompletion.StatusBlocked:
		return "blocked"
	case agentcompletion.StatusCancelled:
		return "cancelled"
	default:
		return string(status)
	}
}

func completionPageTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}
