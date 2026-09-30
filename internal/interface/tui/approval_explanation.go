package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

const approvalExplanationPollInterval = 1200 * time.Millisecond

type approvalExplanationMsg struct {
	requestID string
	status    application.ApprovalExplainStatus
	result    application.ApprovalExplanationResult
	err       error
}

type approvalExplanationPollMsg struct {
	requestID string
}

func (model *Model) prepareApprovalExplanation(requestID string) {
	if model == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	if model.approvalExplainID == requestID {
		return
	}
	model.approvalExplainID = requestID
	model.approvalExplainStatus = application.ApprovalExplainStatus{}
	model.approvalExplanation = application.ApprovalExplanationResult{RequestID: requestID, State: application.ApprovalExplanationNone}
	model.approvalExplainErr = nil
	model.approvalExplainLoading = true
}

func (model *Model) resetApprovalExplanation() {
	if model == nil {
		return
	}
	model.approvalExplainStatus = application.ApprovalExplainStatus{}
	model.approvalExplanation = application.ApprovalExplanationResult{}
	model.approvalExplainErr = nil
	model.approvalExplainLoading = false
	model.approvalExplainID = ""
}

func (model Model) loadActiveApprovalExplanationCmd() tea.Cmd {
	requestID := model.activeApprovalID()
	if strings.TrimSpace(requestID) == "" {
		return nil
	}
	ctx := model.ctx
	return func() tea.Msg {
		status, err := application.GetApprovalExplainStatus(ctx)
		if err != nil {
			return approvalExplanationMsg{requestID: requestID, err: err}
		}
		result, err := application.GetApprovalExplanation(ctx, requestID)
		return approvalExplanationMsg{requestID: requestID, status: status, result: result, err: err}
	}
}

func (model Model) triggerActiveApprovalExplanationCmd(retry bool) tea.Cmd {
	requestID := model.activeApprovalID()
	if strings.TrimSpace(requestID) == "" {
		return nil
	}
	ctx := model.ctx
	return func() tea.Msg {
		status, err := application.GetApprovalExplainStatus(ctx)
		if err != nil {
			return approvalExplanationMsg{requestID: requestID, err: err}
		}
		result, err := application.ExplainApprovalRequest(ctx, requestID, retry)
		return approvalExplanationMsg{requestID: requestID, status: status, result: result, err: err}
	}
}

func (model *Model) applyApprovalExplanation(msg approvalExplanationMsg) tea.Cmd {
	if model == nil || msg.requestID == "" || msg.requestID != model.activeApprovalID() {
		return nil
	}
	model.approvalExplainID = msg.requestID
	model.approvalExplainLoading = false
	if msg.err != nil {
		model.approvalExplainErr = msg.err
		model.syncApprovalViewport(false)
		return nil
	}
	model.approvalExplainErr = nil
	model.approvalExplainStatus = msg.status
	model.approvalExplanation = msg.result
	model.syncApprovalViewport(false)
	if !model.shouldPollApprovalExplanation() {
		return nil
	}
	requestID := msg.requestID
	return tea.Tick(approvalExplanationPollInterval, func(time.Time) tea.Msg {
		return approvalExplanationPollMsg{requestID: requestID}
	})
}

func (model Model) shouldPollApprovalExplanation() bool {
	if model.activeApprovalID() == "" || model.approvalExplainID != model.activeApprovalID() {
		return false
	}
	if model.approvalExplanation.State == application.ApprovalExplanationPending {
		return true
	}
	return strings.EqualFold(string(model.approvalExplainStatus.Mode), "auto") &&
		model.approvalExplainStatus.Available &&
		model.approvalExplanation.State == application.ApprovalExplanationNone
}

func (model Model) canTriggerApprovalExplanation() bool {
	if !model.approvalExplainStatus.Available || model.approvalExplainLoading {
		return false
	}
	switch model.approvalExplanation.State {
	case application.ApprovalExplanationNone, application.ApprovalExplanationFailed:
		return true
	default:
		return false
	}
}

func (model Model) approvalExplanationHint() string {
	if !model.canTriggerApprovalExplanation() {
		return ""
	}
	if model.approvalExplanation.State == application.ApprovalExplanationFailed {
		return "e retry explanation"
	}
	return "e explain"
}

func (model Model) approvalExplanationView(width int) string {
	lines := []string{component.WrapContent(component.Label("AI explanation · informational"), max(1, width))}
	switch {
	case model.approvalExplainLoading:
		lines = append(lines, component.WrapContent(component.Muted("Loading explanation state..."), max(1, width)))
	case model.approvalExplainErr != nil:
		lines = append(lines, component.BannerWidth(model.approvalExplainErr.Error(), component.ToneWarning, max(1, width)))
	case !model.approvalExplainStatus.Available:
		reason := strings.TrimSpace(model.approvalExplainStatus.Reason)
		if reason == "" {
			reason = "Explanation is unavailable for the active LLM provider."
		}
		lines = append(lines, component.WrapContent(component.Muted(reason), max(1, width)))
	default:
		switch model.approvalExplanation.State {
		case application.ApprovalExplanationPending:
			lines = append(lines, component.WrapContent(component.Muted(fmt.Sprintf("Generating · attempt %d", model.approvalExplanation.Attempt)), max(1, width)))
		case application.ApprovalExplanationFailed:
			failure := strings.TrimSpace(model.approvalExplanation.Failure)
			if failure == "" {
				failure = "Explanation generation failed."
			}
			lines = append(lines, component.BannerWidth(failure, component.ToneWarning, max(1, width)), component.WrapContent(component.Muted("Press e to retry."), max(1, width)))
		case application.ApprovalExplanationReady:
			if explanation := model.approvalExplanation.Explanation; explanation != nil {
				lines = append(lines, component.WrapContent(explanation.Summary, max(1, width)))
				lines = appendApprovalExplanationItems(lines, "Steps", explanation.Steps, width)
				lines = appendApprovalExplanationItems(lines, "Likely effects", explanation.Effects, width)
				lines = appendApprovalExplanationItems(lines, "Risk notes", explanation.RiskNotes, width)
				lines = appendApprovalExplanationItems(lines, "Unknowns", explanation.Unknowns, width)
				meta := strings.Trim(strings.Join([]string{string(explanation.ProviderID), explanation.Model}, " · "), " ·")
				if meta != "" {
					lines = append(lines, "", component.WrapContent(component.Muted("Generated by "+meta), max(1, width)))
				}
			}
		default:
			lines = append(lines, component.WrapContent(component.Muted("No AI explanation has been generated. Press e to explain."), max(1, width)))
		}
	}
	return strings.Join(lines, "\n")
}

func appendApprovalExplanationItems(lines []string, label string, values []string, width int) []string {
	if len(values) == 0 {
		return lines
	}
	lines = append(lines, "", component.Muted(label))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			lines = append(lines, component.WrapContent("• "+value, max(1, width)))
		}
	}
	return lines
}
