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
	for _, expected := range []string{"workspace_register", "workspace_container_context", "project_context", "load_path_rules", "load_skill", "apply_patch", "run_command", "verify", "agent_complete"} {
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
