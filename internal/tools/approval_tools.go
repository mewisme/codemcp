package tools

import (
	"errors"
	"fmt"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

type approvalRequiredResponse struct {
	Code        string    `json:"code"`
	ChallengeID string    `json:"challenge_id"`
	WorkspaceID string    `json:"workspace_id"`
	TargetTool  string    `json:"target_tool"`
	Arguments   any       `json:"arguments"`
	GuardCode   string    `json:"guard_code"`
	Reason      string    `json:"reason"`
	Command     string    `json:"command,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	Instruction string    `json:"instruction"`
}

type approvalResolutionResponse struct {
	ID          string          `json:"id"`
	Status      approval.Status `json:"status"`
	WorkspaceID string          `json:"workspace_id"`
	TargetTool  string          `json:"target_tool"`
	Arguments   any             `json:"arguments"`
	RetryUntil  time.Time       `json:"retry_until,omitempty"`
	Instruction string          `json:"instruction"`
}

type approvalMismatchTarget struct {
	Tool      string `json:"tool"`
	Arguments any    `json:"arguments"`
}

type approvalMismatchResponse struct {
	Code        string                 `json:"code"`
	RequestID   string                 `json:"request_id"`
	Expected    approvalMismatchTarget `json:"expected"`
	Actual      approvalMismatchTarget `json:"actual"`
	Instruction string                 `json:"instruction"`
}

func approvalRequiredResult(challenge approval.Challenge) Result {
	arguments := approval.PublicArguments(challenge.TargetTool, challenge.Arguments)
	reason, command := challenge.GuardReason, challenge.Command
	if challenge.TargetTool == mcpconfigwire.SetToolName {
		reason = "CodeMCP configuration changes require local approval."
		command = ""
	}
	instruction := fmt.Sprintf(
		"Call %s again with exactly the original business arguments and add %s containing challenge_id %q and a concise human-readable action title. The title must describe the guarded action rather than the tool call and must not copy raw command arguments, flags, tokens, secrets, or IDs. If local approval is granted before the call deadline, that same call executes the action and returns its actual result.",
		challenge.TargetTool, InlineApprovalArgumentKey, challenge.ID,
	)
	response := approvalRequiredResponse{
		Code: "approval_required", ChallengeID: challenge.ID, WorkspaceID: challenge.WorkspaceID, TargetTool: challenge.TargetTool, Arguments: arguments,
		GuardCode: string(challenge.GuardCode), Reason: reason, Command: command, ExpiresAt: challenge.ExpiresAt, Instruction: instruction,
	}
	text := "This action requires local approval. " + instruction
	return Result{Content: []Content{{Type: "text", Text: text}}, StructuredContent: response, IsError: true, ResultType: "complete"}
}

func approvalResolutionResult(request approval.Request) Result {
	instruction := "Do not retry the guarded action."
	if request.Status == approval.StatusApproved {
		instruction = "Retry the target tool with exactly the original arguments before retry_until."
		if request.TargetTool != mcpconfigwire.SetToolName {
			instruction = "Retry the target tool with exactly these arguments before retry_until."
		}
	}
	response := approvalResolutionResponse{
		ID: request.ID, Status: request.Status, WorkspaceID: request.WorkspaceID, TargetTool: request.TargetTool, Arguments: approval.PublicArguments(request.TargetTool, request.Arguments),
		RetryUntil: request.RetryUntil, Instruction: instruction,
	}
	result := JSONResult(response)
	if request.Status != approval.StatusApproved {
		result.IsError = true
	}
	return result
}

func approvalMismatchResult(mismatch *approval.MismatchError) Result {
	if mismatch == nil {
		return ErrorResult(errors.New("approval retry does not match approved arguments"))
	}
	response := approvalMismatchResponse{
		Code: "approval_mismatch", RequestID: mismatch.RequestID,
		Expected:    approvalMismatchTarget{Tool: mismatch.TargetTool, Arguments: approval.PublicArguments(mismatch.TargetTool, mismatch.Expected)},
		Actual:      approvalMismatchTarget{Tool: mismatch.TargetTool, Arguments: approval.PublicArguments(mismatch.TargetTool, mismatch.Actual)},
		Instruction: "Retry the exact approved target and arguments, or abandon this approval request.",
	}
	result := JSONResult(response)
	result.IsError = true
	return result
}
