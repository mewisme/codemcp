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

type rejectingPlanAuthoringProvider struct{}

func (rejectingPlanAuthoringProvider) AuthorPlan(context.Context, map[string]any) (any, error) {
	return nil, errors.New(strings.Repeat("p", 5000))
}

func newPlanAuthoringRuntime(t *testing.T) (*tools.Runtime, string, string) {
	t.Helper()
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	root := t.TempDir()
	item, err := runtime.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	runtime.SetPlanAuthoringProvider(application.NewAgentPlanAuthoringProvider(runtime.Workspaces, runtime.InstructionChanges))
	return runtime, item.ID, root
}

func TestCreatePlanToolUsesCanonicalOwnerAndReservedGuidance(t *testing.T) {
	runtime, workspaceID, root := newPlanAuthoringRuntime(t)
	schema, ok := runtime.Registry.Schema(tools.CreatePlanToolName)
	if !ok {
		t.Fatal("create_plan schema is missing")
	}
	var input map[string]any
	if err := json.Unmarshal(schema.InputSchema, &input); err != nil {
		t.Fatal(err)
	}
	if input["additionalProperties"] != false {
		t.Fatalf("create_plan input is not strict: %#v", input)
	}
	properties, _ := input["properties"].(map[string]any)
	for _, required := range []string{"workspace_id", "mode", "name", "plan_content", "implementation_order"} {
		if _, ok := properties[required]; !ok {
			t.Fatalf("create_plan missing input property %q", required)
		}
	}

	planBody, orderBody := agentPlanFixture(false)
	result, err := runtime.Call(context.Background(), tools.CreatePlanToolName, map[string]any{
		"workspace_id": workspaceID, "mode": "create", "name": "agent-plan",
		"plan_content": planBody, "implementation_order": orderBody,
	})
	if err != nil || result.IsError {
		t.Fatalf("create_plan err=%v result=%#v", err, result)
	}
	created, ok := result.StructuredContent.(application.PlanAuthoringResult)
	if !ok {
		t.Fatalf("create_plan result type=%T", result.StructuredContent)
	}
	if created.Name != "agent-plan" || created.ContentID == "" || created.PhaseCount != 1 || created.DryRun {
		t.Fatalf("create_plan result=%#v", created)
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(root).PlansRoot(), "agent-plan.md")); err != nil {
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
	count := 0
	for _, skill := range listed.Skills {
		if skill.Name == skills.BuiltinCreatePlanName {
			count++
			if !skills.IsBuiltin(skill) {
				t.Fatalf("create-plan guidance is not builtin: %#v", skill)
			}
		}
	}
	if count != 1 {
		t.Fatalf("create-plan guidance count=%d inventory=%#v", count, listed.Skills)
	}
	loadedResult, err := runtime.Call(context.Background(), "load_skill", map[string]any{
		"workspace_id": workspaceID, "name": skills.BuiltinCreatePlanName, "max_bytes": 500_000,
	})
	if err != nil || loadedResult.IsError {
		t.Fatalf("load create-plan guidance err=%v result=%#v", err, loadedResult)
	}
	var loaded skills.Loaded
	if err := json.Unmarshal([]byte(loadedResult.Content[0].Text), &loaded); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"create_plan", "Plan Mode", "expected_content_id", ".cm/plans", "Do not use generic"} {
		if !strings.Contains(loaded.Content, marker) {
			t.Fatalf("create-plan guidance missing %q: %q", marker, loaded.Content)
		}
	}

	seenCreatePlan := 0
	for _, registered := range runtime.List() {
		if registered.Name == tools.CreatePlanToolName {
			seenCreatePlan++
		}
		for _, forbidden := range []string{
			"plan_list", "get_plan", "plan_status", "delete_plan", "enter_plan",
			"exit_plan", "implement_plan", "complete_plan",
		} {
			if registered.Name == forbidden {
				t.Fatalf("unexpected plan tool %q", registered.Name)
			}
		}
	}
	if seenCreatePlan != 1 {
		t.Fatalf("create_plan registrations=%d", seenCreatePlan)
	}
}

