package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	plandoc "go.mewis.me/codemcp/internal/plan"
)

const (
	CreatePlanToolName = "create_plan"

	maxPlanAuthoringErrorRunes = 1024
)

type PlanAuthoringProvider interface {
	AuthorPlan(context.Context, map[string]any) (any, error)
}

type planAuthoringSlot struct {
	mu       sync.RWMutex
	provider PlanAuthoringProvider
}

func (r *Runtime) SetPlanAuthoringProvider(provider PlanAuthoringProvider) {
	if r == nil {
		return
	}
	r.planAuthoring.mu.Lock()
	r.planAuthoring.provider = provider
	r.planAuthoring.mu.Unlock()
}

func (r *Runtime) planAuthoringProvider() PlanAuthoringProvider {
	if r == nil {
		return nil
	}
	r.planAuthoring.mu.RLock()
	defer r.planAuthoring.mu.RUnlock()
	return r.planAuthoring.provider
}

func RegisterPlanAuthoringTool(registry *Registry, runtime *Runtime) {
	if registry == nil {
		return
	}
	registry.MustRegister(CreatePlanToolName, Schema{
		Name:         CreatePlanToolName,
		Title:        "Create Plan",
		Description:  "Create or update one canonical implementation plan for an authorized workspace. Updates require the current content identity so stale sessions cannot overwrite newer progress. During an active plan implementation binding, progress updates may complete only the bound next phase, must synchronize the phase checklist with the embedded ordered phase, and only a successful non-dry-run canonical write closes that phase for agent completion.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"mode":{"type":"string","enum":["create","update"]},"name":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[a-z0-9][a-z0-9-]{0,63}$"},"plan_content":{"type":"string","minLength":1,"maxLength":524288},"implementation_order":{"type":"string","minLength":1,"maxLength":262144},"expected_content_id":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"dry_run":{"type":"boolean","default":false}},"required":["workspace_id","mode","name","plan_content","implementation_order"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"name":{"type":"string"},"content_id":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"status":{"type":"string","enum":["pending","in_progress","completed"]},"phase_count":{"type":"integer","minimum":1,"maximum":128},"completed_phase_count":{"type":"integer","minimum":0,"maximum":128},"next_phase":{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"}},"required":["id","title"],"additionalProperties":false},"dry_run":{"type":"boolean"}},"required":["path","name","content_id","status","phase_count","completed_phase_count","dry_run"],"additionalProperties":false}`),
		Annotations:  ToolAnnotations(RiskEdit),
		Capability:   toolCapability(CapabilityDomainPlans),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		var transition plandoc.ExecutionTransition
		transitionPrepared := false
		sessionKey := planExecutionSessionKey(ctx)
		dryRun, _ := args["dry_run"].(bool)
		if runtime != nil && runtime.PlanExecutions != nil && stringArgument(args, "mode") == "update" {
			workspaceID := stringArgument(args, "workspace_id")
			if _, ok := runtime.PlanExecutions.Lookup(sessionKey, workspaceID); ok {
				next, err := plandoc.ParseParts(stringArgument(args, "plan_content"), stringArgument(args, "implementation_order"))
				if err != nil {
					return boundedPlanAuthoringError(err), nil
				}
				transition, err = runtime.PlanExecutions.PrepareUpdate(sessionKey, workspaceID, stringArgument(args, "name"), stringArgument(args, "expected_content_id"), next)
				if err != nil {
					return boundedPlanAuthoringError(err), nil
				}
				transitionPrepared = true
			}
		}
		provider := runtime.planAuthoringProvider()
		if provider == nil {
			return boundedPlanAuthoringError(errors.New("plan authoring is unavailable")), nil
		}
		value, err := provider.AuthorPlan(ctx, cloneMap(args))
		if err != nil {
			return boundedPlanAuthoringError(err), nil
		}
		if transitionPrepared && !dryRun {
			if err := runtime.PlanExecutions.CommitUpdate(sessionKey, transition); err != nil {
				return boundedPlanAuthoringError(err), nil
			}
		}
		return JSONResult(value), nil
	})
}

func boundedPlanAuthoringError(err error) Result {
	if err == nil {
		err = errors.New("plan authoring failed")
	}
	message := strings.TrimSpace(err.Error())
	runes := []rune(message)
	if len(runes) > maxPlanAuthoringErrorRunes {
		message = string(runes[:maxPlanAuthoringErrorRunes]) + "…"
	}
	return ErrorResult(errors.New(message))
}
