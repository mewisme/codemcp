package instructioncontext

import (
	"strings"
	"testing"
)

func TestAgentWorkflowCoversNativeToolFlow(t *testing.T) {
	workflow := AgentWorkflow()
	for _, expected := range []string{
		"MCP session", "project_context", "workspace_container_context", "load_path_rules", "load_skill", "read_files", "read_text_file", "apply_patch", "edit_file", "multi_edit", "run_command", "rewind", "remember", "verify", "agent_complete",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing %q: %s", expected, workflow)
		}
	}
}

func TestAgentWorkflowRequiresExplicitTerminalCompletionWhenCapabilityExists(t *testing.T) {
	workflow := AgentWorkflow()
	server := StaticServerInstructions()
	for _, expected := range []string{
		"agent_complete is available in the current tool profile",
		"finish verification first",
		"final CodeMCP tool call",
		"completed only when",
		"partial",
		"blocked",
		"cancelled",
		"intermediate steps",
		"MCP disconnect",
		"transport close",
		"model-process exit",
		"never successful completion",
		"does not close the MCP transport",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing completion guidance %q: %s", expected, workflow)
		}
		if !strings.Contains(server, expected) {
			t.Fatalf("server instructions missing completion guidance %q: %s", expected, server)
		}
	}
}

func TestAgentWorkflowRequiresLifecycleDrivenBackgroundWaiting(t *testing.T) {
	workflow := AgentWorkflow()
	server := StaticServerInstructions()
	for _, expected := range []string{
		"start_process",
		"lifecycle-driven",
		"Do not poll process_status/process_output",
		"inspection/recovery",
		"stop_process is explicit cancellation/intervention",
		"No model continuation is proven",
		"return control after starting background work",
		"Intentional in-process sleep is valid",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing background guidance %q: %s", expected, workflow)
		}
		if !strings.Contains(server, expected) {
			t.Fatalf("server instructions missing background guidance %q: %s", expected, server)
		}
	}
}

func TestBackgroundWorkGuidanceCapabilityCombinations(t *testing.T) {
	tests := []struct {
		name         string
		capabilities BackgroundWorkCapabilities
		continuation string
	}{
		{name: "unproven", continuation: "No model continuation is proven, so return control after starting background work."},
		{name: "tasks-only", capabilities: BackgroundWorkCapabilities{TaskObservation: true}, continuation: "Tasks provide observation only and do not prove model continuation; return control after starting background work."},
		{name: "notification-only", capabilities: BackgroundWorkCapabilities{ServerNotification: true}, continuation: "Server notifications do not prove model continuation; return control after starting background work."},
		{name: "continuation", capabilities: BackgroundWorkCapabilities{ModelContinuation: true}, continuation: "The client proves model continuation; rely on that lifecycle path instead of polling."},
		{name: "continuation-and-steering", capabilities: BackgroundWorkCapabilities{ModelContinuation: true, InFlightSteering: true}, continuation: "The client proves model continuation and in-flight steering; rely on that lifecycle path instead of polling."},
	}
	const prefix = "Use start_process for long work; completion is lifecycle-driven. Do not poll process_status/process_output or Tasks to wait. Status/output are inspection/recovery; stop_process is explicit cancellation/intervention. "
	const suffix = " Intentional in-process sleep is valid."
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := prefix + tc.continuation + suffix
			if got := BackgroundWorkGuidanceFor(tc.capabilities); got != want {
				t.Fatalf("guidance=%q want=%q", got, want)
			}
			workflow := AgentWorkflowForBackground(tc.capabilities)
			if !strings.Contains(workflow, want) {
				t.Fatalf("workflow missing capability-specific guidance: %s", workflow)
			}
		})
	}
}

func TestAgentWorkflowDocumentsWorkspaceContainerOrchestration(t *testing.T) {
	workflow := AgentWorkflow()
	server := StaticServerInstructions()
	for _, expected := range []string{"ws_*", "wsc_*", "workspace-container orchestration scope", "workspace_container_context first", "member ws_*", "Never pass wsc_* as workspace_id", "never merge member cwd"} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing container guidance %q: %s", expected, workflow)
		}
		if !strings.Contains(server, expected) {
			t.Fatalf("server instructions missing container guidance %q: %s", expected, server)
		}
	}
}

