package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/notification"
)

func (ui *Interface) RenderNotification(ctx context.Context, chatID int64, message notification.Message) (Screen, bool, error) {
	switch message.Kind {
	case notification.KindApprovalPending, notification.KindApprovalResolved, notification.KindApprovalUpdated:
		return ui.renderApprovalNotification(ctx, chatID, message)
	case notification.KindCompletionAccepted:
		if ui == nil || ui.runtime == nil {
			return Screen{}, false, nil
		}
		return ui.completionNotificationScreen(message), true, nil
	case notification.KindBackgroundJobFinished:
		if ui == nil || ui.runtime == nil {
			return Screen{}, false, nil
		}
		return ui.backgroundProcessNotificationScreen(message), true, nil
	default:
		return Screen{}, false, nil
	}
}

func (ui *Interface) completionNotificationScreen(message notification.Message) Screen {
	status := strings.TrimSpace(message.Status)
	heading := strings.TrimSpace(message.Title)
	if heading == "" {
		heading = "Agent completed"
	}
	blocks := []RichBlock{{Kind: RichHeading, Title: heading}}

	subject := strings.TrimSpace(message.Subject)
	summary := strings.TrimSpace(message.Summary)
	if subject == "" && summary == "" {
		summary = strings.TrimSpace(message.Body)
	}
	switch {
	case subject != "" && summary != "":
		blocks = append(blocks, RichBlock{Kind: RichSection, Title: subject, Text: summary})
	case subject != "":
		blocks = append(blocks, RichBlock{Kind: RichSection, Title: subject})
	case summary != "":
		blocks = append(blocks, RichBlock{Kind: RichSection, Title: "Summary", Text: summary})
	}

	rows := make([][]string, 0, 4)
	if status != "" {
		blocks = append(blocks, StateBlock(statusTone(status), displayState(status), ""))
	}
	if workspace := strings.TrimSpace(message.WorkspaceID); workspace != "" {
		rows = append(rows, []string{"Workspace", workspace})
	}
	if !message.Timestamp.IsZero() {
		rows = append(rows, []string{"Completed", formatNotificationTime(message.Timestamp)})
	}
	if len(rows) > 0 {
		blocks = append(blocks, RichBlock{Kind: RichFields, Title: "Completion", Rows: rows})
	}
	if id := strings.TrimSpace(message.CompletionID); id != "" {
		blocks = append(blocks, RichBlock{Kind: RichCopy, Title: "Completion ID", Text: id, CopyText: id})
	}
	return Screen{Rich: BuildRichPresentation(blocks...)}
}

func (ui *Interface) backgroundProcessNotificationScreen(message notification.Message) Screen {
	heading := strings.TrimSpace(message.Title)
	if heading == "" {
		heading = "Background process finished"
	}
	blocks := []RichBlock{{Kind: RichHeading, Title: heading}}
	status := strings.TrimSpace(message.Status)
	if status != "" {
		blocks = append(blocks, StateBlock(statusTone(status), displayState(status), ""))
	}

	rows := make([][]string, 0, 8)
	appendRow := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			rows = append(rows, []string{label, value})
		}
	}
	appendRow("Tool", message.TargetTool)
	appendRow("Reason", message.Reason)
	appendRow("Workspace", message.WorkspaceID)
	if message.DurationMS > 0 {
		appendRow("Duration", formatNotificationDuration(message.DurationMS))
	}
	if message.ExitCode != nil {
		appendRow("Exit code", strconv.Itoa(*message.ExitCode))
	}
	appendRow("Signal", message.Signal)
	if !message.Timestamp.IsZero() {
		appendRow("Finished", formatNotificationTime(message.Timestamp))
	}
	if len(rows) > 0 {
		blocks = append(blocks, RichBlock{Kind: RichFields, Title: "Process", Rows: rows})
	}
	if id := strings.TrimSpace(message.ProcessID); id != "" {
		blocks = append(blocks, RichBlock{Kind: RichCopy, Title: "Process ID", Text: id, CopyText: id})
	}
	if id := strings.TrimSpace(message.ExecutionID); id != "" {
		blocks = append(blocks, RichBlock{Kind: RichCopy, Title: "Execution ID", Text: id, CopyText: id})
	}
	blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Details", Text: "Command and output are omitted from notifications. Open the process log for retained execution details."})
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: ui.notificationInspectKeyboard(message.ExecutionID)}
}

func (ui *Interface) notificationInspectKeyboard(executionID string) [][]Button {
	button, ok := ui.currentLogsWebAppExecutionButton(executionID)
	if !ok {
		return nil
	}
	return BoundedActionGroups(ActionGroups{Primary: []Button{button}})
}

func formatNotificationTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func formatNotificationDuration(milliseconds int64) string {
	if milliseconds <= 0 {
		return ""
	}
	duration := time.Duration(milliseconds) * time.Millisecond
	if duration < time.Second {
		return fmt.Sprintf("%d ms", milliseconds)
	}
	return duration.Round(time.Millisecond).String()
}
