package telegram

import (
	"context"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

func (ui *Interface) approvalExplanationState(ctx context.Context, requestID string) (application.ApprovalExplainStatus, application.ApprovalExplanationResult, error) {
	value, err := ui.dispatch(ctx, capability.RequestExplainStatus, nil)
	if err != nil {
		return application.ApprovalExplainStatus{}, application.ApprovalExplanationResult{}, err
	}
	status, ok := value.(application.ApprovalExplainStatus)
	if !ok {
		return application.ApprovalExplainStatus{}, application.ApprovalExplanationResult{}, fmt.Errorf("approval Explain status returned an unexpected result")
	}
	value, err = ui.dispatch(ctx, capability.RequestExplanationView, application.ApprovalExplanationReadInput{ID: requestID})
	if err != nil {
		return status, application.ApprovalExplanationResult{}, err
	}
	result, ok := value.(application.ApprovalExplanationResult)
	if !ok {
		return status, application.ApprovalExplanationResult{}, fmt.Errorf("approval explanation returned an unexpected result")
	}
	return status, result, nil
}

func approvalExplanationBlocks(status application.ApprovalExplainStatus, result application.ApprovalExplanationResult, stateErr error) []RichBlock {
	if stateErr != nil {
		return []RichBlock{{Kind: RichSection, Title: "AI explanation", Text: "State unavailable: " + compactPresentationValue(stateErr.Error())}}
	}
	if !status.Available {
		reason := strings.TrimSpace(status.Reason)
		if reason == "" {
			reason = "Explanation is unavailable for the active LLM provider."
		}
		return []RichBlock{{Kind: RichSection, Title: "AI explanation", Text: reason}}
	}
	switch result.State {
	case application.ApprovalExplanationPending:
		return []RichBlock{{Kind: RichSection, Title: "AI explanation", Text: fmt.Sprintf("Generating · attempt %d", result.Attempt)}}
	case application.ApprovalExplanationFailed:
		failure := strings.TrimSpace(result.Failure)
		if failure == "" {
			failure = "Explanation generation failed."
		}
		return []RichBlock{{Kind: RichSection, Title: "AI explanation", Text: failure}}
	case application.ApprovalExplanationReady:
		if result.Explanation == nil {
			return []RichBlock{{Kind: RichSection, Title: "AI explanation", Text: "Explanation result is unavailable."}}
		}
		return aiExplanationBlocks(result.Explanation, "AI explanation", "It cannot approve or deny the request.")
	default:
		return []RichBlock{{Kind: RichSection, Title: "AI explanation", Text: "No AI explanation has been generated."}}
	}
}
