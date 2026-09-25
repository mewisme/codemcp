package tools

import (
	"context"
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/workspace"
)

func (r *Runtime) prepareApprovalRetry(ctx context.Context, correlation ApprovalCorrelation, workspaceID, source, name string, args map[string]any) (context.Context, approval.Request, *Result, error) {
	if r == nil || r.Approvals == nil || strings.TrimSpace(workspaceID) == "" || name == ApprovalRequestToolName {
		return ctx, approval.Request{}, nil, nil
	}
	command, _ := args["command"].(string)
	retry := approval.RetryInput{
		CallerID: correlation.CallerID, RequestID: correlation.RequestID, WorkspaceID: workspaceID, Source: source, TargetTool: name, Arguments: args, Command: command,
	}
	if granted, matched := r.Approvals.MatchRuntimeGrant(retry); matched {
		ctx = WithApprovalRequest(ctx, granted.ID)
		ctx = controlguard.WithGrant(ctx, controlguard.Grant{RequestID: granted.ID, Code: granted.GuardCode})
		return ctx, approval.Request{}, nil, nil
	}
	if strings.TrimSpace(correlation.CallerID) == "" || strings.TrimSpace(correlation.RequestID) == "" {
		return ctx, approval.Request{}, nil, nil
	}
	_, matched, err := r.Approvals.MatchApproved(retry)
	if err != nil {
		var mismatch *approval.MismatchError
		if errors.As(err, &mismatch) {
			result := approvalMismatchResult(mismatch)
			return ctx, approval.Request{}, &result, nil
		}
		return ctx, approval.Request{}, nil, err
	}
	if !matched {
		return ctx, approval.Request{}, nil, nil
	}
	if name != "run_command" && name != "start_process" {
		claimed, matched, err := r.Approvals.ClaimApproved(retry)
		if err != nil || !matched {
			return ctx, approval.Request{}, nil, err
		}
		ctx = WithApprovalRequest(ctx, claimed.ID)
		ctx = controlguard.WithGrant(ctx, controlguard.Grant{RequestID: claimed.ID, Code: claimed.GuardCode})
		return ctx, claimed, nil, nil
	}
	invocation, ok := workspace.DirectControlPlaneInvocation(command)
	if !ok || invocation == nil {
		claimed, matched, err := r.Approvals.ClaimApproved(retry)
		if err != nil || !matched {
			return ctx, approval.Request{}, nil, err
		}
		ctx = WithApprovalRequest(ctx, claimed.ID)
		ctx = controlguard.WithGrant(ctx, controlguard.Grant{RequestID: claimed.ID, Code: claimed.GuardCode})
		return ctx, claimed, nil, nil
	}
	claimed, capability, matched, err := r.Approvals.ClaimApprovedCLI(retry, approval.CLIInvocation{Program: invocation.Program, Args: invocation.Args})
	if err != nil || !matched {
		return ctx, approval.Request{}, nil, err
	}
	if capability == "" {
		return ctx, approval.Request{}, nil, errors.New("approved shell retry did not receive a child capability")
	}
	ctx = WithApprovalRequest(ctx, claimed.ID)
	ctx = controlguard.WithApproval(ctx, controlguard.Approval{RequestID: claimed.ID, Capability: capability, Invocation: *invocation})
	return ctx, claimed, nil, nil
}

func (r *Runtime) approvalResultForGuard(guard *controlguard.Error, correlation ApprovalCorrelation, sessionHash, workspaceID, source, name string, args map[string]any, claimed approval.Request) (Result, bool, error) {
	if guard == nil || !guard.Approvable || claimed.ID != "" || r == nil || r.Approvals == nil || strings.TrimSpace(correlation.CallerID) == "" || strings.TrimSpace(correlation.RequestID) == "" || strings.TrimSpace(workspaceID) == "" {
		return Result{}, false, nil
	}
	command := ""
	if guard.Invocation != nil {
		command = strings.TrimSpace(guard.Invocation.Command)
	}
	if command == "" {
		command, _ = args["command"].(string)
		command = strings.TrimSpace(command)
	}
	similarPattern := ""
	if command != "" {
		similarPattern, _ = workspace.SimilarCommandPattern(command)
	}
	challenge, _, err := r.Approvals.CreateChallenge(approval.ChallengeInput{
		CallerID: correlation.CallerID, RequestCorrelationID: correlation.RequestID, SessionHash: sessionHash, WorkspaceID: workspaceID, Source: source, TargetTool: name, Arguments: args,
		GuardCode: guard.Code, GuardReason: guard.Error(), Command: command, SimilarCommandPattern: similarPattern,
	})
	if err != nil {
		return Result{}, false, err
	}
	return approvalRequiredResult(challenge), true, nil
}
