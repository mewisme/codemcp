package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/tools"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type rejectingInstructionAuthoringProvider struct{}

func (rejectingInstructionAuthoringProvider) AuthorRule(context.Context, map[string]any) (any, error) {
	return nil, errors.New(strings.Repeat("x", 5000))
}

func (rejectingInstructionAuthoringProvider) AuthorSkill(context.Context, map[string]any) (any, error) {
	return nil, errors.New(strings.Repeat("y", 5000))
}

func newInstructionAuthoringRuntime(t *testing.T) (*tools.Runtime, string, string) {
	t.Helper()
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	root := t.TempDir()
	item, err := runtime.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	runtime.SetInstructionAuthoringProvider(application.NewAgentInstructionAuthoringProvider(runtime.Workspaces))
	return runtime, item.ID, root
}

func TestInstructionAuthoringToolsUseCanonicalWorkspaceOwnerAndExposeBuiltinGuidance(t *testing.T) {
	runtime, workspaceID, root := newInstructionAuthoringRuntime(t)
	store := workspacestate.New(root)

	for _, name := range []string{tools.CreateRuleToolName, tools.CreateSkillToolName} {
		schema, ok := runtime.Registry.Schema(name)
		if !ok {
			t.Fatalf("missing %s schema", name)
		}
		var input map[string]any
		if err := json.Unmarshal(schema.InputSchema, &input); err != nil {
			t.Fatal(err)
		}
		properties, _ := input["properties"].(map[string]any)
		if _, exists := properties["scope"]; exists {
			t.Fatalf("%s exposes caller-controlled scope", name)
		}
		if input["additionalProperties"] != false {
			t.Fatalf("%s permits undeclared authoring fields: %#v", name, input)
		}
	}

	ruleResult, err := runtime.Call(context.Background(), tools.CreateRuleToolName, map[string]any{
		"workspace_id": workspaceID,
		"mode":         "create",
		"name":         "agent-rule",
		"always_apply": true,
		"content":      "agent-authored rule",
	})
	if err != nil || ruleResult.IsError {
		t.Fatalf("create_rule err=%v result=%#v", err, ruleResult)
	}
	if _, err := os.Stat(filepath.Join(store.RulesRoot(), "agent-rule.md")); err != nil {
		t.Fatal(err)
	}

	skillResult, err := runtime.Call(context.Background(), tools.CreateSkillToolName, map[string]any{
		"workspace_id": workspaceID,
		"mode":         "create",
		"name":         "agent-skill",
		"description":  "Agent authored skill",
		"instructions": "agent skill body",
		"supporting_files": []any{
			map[string]any{"path": "scripts/check.sh", "content": "#!/bin/sh\nexit 0\n", "executable": true},
		},
	})
	if err != nil || skillResult.IsError {
		t.Fatalf("create_skill err=%v result=%#v", err, skillResult)
	}
	if _, err := os.Stat(filepath.Join(store.SkillsRoot(), "agent-skill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	listResult, err := runtime.Call(context.Background(), "list_skills", map[string]any{"workspace_id": workspaceID})
	if err != nil || listResult.IsError {
		t.Fatalf("list_skills err=%v result=%#v", err, listResult)
	}
	var listed tools.SkillsListResult
	if err := json.Unmarshal([]byte(listResult.Content[0].Text), &listed); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	agentSkillListed := false
	for _, skill := range listed.Skills {
		if skill.Name == "agent-skill" {
			agentSkillListed = true
		}
		if skill.Name == skills.BuiltinCreateRuleName || skill.Name == skills.BuiltinCreateSkillName || skill.Name == skills.BuiltinCreatePlanName {
			seen[skill.Name]++
			if !skills.IsBuiltin(skill) {
				t.Fatalf("reserved guidance is not builtin: %#v", skill)
			}
		}
	}
	if seen[skills.BuiltinCreateRuleName] != 1 || seen[skills.BuiltinCreateSkillName] != 1 || seen[skills.BuiltinCreatePlanName] != 1 {
		t.Fatalf("reserved guidance counts=%v inventory=%#v", seen, listed.Skills)
	}
	if !agentSkillListed {
		t.Fatalf("newly authored skill missing without restart: %#v", listed.Skills)
	}
	agentSkillResult, err := runtime.Call(context.Background(), "load_skill", map[string]any{
		"workspace_id": workspaceID,
		"name":         "agent-skill",
		"max_bytes":    500_000,
	})
	if err != nil || agentSkillResult.IsError {
		t.Fatalf("load_skill(agent-skill) err=%v result=%#v", err, agentSkillResult)
	}
	var agentSkill skills.Loaded
	if err := json.Unmarshal([]byte(agentSkillResult.Content[0].Text), &agentSkill); err != nil {
		t.Fatal(err)
	}
	if agentSkill.Skill.Name != "agent-skill" || !strings.Contains(agentSkill.Content, "agent skill body") {
		t.Fatalf("newly authored skill load=%#v", agentSkill)
	}

	for _, test := range []struct {
		name string
		tool string
	}{
		{name: skills.BuiltinCreateRuleName, tool: tools.CreateRuleToolName},
		{name: skills.BuiltinCreateSkillName, tool: tools.CreateSkillToolName},
		{name: skills.BuiltinCreatePlanName, tool: tools.CreatePlanToolName},
	} {
		loadedResult, err := runtime.Call(context.Background(), "load_skill", map[string]any{
			"workspace_id": workspaceID,
			"name":         test.name,
			"max_bytes":    500_000,
		})
		if err != nil || loadedResult.IsError {
			t.Fatalf("load_skill(%s) err=%v result=%#v", test.name, err, loadedResult)
		}
		var loaded skills.Loaded
		if err := json.Unmarshal([]byte(loadedResult.Content[0].Text), &loaded); err != nil {
			t.Fatal(err)
		}
		if !skills.IsBuiltin(loaded.Skill) || !strings.Contains(loaded.Content, test.tool) {
			t.Fatalf("loaded reserved guidance=%#v", loaded)
		}
		if !strings.Contains(loaded.Content, "Do not use generic") {
			t.Fatalf("reserved guidance lacks no-fallback contract: %q", loaded.Content)
		}
	}
}

func TestInstructionAuthoringToolsRemainWorkspaceBoundAndFailClosed(t *testing.T) {
	runtime, workspaceID, root := newInstructionAuthoringRuntime(t)
	otherRoot := t.TempDir()
	other, err := runtime.Workspaces.Register(otherRoot)
	if err != nil {
		t.Fatal(err)
	}

	ctx := tools.WithBoundWorkspace(context.Background(), workspaceID)
	result, err := runtime.Call(ctx, tools.CreateRuleToolName, map[string]any{
		"workspace_id": other.ID,
		"mode":         "create",
		"name":         "cross-workspace",
		"always_apply": true,
		"content":      "must not write",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("cross-workspace authoring succeeded: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(otherRoot).RulesRoot(), "cross-workspace.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-workspace authoring mutated target: %v", err)
	}

	result, err = runtime.Call(context.Background(), tools.CreateRuleToolName, map[string]any{
		"workspace_id": workspaceID,
		"scope":        "global",
		"mode":         "create",
		"name":         "forced-workspace",
		"always_apply": true,
		"content":      "workspace only",
	})
	if err != nil || result.IsError {
		t.Fatalf("forced workspace call err=%v result=%#v", err, result)
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(root).RulesRoot(), "forced-workspace.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(configformat.RootPath(), "rules", "forced-workspace.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agent authoring reached global rule root: %v", err)
	}

	for _, reserved := range []string{skills.BuiltinCreateRuleName, skills.BuiltinCreateSkillName, skills.BuiltinCreatePlanName} {
		result, err = runtime.Call(context.Background(), tools.CreateSkillToolName, map[string]any{
			"workspace_id": workspaceID,
			"mode":         "create",
			"name":         reserved,
			"description":  "must be rejected",
			"instructions": "must not shadow builtin guidance",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "reserved by CodeMCP") {
			t.Fatalf("reserved skill %q was not rejected: %#v", reserved, result)
		}
		if _, err := os.Stat(filepath.Join(workspacestate.New(root).SkillsRoot(), reserved)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reserved skill %q created native shadow state: %v", reserved, err)
		}
	}

	failClosed := tools.NewRuntime()
	blockedRoot := t.TempDir()
	blocked, err := failClosed.Workspaces.Register(blockedRoot)
	if err != nil {
		t.Fatal(err)
	}
	result, err = failClosed.Call(context.Background(), tools.CreateRuleToolName, map[string]any{
		"workspace_id": blocked.ID,
		"mode":         "create",
		"name":         "missing-provider",
		"always_apply": true,
		"content":      "must not write",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "authoring is unavailable") {
		t.Fatalf("missing provider did not fail closed: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(blockedRoot).RulesRoot(), "missing-provider.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing provider mutated filesystem: %v", err)
	}
}

func TestInstructionAuthoringToolErrorsAreBoundedAndBodiesAreRedacted(t *testing.T) {
	runtime, workspaceID, _ := newInstructionAuthoringRuntime(t)
	var observations []tools.CallObservation
	runtime.SetCallObserver(func(value tools.CallObservation) {
		observations = append(observations, value)
	})

	const ruleSecret = "RULE_BODY_SECRET_81f9"
	ruleArgs := map[string]any{
		"workspace_id": workspaceID,
		"mode":         "create",
		"name":         "observed-rule",
		"always_apply": true,
		"content":      ruleSecret,
	}
	ruleEnvelope := map[string]any{"name": tools.CreateRuleToolName, "arguments": ruleArgs}
	ctx := tools.WithCallDetails(context.Background(), "tools/call", ruleEnvelope)
	ctx = tools.WithCallRequest(ctx, map[string]any{"method": "tools/call", "params": ruleEnvelope})
	result, err := runtime.Call(ctx, tools.CreateRuleToolName, ruleArgs)
	if err != nil || result.IsError {
		t.Fatalf("observed rule err=%v result=%#v", err, result)
	}

	const instructionSecret = "SKILL_INSTRUCTION_SECRET_42ac"
	const supportSecret = "SKILL_SUPPORT_SECRET_990a"
	skillArgs := map[string]any{
		"workspace_id": workspaceID,
		"mode":         "create",
		"name":         "observed-skill",
		"description":  "Observed skill",
		"instructions": instructionSecret,
		"supporting_files": []any{
			map[string]any{"path": "secret.txt", "content": supportSecret},
		},
	}
	skillEnvelope := map[string]any{"name": tools.CreateSkillToolName, "arguments": skillArgs}
	ctx = tools.WithCallDetails(context.Background(), "tools/call", skillEnvelope)
	ctx = tools.WithCallRequest(ctx, map[string]any{"method": "tools/call", "params": skillEnvelope})
	result, err = runtime.Call(ctx, tools.CreateSkillToolName, skillArgs)
	if err != nil || result.IsError {
		t.Fatalf("observed skill err=%v result=%#v", err, result)
	}

	encoded, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{ruleSecret, instructionSecret, supportSecret} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("authored body leaked into call observations: %s", encoded)
		}
	}

	runtime.SetInstructionAuthoringProvider(rejectingInstructionAuthoringProvider{})
	result, err = runtime.Call(context.Background(), tools.CreateRuleToolName, map[string]any{
		"workspace_id": workspaceID,
		"mode":         "create",
		"name":         "bounded-error",
		"always_apply": true,
		"content":      "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) == 0 {
		t.Fatalf("expected bounded authoring error: %#v", result)
	}
	if len([]rune(result.Content[0].Text)) > 1100 {
		t.Fatalf("authoring error is not bounded: %d runes", len([]rune(result.Content[0].Text)))
	}
}
