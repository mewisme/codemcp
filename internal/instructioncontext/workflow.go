package instructioncontext

import "strings"

const (
	agentWorkflowIntroduction = "Use CodeMCP as a multi-workspace coding agent with explicit workspace targeting."
	serverIntroduction        = "Use CodeMCP for local, workspace-aware coding and project operations."
	serverWorkspaceBootstrap  = "For project work, obtain a workspace_id with workspace_register unless one is already provided; use workspace_status to inspect its registered root, persisted shell cwd, and allowed directories. If the user provides a wsc_* workspace container, call workspace_container_context first and choose concrete member ws_* workspace IDs for actual work."
	serverContextBootstrap    = "Call agent_status when runtime or permission details are needed. If the client negotiated the native Skills extension, use skills/list and skills/get for Skill discovery/content and do not fetch the same Skill again through Tool fallback; otherwise use list_skills/load_skill."

	guidanceWorkspace  = "One MCP session may work across multiple registered workspaces. Every workspace-scoped call must explicitly target workspace_id; keep each workspace's project context, rules, memory, persisted shell cwd, REPL state, checkpoints, and assumptions isolated and never carry workspace-specific state into another workspace."
	guidanceContainer  = "Treat ws_* as an execution/filesystem workspace and wsc_* as a workspace-container orchestration scope. When the user targets wsc_*, call workspace_container_context first, choose one or more member ws_* IDs for concrete work, and call project_context with memory enabled before substantial work in each selected member. Never pass wsc_* as workspace_id to filesystem, Git, shell, checkpoint, memory, rule, or project tools, and never merge member cwd, permissions, rules, memory, checkpoints, or assumptions."
	guidanceContext    = "At the start of every MCP session, fetch workspace memory by calling project_context with memory enabled before substantial work; repeat project_context before first work in each additional workspace targeted by that session. Treat project_context as the workspace instruction bundle and follow project/user instructions and unconditional rules from it before acting."
	guidanceRead       = "Inspect relevant files before changing them. Use read_files/read_text_file for source context and load_path_rules for path-scoped rules before modifying matching files."
	guidanceSkills     = "Review the skill summaries in project_context. When a skill is applicable, call load_skill with its exact name before using that workflow."
	guidancePlanMode   = "Treat /plan as Plan Mode only when it appears in the user's request as an exact standalone whitespace-delimited token. In Plan Mode, establish the target workspace, call project_context with memory enabled, inspect applicable rules and skills, and audit the relevant source plus existing persisted plan state before authoring or updating a plan. A successful Plan Mode request must persist through create_plan; do not return only an unpersisted prose plan. Once Plan Mode begins, do not perform implementation mutations except normal workspace bootstrap needed to establish the target and the final create_plan mutation. /plan dominates contradictory same-request implementation wording: create or update the plan, then stop before implementation. If plan authoring cannot be persisted, report the error and do not fall through to implementation. Normal agent_complete terminal semantics still apply after the plan mutation when available. Generic MCP servers cannot hard-block unrelated Tool calls solely because /plan was present because their Tool requests do not carry the original raw user prompt; raw-prompt interpretation belongs to the host agent. First-party harnesses that own raw prompts may map the same directive to a typed local Plan Mode without changing these semantics."
	guidanceEdit       = "Prefer deterministic edits with apply_patch, edit_file, or multi_edit. Use run_command for commands, builds, tests, formatting, and other shell operations within the persisted workspace cwd."
	guidanceBackground = "Use start_process for long work; completion is lifecycle-driven. Do not poll process_status/process_output or Tasks to wait. Status/output are inspection/recovery; stop_process is explicit cancellation/intervention. No model continuation is proven, so return control after starting background work. Intentional in-process sleep is valid."
	guidanceVerify     = "For non-trivial work, make a short plan, implement incrementally, and verify with the repository's relevant tests, lint, typecheck, build, or other documented checks."
	guidanceRewind     = "Use rewind to inspect or recover automatic file checkpoints when an edit must be reviewed or reverted."
	guidanceRemember   = "When the user explicitly asks to remember, save, persist, or retain an eligible workspace-specific note for future sessions, call remember immediately in that same turn before replying; do not merely acknowledge or defer the request. Identify a concise scope and an optional child key: omit key for a scope-level note, and never repeat the scope as its child key. Call memory_get for that target, reconcile the current canonical note with the new information, then call remember with the complete canonical replacement note. A newer explicit user preference supersedes conflicting older memory; rewrite the entry instead of concatenating contradictory statements. Use remember only for durable workspace-specific conclusions that will help future sessions; do not store conversation history, secrets, transient status, or raw MCP session identifiers. If project_context reports memory optimization recommended and the current task permits maintenance, call optimize_memory and reconcile candidates with remember/forget instead of letting memory grow unbounded."
	guidanceComplete   = "When agent_complete is available in the current tool profile and requested CodeMCP workspace work reaches a terminal state, finish verification first, then call agent_complete for each materially worked workspace as the final CodeMCP tool call immediately before the final user response. Use completed only when the requested work is actually finished; use partial when useful work remains incomplete, blocked when a concrete blocker prevents completion, or cancelled when the work was intentionally stopped. Do not call agent_complete for intermediate steps when more work in the current request is about to continue. MCP disconnect, transport close, or model-process exit alone is never successful completion; agent_complete records terminal work state but does not close the MCP transport or model process."
	guidanceMissing    = "Do not assume instructions, rules, skill bodies, Git state, or environment details that are absent from the supplied context. Query the appropriate tool instead of guessing."
	guidanceScope      = "Preserve unrelated user changes and keep mutations scoped to the requested task."

	DefaultAgentWorkflow = agentWorkflowIntroduction + "\n\n" +
		"1. " + guidanceWorkspace + "\n" +
		"2. " + guidanceContainer + "\n" +
		"3. " + guidanceContext + "\n" +
		"4. " + guidanceRead + "\n" +
		"5. " + guidanceSkills + "\n" +
		"6. " + guidanceEdit + "\n" +
		"7. " + guidanceBackground + "\n" +
		"8. " + guidanceVerify + "\n" +
		"9. " + guidanceRewind + "\n" +
		"10. " + guidanceRemember + "\n" +
		"11. " + guidanceComplete + "\n" +
		"12. " + guidanceMissing + "\n" +
		"13. " + guidanceScope
)

