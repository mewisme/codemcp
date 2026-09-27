package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type SemanticApprovalAction string

const (
	SemanticApprovalAllow           SemanticApprovalAction = "allow"
	SemanticApprovalRequireApproval SemanticApprovalAction = "require_approval"
	SemanticApprovalDeny            SemanticApprovalAction = "deny"
)

type SemanticApprovalPolicy struct {
	Enabled           bool
	Provider          string
	Timeout           time.Duration
	MinimumConfidence float64
	FailMode          SemanticApprovalAction
	Actions           map[semantic.RiskClass]SemanticApprovalAction
}

type semanticApprovalSlot struct {
	mu     sync.RWMutex
	policy SemanticApprovalPolicy
}

func DefaultSemanticApprovalPolicy() SemanticApprovalPolicy {
	return SemanticApprovalPolicy{
		Provider: "typesafe", Timeout: 1500 * time.Millisecond, MinimumConfidence: 0.8,
		FailMode: SemanticApprovalRequireApproval,
		Actions: map[semantic.RiskClass]SemanticApprovalAction{
			semantic.RiskLow: SemanticApprovalAllow, semantic.RiskMedium: SemanticApprovalRequireApproval,
			semantic.RiskHigh: SemanticApprovalRequireApproval, semantic.RiskCritical: SemanticApprovalDeny,
		},
	}
}

func (r *Runtime) SetSemanticApprovalPolicy(policy SemanticApprovalPolicy) {
	if r == nil {
		return
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 1500 * time.Millisecond
	}
	if policy.MinimumConfidence < 0 || policy.MinimumConfidence > 1 {
		policy.MinimumConfidence = 0.8
	}
	policy.Provider = strings.TrimSpace(policy.Provider)
	if policy.Provider == "" {
		policy.Provider = "typesafe"
	}
	if policy.FailMode != SemanticApprovalDeny {
		policy.FailMode = SemanticApprovalRequireApproval
	}
	defaults := DefaultSemanticApprovalPolicy()
	next := make(map[semantic.RiskClass]SemanticApprovalAction, len(defaults.Actions))
	for class, fallback := range defaults.Actions {
		action := policy.Actions[class]
		if action != SemanticApprovalAllow && action != SemanticApprovalRequireApproval && action != SemanticApprovalDeny {
			action = fallback
		}
		next[class] = action
	}
	policy.Actions = next
	r.semanticApproval.mu.Lock()
	r.semanticApproval.policy = policy
	r.semanticApproval.mu.Unlock()
}

func (r *Runtime) semanticApprovalPolicy() SemanticApprovalPolicy {
	if r == nil {
		return DefaultSemanticApprovalPolicy()
	}
	r.semanticApproval.mu.RLock()
	policy := r.semanticApproval.policy
	r.semanticApproval.mu.RUnlock()
	if policy.Provider == "" {
		return DefaultSemanticApprovalPolicy()
	}
	policy.Actions = cloneSemanticApprovalActions(policy.Actions)
	return policy
}

func (r *Runtime) semanticApprovalPreflight(ctx context.Context, correlation ApprovalCorrelation, workspaceID, tool string, args map[string]any, claimed bool) error {
	if r == nil || claimed || (tool != "run_command" && tool != "start_process") {
		return nil
	}
	policy := r.semanticApprovalPolicy()
	if !policy.Enabled {
		return nil
	}
	command, _ := args["command"].(string)
	if strings.TrimSpace(command) == "" || r.Shell == nil || r.Workspaces == nil {
		return nil
	}
	preview, err := r.Shell.PreviewCommand(ctx, workspaceID, command, tool == "start_process")
	if err != nil {
		if _, guarded := controlguard.As(err); guarded {
			return nil
		}
		return err
	}
	if !r.Workspaces.IsMutationCommand(preview.Security) {
		return nil
	}
	input := semantic.RiskInput{
		Consumer:    semantic.Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		WorkspaceID: workspaceID,
		CallerID:    strings.TrimSpace(correlation.CallerID),
		Invocation: semantic.CanonicalInvocation{
			Operation: tool,
			Tool:      tool,
			Arguments: map[string]any{"command": boundedSemanticCommand(preview.Effective)},
		},
	}
	assessment, classifyErr := r.classifySemanticApproval(ctx, policy, input)
	action := policy.FailMode
	reason := "semantic risk classification unavailable"
	if classifyErr == nil {
		action = policy.Actions[assessment.Class]
		reason = semanticAssessmentReason(assessment, action)
	}
	switch action {
	case SemanticApprovalAllow:
		return nil
	case SemanticApprovalDeny:
		return controlguard.New(controlguard.CodeSemanticRisk, reason, false, &controlguard.Invocation{Command: tracepkg.SanitizeCommand(command)})
	default:
		return controlguard.New(controlguard.CodeSemanticRisk, reason, true, &controlguard.Invocation{Command: tracepkg.SanitizeCommand(command)})
	}
}

func (r *Runtime) classifySemanticApproval(ctx context.Context, policy SemanticApprovalPolicy, input semantic.RiskInput) (semantic.RiskAssessment, error) {
	if r.Semantic == nil {
		return semantic.RiskAssessment{}, semantic.NewError(semantic.ErrorUnavailable, "")
	}
	health := r.Semantic.Health()
	if !health.Available || !strings.EqualFold(strings.TrimSpace(health.Provider), policy.Provider) {
		return semantic.RiskAssessment{}, semantic.NewError(semantic.ErrorUnavailable, "")
	}
	classifyCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
	defer cancel()
	return r.Semantic.ClassifyRisk(classifyCtx, input, policy.MinimumConfidence)
}

func semanticAssessmentReason(assessment semantic.RiskAssessment, action SemanticApprovalAction) string {
	category := strings.TrimSpace(assessment.Category)
	if category == "" {
		category = "uncategorized"
	}
	return fmt.Sprintf("semantic risk %s (%s): %s", assessment.Class, category, action)
}

func boundedSemanticCommand(command string) string {
	value := tracepkg.SanitizeCommand(strings.TrimSpace(command))
	const max = 4096
	if len(value) > max {
		value = value[:max]
	}
	return value
}

func cloneSemanticApprovalActions(source map[semantic.RiskClass]SemanticApprovalAction) map[semantic.RiskClass]SemanticApprovalAction {
	result := make(map[semantic.RiskClass]SemanticApprovalAction, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