func TestAgentWorkflowRequiresImmediateRememberOnExplicitUserRequest(t *testing.T) {
	workflow := AgentWorkflow()
	for _, expected := range []string{"explicitly asks to remember", "call remember immediately in that same turn", "do not merely acknowledge or defer", "optional child key", "never repeat the scope as its child key", "memory_get", "complete canonical replacement note", "supersedes conflicting older memory", "instead of concatenating contradictory statements"} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing immediate remember guidance %q: %s", expected, workflow)
		}
	}
}

func TestAgentWorkflowStoresMemoryConclusionsNotConversationHistory(t *testing.T) {
	workflow := AgentWorkflow()
	for _, expected := range []string{"durable workspace-specific conclusions", "do not store conversation history"} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing canonical memory guidance %q: %s", expected, workflow)
		}
	}
}

func TestServerInstructionsRequireMemoryFetchEverySession(t *testing.T) {
	workflow := AgentWorkflow()
	server := StaticServerInstructions()
	for _, expected := range []string{"start of every MCP session", "fetch workspace memory", "project_context with memory enabled", "first work in each additional workspace"} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing session memory bootstrap %q: %s", expected, workflow)
		}
		if !strings.Contains(server, expected) {
			t.Fatalf("server instructions missing session memory bootstrap %q: %s", expected, server)
		}
	}
}

func TestAgentWorkflowDocumentsMultiWorkspaceIsolationInvariant(t *testing.T) {
	workflow := AgentWorkflow()
	for _, expected := range []string{"multiple registered workspaces", "explicitly target workspace_id", "persisted shell cwd", "never carry workspace-specific state into another workspace"} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("workflow missing multi-workspace isolation invariant %q", expected)
		}
	}
}

func TestAgentWorkflowDoesNotRequireSkillBodiesUpFront(t *testing.T) {
	workflow := AgentWorkflow()
	if strings.Contains(workflow, "load every skill") || strings.Contains(workflow, "all skill bodies") {
		t.Fatalf("workflow eagerly loads skills: %s", workflow)
	}
	if !strings.Contains(workflow, "When a skill is applicable") {
		t.Fatalf("workflow must load only applicable skills: %s", workflow)
	}
}

func TestAgentWorkflowIsStableAndNonEmpty(t *testing.T) {
	if strings.TrimSpace(DefaultAgentWorkflow) == "" || AgentWorkflow() != DefaultAgentWorkflow {
		t.Fatalf("workflow = %q", AgentWorkflow())
	}
}

func TestPlanModeDirectiveUsesStandaloneWhitespaceDelimitedToken(t *testing.T) {
	for _, prompt := range []string{
		"/plan",
		" /plan ",
		"please /plan this change",
		"/plan\nthen describe the work",
		"implement this\t/plan\tbut do not code yet",
		"prefix\u2003/plan\u2003suffix",
	} {
		if !RequestsPlanMode(prompt) {
			t.Fatalf("expected Plan Mode for %q", prompt)
		}
	}
	for _, prompt := range []string{
		"",
		"plan",
		"/PLAN",
		"/planner",
		"/plan/foo",
		"./plan",
		"https://example.com/plan",
		"file:///plan",
		"say '/plan'",
		"(/plan)",
		"/plan,",
		"docs mention /planner mode",
	} {
		if RequestsPlanMode(prompt) {
			t.Fatalf("unexpected Plan Mode for %q", prompt)
		}
	}
}

func TestPlanModeGuidanceIsCanonicalAndStopsBeforeImplementation(t *testing.T) {
	server := StaticServerInstructions()
	for _, expected := range []string{
		"exact standalone whitespace-delimited token",
		"project_context with memory enabled",
		"inspect applicable rules and skills",
		"audit the relevant source plus existing persisted plan state",
		"persist through create_plan",
		"do not return only an unpersisted prose plan",
		"do not perform implementation mutations",
		"final create_plan mutation",
		"dominates contradictory same-request implementation wording",
		"stop before implementation",
		"do not fall through to implementation",
		"agent_complete terminal semantics",
		"Generic MCP servers cannot hard-block unrelated Tool calls",
		"original raw user prompt",
		"raw-prompt interpretation belongs to the host agent",
		"typed local Plan Mode",
	} {
		if !strings.Contains(server, expected) {
			t.Fatalf("Plan Mode guidance missing %q: %s", expected, server)
		}
	}

	model := CanonicalServerInstructionModel()
	count := 0
	for _, directive := range model.Workflow {
		if directive.ID == "plan-mode" {
			count++
			if directive.Text != guidancePlanMode {
				t.Fatalf("plan-mode directive text drifted: %q", directive.Text)
			}
		}
	}
	if count != 1 || strings.Count(server, guidancePlanMode) != 1 {
		t.Fatalf("plan-mode directive count=%d rendered=%d", count, strings.Count(server, guidancePlanMode))
	}
}