func TestCreatePlanToolIsWorkspaceBoundAndFailsClosed(t *testing.T) {
	runtime, workspaceID, root := newPlanAuthoringRuntime(t)
	otherRoot := t.TempDir()
	other, err := runtime.Workspaces.Register(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	planBody, orderBody := agentPlanFixture(false)

	ctx := tools.WithBoundWorkspace(context.Background(), workspaceID)
	result, err := runtime.Call(ctx, tools.CreatePlanToolName, map[string]any{
		"mode": "create", "name": "bound-plan", "plan_content": planBody, "implementation_order": orderBody,
	})
	if err != nil || result.IsError {
		t.Fatalf("bound create_plan err=%v result=%#v", err, result)
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(root).PlansRoot(), "bound-plan.md")); err != nil {
		t.Fatal(err)
	}

	result, err = runtime.Call(ctx, tools.CreatePlanToolName, map[string]any{
		"workspace_id": other.ID, "mode": "create", "name": "cross-plan",
		"plan_content": planBody, "implementation_order": orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("cross-workspace create_plan succeeded: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(workspacestate.New(otherRoot).PlansRoot(), "cross-plan.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-workspace plan state changed: %v", err)
	}

	failClosed := tools.NewRuntime()
	blockedRoot := t.TempDir()
	blocked, err := failClosed.Workspaces.Register(blockedRoot)
	if err != nil {
		t.Fatal(err)
	}
	result, err = failClosed.Call(context.Background(), tools.CreatePlanToolName, map[string]any{
		"workspace_id": blocked.ID, "mode": "create", "name": "missing-provider",
		"plan_content": planBody, "implementation_order": orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "authoring is unavailable") {
		t.Fatalf("missing plan provider did not fail closed: %#v", result)
	}
}

func TestCreatePlanToolMapsStaleErrorsAndRedactsBodies(t *testing.T) {
	runtime, workspaceID, _ := newPlanAuthoringRuntime(t)
	var observations []tools.CallObservation
	runtime.SetCallObserver(func(value tools.CallObservation) {
		observations = append(observations, value)
	})
	const planSecret = "PLAN_BODY_SECRET_24c1"
	const orderSecret = "PLAN_ORDER_SECRET_17af"
	planBody, orderBody := agentPlanFixture(false)
	planBody = strings.Replace(planBody, "Persist safely.", "Persist safely. "+planSecret, 1)
	orderBody = strings.Replace(orderBody, "Persistence comes first.", "Persistence comes first. "+orderSecret, 1)
	args := map[string]any{
		"workspace_id": workspaceID, "mode": "create", "name": "observed-plan",
		"plan_content": planBody, "implementation_order": orderBody,
	}
	envelope := map[string]any{"name": tools.CreatePlanToolName, "arguments": args}
	ctx := tools.WithCallDetails(context.Background(), "tools/call", envelope)
	ctx = tools.WithCallRequest(ctx, map[string]any{"method": "tools/call", "params": envelope})
	result, err := runtime.Call(ctx, tools.CreatePlanToolName, args)
	if err != nil || result.IsError {
		t.Fatalf("observed create_plan err=%v result=%#v", err, result)
	}
	created := result.StructuredContent.(application.PlanAuthoringResult)

	completedPlan, completedOrder := agentPlanFixture(true)
	stale, err := runtime.Call(context.Background(), tools.CreatePlanToolName, map[string]any{
		"workspace_id": workspaceID, "mode": "update", "name": "observed-plan",
		"plan_content": completedPlan, "implementation_order": completedOrder,
		"expected_content_id": "sha256:" + strings.Repeat("0", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stale.IsError || len(stale.Content) == 0 || !strings.Contains(stale.Content[0].Text, "stale") {
		t.Fatalf("stale update result=%#v current=%s", stale, created.ContentID)
	}
	encoded, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{planSecret, orderSecret} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("plan body leaked into observations: %s", encoded)
		}
	}

	runtime.SetPlanAuthoringProvider(rejectingPlanAuthoringProvider{})
	bounded, err := runtime.Call(context.Background(), tools.CreatePlanToolName, map[string]any{
		"workspace_id": workspaceID, "mode": "create", "name": "bounded-error",
		"plan_content": planBody, "implementation_order": orderBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.IsError || len(bounded.Content) == 0 || len([]rune(bounded.Content[0].Text)) > 1100 {
		t.Fatalf("plan authoring error is not bounded: %#v", bounded)
	}
}

func agentPlanFixture(completed bool) (string, string) {
	mark := " "
	if completed {
		mark = "x"
	}
	return "# Persisted plan\n\n## Goal\nPersist safely.\n\n## Phase 1A - Persist state\n\n- [" + mark + "] Persist the state.\n\n## Acceptance\nState is durable.",
		"## Execution rules\nComplete the phase.\n\n## Why this order\nPersistence comes first.\n\n## Ordered phases\n\n- [" + mark + "] Phase 1A - Persist state\n\n## Terminal acceptance\n\n- [" + mark + "] Durable-state validation passes."
}
