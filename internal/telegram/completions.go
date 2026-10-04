package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
)

const completionHistoryLimit = 24

type CompletionListResolver func(context.Context, string, int) ([]agentcompletion.Record, error)
type CompletionViewResolver func(context.Context, string) (agentcompletion.Record, error)

func (ui *Interface) handleCompletions(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen := ui.routeScreen(ctx, owner, ActionState{Route: RouteCompletions, Back: RouteHome})
	_, _ = ui.runtime.SendRichMessageToTopic(ctx, owner.ChatID, TopicCompletions, screen, RichMessageOptions{})
}

func (ui *Interface) completionListScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	if ui == nil || ui.completionList == nil {
		return Screen{}, errors.New("agent completion history is unavailable")
	}
	records, err := ui.completionList(ctx, "", completionHistoryLimit)
	if err != nil {
		return Screen{}, err
	}
	records = append([]agentcompletion.Record(nil), records...)
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Sequence != records[j].Sequence {
			return records[i].Sequence > records[j].Sequence
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	start, end, page, pages := PageBounds(len(records), state.Page, domainPageSize)
	list := make([]string, 0, end-start)
	buttons := make([]Button, 0, end-start)
	for _, record := range records[start:end] {
		list = append(list, completionListLabel(record))
		label := strings.TrimSpace(record.Title)
		if label == "" {
			label = record.ID
		}
		button, buttonErr := ui.stateButton(owner, CompactResourceLabel(label), CallbackOpen, ActionState{
			Route: RouteCompletion, Back: RouteCompletions, ResourceID: record.ID,
		})
		if buttonErr != nil {
			return Screen{}, buttonErr
		}
		button.Role = ButtonRoleResource
		buttons = append(buttons, button)
	}

	blocks := []RichBlock{{
		Kind: RichHeading, Title: "Agent completions",
		Text: fmt.Sprintf("%d recent accepted completions · newest first", len(records)),
	}}
	if len(records) == 0 {
		blocks = append(blocks, RichBlock{Kind: RichSection, Title: "No completions", Text: "No accepted agent completion has been recorded yet."})
	} else {
		latest := records[0]
		blocks = append(blocks,
			RichBlock{Kind: RichSection, Title: "Latest", Text: completionListLabel(latest)},
			RichBlock{Kind: RichList, Items: list},
		)
	}
	navigation, err := ui.domainPaginationButtons(owner, state, page, pages)
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, state)
	if err != nil {
		return Screen{}, err
	}
	navigation = append([]Button{refresh}, navigation...)
	current, err := ui.stateButton(owner, "Current", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteCompletions, Operation: capability.CompletionCurrent,
		Input: application.CompletionWorkspaceInput{},
	})
	if err != nil {
		return Screen{}, err
	}
	doctor, err := ui.stateButton(owner, "Doctor", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteCompletions, Operation: capability.CompletionDoctor,
		Input: application.CompletionWorkspaceInput{},
	})
	if err != nil {
		return Screen{}, err
	}
	feed, err := ui.stateButton(owner, "Feed snapshot", CallbackOpen, ActionState{
		Route: RouteOperation, Back: RouteCompletions, Operation: capability.CompletionFeed,
		Input: application.CompletionWorkspaceInput{Limit: completionHistoryLimit},
	})
	if err != nil {
		return Screen{}, err
	}
	return Screen{
		Rich:     BuildRichPresentation(blocks...),
		Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{current, doctor, feed}, Secondary: buttons, Navigation: navigation}),
	}, nil
}

func (ui *Interface) completionDetailScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	if ui == nil || ui.completionView == nil {
		return Screen{}, errors.New("agent completion history is unavailable")
	}
	id := strings.TrimSpace(state.ResourceID)
	if id == "" {
		return Screen{}, errors.New("completion ID is required")
	}
	record, err := ui.completionView(ctx, id)
	if err != nil {
		return Screen{}, err
	}
	back, err := ui.backButton(owner, RouteCompletions)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteCompletion, Back: RouteCompletions, ResourceID: record.ID})
	if err != nil {
		return Screen{}, err
	}
	primary := []Button{}
	if logs, ok := ui.currentLogsWebAppButton(); ok {
		primary = append(primary, logs)
	}
	return Screen{
		Rich: completionRecordPresentation(record),
		Keyboard: BoundedActionGroups(ActionGroups{
			Primary:    primary,
			Navigation: []Button{back, home, refresh},
		}),
	}, nil
}

func completionRecordPresentation(record agentcompletion.Record) *RichPresentation {
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = "Agent completion"
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: title},
		StateBlock(statusTone(string(record.Status)), displayState(string(record.Status)), ""),
	}
	if summary := strings.TrimSpace(record.Summary); summary != "" {
		blocks = append(blocks, RichBlock{Kind: RichSection, Title: "Summary", Text: summary})
	}
	rows := [][]string{
		{"Workspace", record.WorkspaceID},
	}
	if source := strings.TrimSpace(record.Source); source != "" {
		rows = append(rows, []string{"Source", source})
	}
	if !record.CreatedAt.IsZero() {
		rows = append(rows, []string{"Completed", formatNotificationTime(record.CreatedAt)})
	}
	blocks = append(blocks,
		RichBlock{Kind: RichFields, Title: "Completion", Rows: rows},
		RichBlock{Kind: RichCopy, Title: "Completion ID", Text: record.ID, CopyText: record.ID},
	)
	if supersedes := strings.TrimSpace(record.SupersedesID); supersedes != "" {
		blocks = append(blocks, RichBlock{Kind: RichCopy, Title: "Supersedes", Text: supersedes, CopyText: supersedes})
	}
	return BuildRichPresentation(blocks...)
}

func completionListLabel(record agentcompletion.Record) string {
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = record.ID
	}
	parts := make([]string, 0, 3)
	if status := strings.TrimSpace(string(record.Status)); status != "" {
		parts = append(parts, displayState(status))
	}
	if workspace := strings.TrimSpace(record.WorkspaceID); workspace != "" {
		parts = append(parts, workspace)
	}
	if !record.CreatedAt.IsZero() {
		parts = append(parts, record.CreatedAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if len(parts) == 0 {
		return title
	}
	return title + "\n" + strings.Join(parts, " · ")
}

func (ui *Interface) currentLogsWebAppButton() (Button, bool) {
	if ui == nil || ui.runtime == nil {
		return Button{}, false
	}
	health := ui.runtime.Health().LogsMiniApp
	if health.State != MiniAppReady || strings.TrimSpace(health.PublicURL) == "" {
		return Button{}, false
	}
	return Button{Text: "Open Activity", WebAppURL: health.PublicURL, Role: ButtonRolePrimary}, true
}

func (ui *Interface) currentLogsWebAppExecutionButton(executionID string) (Button, bool) {
	button, ok := ui.currentLogsWebAppButton()
	if !ok {
		return Button{}, false
	}
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return button, true
	}
	parsed, err := url.Parse(button.WebAppURL)
	if err != nil {
		return button, true
	}
	query := parsed.Query()
	query.Set("feed", "executions")
	query.Set("execution", executionID)
	parsed.RawQuery = query.Encode()
	button.Text = "Open process log"
	button.WebAppURL = parsed.String()
	return button, true
}