const PlanModeDirectiveToken = "/plan"

type ServerInstructionDirective struct {
	ID   string
	Text string
}

type ServerInstructionModel struct {
	Role      string
	Bootstrap []ServerInstructionDirective
	Context   []ServerInstructionDirective
	Workflow  []ServerInstructionDirective
	Lifecycle []ServerInstructionDirective
}

type ServerInstructionRenderOptions struct {
	Heading string
}

type BackgroundWorkCapabilities struct {
	TaskObservation    bool
	ServerNotification bool
	ModelContinuation  bool
	InFlightSteering   bool
}

func CanonicalServerInstructionModel() ServerInstructionModel {
	return ServerInstructionModel{
		Role: serverIntroduction,
		Bootstrap: []ServerInstructionDirective{
			{ID: "workspace-bootstrap", Text: serverWorkspaceBootstrap},
			{ID: "context-bootstrap", Text: serverContextBootstrap},
		},
		Context: []ServerInstructionDirective{
			{ID: "workspace-isolation", Text: guidanceWorkspace},
			{ID: "container-orchestration", Text: guidanceContainer},
			{ID: "project-context", Text: guidanceContext},
		},
		Workflow: []ServerInstructionDirective{
			{ID: "inspect", Text: guidanceRead},
			{ID: "skills", Text: guidanceSkills},
			{ID: "plan-mode", Text: guidancePlanMode},
			{ID: "mutate", Text: guidanceEdit},
			{ID: "background-work", Text: guidanceBackground},
			{ID: "verify", Text: guidanceVerify},
			{ID: "rewind", Text: guidanceRewind},
		},
		Lifecycle: []ServerInstructionDirective{
			{ID: "remember", Text: guidanceRemember},
			{ID: "complete", Text: guidanceComplete},
			{ID: "missing-context", Text: guidanceMissing},
			{ID: "scope", Text: guidanceScope},
		},
	}
}

func RenderServerInstructions(model ServerInstructionModel, options ServerInstructionRenderOptions) string {
	parts := make([]string, 0, 1+len(model.Bootstrap)+len(model.Context)+len(model.Workflow)+len(model.Lifecycle))
	if heading := strings.TrimSpace(options.Heading); heading != "" {
		parts = append(parts, heading)
	}
	if role := strings.TrimSpace(model.Role); role != "" {
		parts = append(parts, role)
	}
	for _, group := range [][]ServerInstructionDirective{model.Bootstrap, model.Context, model.Workflow, model.Lifecycle} {
		for _, directive := range group {
			if text := strings.TrimSpace(directive.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, " ")
}

var sharedGuidanceSteps = []string{
	guidanceWorkspace,
	guidanceContainer,
	guidanceContext,
	guidanceRead,
	guidanceSkills,
	guidanceEdit,
	guidanceBackground,
	guidanceVerify,
	guidanceRewind,
	guidanceRemember,
	guidanceComplete,
	guidanceMissing,
	guidanceScope,
}

func AgentWorkflow() string {
	return DefaultAgentWorkflow
}

func RequestsPlanMode(prompt string) bool {
	for _, token := range strings.Fields(prompt) {
		if token == PlanModeDirectiveToken {
			return true
		}
	}
	return false
}

func AgentWorkflowForBackground(capabilities BackgroundWorkCapabilities) string {
	return strings.Replace(DefaultAgentWorkflow, guidanceBackground, BackgroundWorkGuidanceFor(capabilities), 1)
}

func SharedGuidanceSteps() []string {
	return append([]string(nil), sharedGuidanceSteps...)
}

func BackgroundWorkGuidance() string {
	return guidanceBackground
}

func BackgroundWorkGuidanceFor(capabilities BackgroundWorkCapabilities) string {
	continuation := "No model continuation is proven, so return control after starting background work."
	switch {
	case capabilities.ModelContinuation && capabilities.InFlightSteering:
		continuation = "The client proves model continuation and in-flight steering; rely on that lifecycle path instead of polling."
	case capabilities.ModelContinuation:
		continuation = "The client proves model continuation; rely on that lifecycle path instead of polling."
	case capabilities.ServerNotification:
		continuation = "Server notifications do not prove model continuation; return control after starting background work."
	case capabilities.TaskObservation:
		continuation = "Tasks provide observation only and do not prove model continuation; return control after starting background work."
	}
	return "Use start_process for long work; completion is lifecycle-driven. Do not poll process_status/process_output or Tasks to wait. Status/output are inspection/recovery; stop_process is explicit cancellation/intervention. " + continuation + " Intentional in-process sleep is valid."
}

func StaticServerInstructions() string {
	return RenderServerInstructions(CanonicalServerInstructionModel(), ServerInstructionRenderOptions{})
}
