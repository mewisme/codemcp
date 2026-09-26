package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

const (
	CreateRuleToolName  = "create_rule"
	CreateSkillToolName = "create_skill"

	maxInstructionAuthoringErrorRunes = 1024
)

type InstructionAuthoringProvider interface {
	AuthorRule(context.Context, map[string]any) (any, error)
	AuthorSkill(context.Context, map[string]any) (any, error)
}

type instructionAuthoringSlot struct {
	mu       sync.RWMutex
	provider InstructionAuthoringProvider
}

func (r *Runtime) SetInstructionAuthoringProvider(provider InstructionAuthoringProvider) {
	if r == nil {
		return
	}
	r.instructionAuthoring.mu.Lock()
	r.instructionAuthoring.provider = provider
	r.instructionAuthoring.mu.Unlock()
}

func (r *Runtime) instructionAuthoringProvider() InstructionAuthoringProvider {
	if r == nil {
		return nil
	}
	r.instructionAuthoring.mu.RLock()
	defer r.instructionAuthoring.mu.RUnlock()
	return r.instructionAuthoring.provider
}

func RegisterInstructionAuthoringTools(registry *Registry, runtime *Runtime) {
	if registry == nil {
		return
	}
	register := func(name, title, description, input string, handler Handler) {
		registry.MustRegister(name, Schema{
			Name: name, Title: title, Description: description,
			InputSchema:  json.RawMessage(input),
			OutputSchema: json.RawMessage(instructionAuthoringOutputSchema()),
			Annotations:  ToolAnnotations(RiskEdit),
		}, handler)
	}

	register(
		CreateRuleToolName,
		"Create Rule",
		"Create or update a CodeMCP-native rule for one authorized workspace. CodeMCP selects the destination; global and provider-native authoring are unavailable through this agent tool.",
		`{"type":"object","properties":{"workspace_id":{"type":"string"},"mode":{"type":"string","enum":["create","update"]},"name":{"type":"string","minLength":1,"maxLength":64},"always_apply":{"type":"boolean","default":false},"globs":{"type":"array","items":{"type":"string"},"maxItems":64},"content":{"type":"string","minLength":1,"maxLength":4000},"dry_run":{"type":"boolean","default":false}},"required":["workspace_id","mode","name","content"],"additionalProperties":false}`,
		func(ctx context.Context, args map[string]any) (Result, error) {
			provider := runtime.instructionAuthoringProvider()
			if provider == nil {
				return boundedInstructionAuthoringError(errors.New("native instruction authoring is unavailable")), nil
			}
			request := cloneMap(args)
			request["scope"] = "workspace"
			value, err := provider.AuthorRule(ctx, request)
			if err != nil {
				return boundedInstructionAuthoringError(err), nil
			}
			return JSONResult(value), nil
		},
	)

	register(
		CreateSkillToolName,
		"Create Skill",
		"Create or update a CodeMCP-native skill for one authorized workspace. CodeMCP selects the destination; global and provider-native authoring are unavailable through this agent tool.",
		`{"type":"object","properties":{"workspace_id":{"type":"string"},"mode":{"type":"string","enum":["create","update"]},"name":{"type":"string","minLength":1,"maxLength":64},"description":{"type":"string","minLength":1,"maxLength":200},"instructions":{"type":"string","minLength":1,"maxLength":500000},"supporting_files":{"type":"array","maxItems":32,"items":{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string"},"executable":{"type":"boolean","default":false}},"required":["path","content"],"additionalProperties":false}},"dry_run":{"type":"boolean","default":false}},"required":["workspace_id","mode","name","description","instructions"],"additionalProperties":false}`,
		func(ctx context.Context, args map[string]any) (Result, error) {
			provider := runtime.instructionAuthoringProvider()
			if provider == nil {
				return boundedInstructionAuthoringError(errors.New("native instruction authoring is unavailable")), nil
			}
			request := cloneMap(args)
			request["scope"] = "workspace"
			value, err := provider.AuthorSkill(ctx, request)
			if err != nil {
				return boundedInstructionAuthoringError(err), nil
			}
			return JSONResult(value), nil
		},
	)
}

func boundedInstructionAuthoringError(err error) Result {
	if err == nil {
		err = errors.New("instruction authoring failed")
	}
	message := strings.TrimSpace(err.Error())
	runes := []rune(message)
	if len(runes) > maxInstructionAuthoringErrorRunes {
		message = string(runes[:maxInstructionAuthoringErrorRunes]) + "…"
	}
	return ErrorResult(errors.New(message))
}

func instructionAuthoringOutputSchema() string {
	return `{"type":"object","properties":{"kind":{"type":"string","enum":["rule","skill"]},"scope":{"type":"string","enum":["workspace"]},"mode":{"type":"string","enum":["create","update"]},"name":{"type":"string"},"path":{"type":"string"},"content_id":{"type":"string"},"dry_run":{"type":"boolean"}},"required":["kind","scope","mode","name","path","content_id","dry_run"],"additionalProperties":false}`
}