func TestPlanContinuationGuidanceUsesDeterministicSelectionAndCanonicalProgressUpdates(t *testing.T) {
	server := StaticServerInstructions()
	for _, expected := range []string{
		"exact plan_name wins",
		"exactly one non-completed plan exists",
		"multiple non-completed plans exist",
		"discovery is truncated",
		"do not guess from recency or filesystem order",
		"Completed plans are not inferred",
		"plan_execution=true",
		"trusted MCP session and workspace",
		".cm/plans/<name>.md",
		"read the full canonical .cm/plans/<name>.md document before implementation",
		"embedded Implementation order",
		"there is no implement_plan tool",
		"plan name as cross-session identity",
		"content_id as revision identity",
		"Implement only the bound next phase",
		"matching embedded Ordered phases entry",
		"create_plan mode=update",
		"latest expected_content_id",
		"completed_phase_count",
		"next_phase",
		"status=completed is rejected",
		"partial, blocked, or cancelled",
		"release the ephemeral binding",
		"Never update plan progress through generic file edits",
	} {
		if !strings.Contains(server, expected) {
			t.Fatalf("plan continuation guidance missing %q: %s", expected, server)
		}
	}
	model := CanonicalServerInstructionModel()
	count := 0
	for _, directive := range model.Workflow {
		if directive.ID == "plan-continuation" {
			count++
			if directive.Text != guidancePlanContinuation {
				t.Fatalf("plan-continuation directive text drifted: %q", directive.Text)
			}
		}
	}
	if count != 1 || strings.Count(server, guidancePlanContinuation) != 1 {
		t.Fatalf("plan-continuation directive count=%d rendered=%d", count, strings.Count(server, guidancePlanContinuation))
	}
	if strings.Contains(DefaultAgentWorkflow, guidancePlanContinuation) {
		t.Fatal("plan continuation guidance was duplicated into project-context workflow budget")
	}
}

func TestSharedGuidanceRendersIntoWorkflowAndServerInstructions(t *testing.T) {
	workflow := AgentWorkflow()
	server := StaticServerInstructions()
	steps := SharedGuidanceSteps()
	if len(steps) == 0 {
		t.Fatal("shared guidance is empty")
	}
	for _, step := range steps {
		if !strings.Contains(workflow, step) {
			t.Fatalf("workflow missing shared guidance %q", step)
		}
		if !strings.Contains(server, step) {
			t.Fatalf("server instructions missing shared guidance %q", step)
		}
	}
	for _, expected := range []string{"workspace_register", "workspace_status", "workspace_container_context", "persisted shell cwd", "agent_status", "project_context", "list_skills"} {
		if !strings.Contains(server, expected) {
			t.Fatalf("server instructions missing bootstrap %q: %s", expected, server)
		}
	}
}

func TestSharedGuidanceStepsReturnsCopy(t *testing.T) {
	first := SharedGuidanceSteps()
	first[0] = "mutated"
	second := SharedGuidanceSteps()
	if len(second) == 0 || second[0] == "mutated" {
		t.Fatalf("shared guidance leaked mutable state: %#v", second)
	}
}

func TestCanonicalServerInstructionModelIsDeterministicAndBounded(t *testing.T) {
	model := CanonicalServerInstructionModel()
	first := RenderServerInstructions(model, ServerInstructionRenderOptions{})
	second := RenderServerInstructions(CanonicalServerInstructionModel(), ServerInstructionRenderOptions{})
	if first != second || first != StaticServerInstructions() {
		t.Fatalf("server instruction rendering is not deterministic")
	}
	if len(first) == 0 || len(first) > 8192 {
		t.Fatalf("server instructions length=%d", len(first))
	}
	for _, expected := range []string{"workspace_register", "workspace_container_context", "project_context", "load_path_rules", "load_skill", "/plan", "create_plan", "apply_patch", "run_command", "verify", "agent_complete"} {
		if !strings.Contains(first, expected) {
			t.Fatalf("server instructions missing %q", expected)
		}
	}
}

func TestCanonicalServerInstructionModelContainsNoTransientWorkspaceContent(t *testing.T) {
	value := StaticServerInstructions()
	for _, forbidden := range []string{"ws_secret_runtime", "/tmp/private-workspace", "user@example.com", "Bearer ", "sk-"} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("server instructions contain transient or secret marker %q", forbidden)
		}
	}
}
