package instructioncontext

const (
	agentWorkflowIntroduction = "Use CodeMCP as a multi-workspace coding agent with explicit workspace targeting."
	serverIntroduction        = "Use CodeMCP for local, workspace-aware coding and project operations."
	serverWorkspaceBootstrap  = "For project work, obtain a workspace_id with workspace_register unless one is already provided; use workspace_status to inspect its registered root, persisted shell cwd, and allowed directories. If the user provides a wsc_* workspace container, call workspace_container_context first and choose concrete member ws_* workspace IDs for actual work."
	serverContextBootstrap    = "Call agent_status when runtime or permission details are needed. Use list_skills when skill summaries need to be discovered independently from project_context."

	guidanceWorkspace = "One MCP session may work across multiple registered workspaces. Every workspace-scoped call must explicitly target workspace_id; keep each workspace's project context, rules, memory, persisted shell cwd, REPL state, checkpoints, and assumptions isolated and never carry workspace-specific state into another workspace."
	guidanceContainer = "Treat ws_* as an execution/filesystem workspace and wsc_* as a workspace-container orchestration scope. When the user targets wsc_*, call workspace_container_context first, choose one or more member ws_* IDs for concrete work, and call project_context with memory enabled before substantial work in each selected member. Never pass wsc_* as workspace_id to filesystem, Git, shell, checkpoint, memory, rule, or project tools, and never merge member cwd, permissions, rules, memory, checkpoints, or assumptions."
	guidanceContext   = "At the start of every MCP session, fetch workspace memory by calling project_context with memory enabled before substantial work; repeat project_context before first work in each additional workspace targeted by that session. Treat project_context as the workspace instruction bundle and follow project/user instructions and unconditional rules from it before acting."
	guidanceRead      = "Inspect relevant files before changing them. Use read_files/read_text_file for source context and load_path_rules for path-scoped rules before modifying matching files."
	guidanceSkills    = "Review the skill summaries in project_context. When a skill is applicable, call load_skill with its exact name before using that workflow."
	guidanceEdit      = "Prefer deterministic edits with apply_patch, edit_file, or multi_edit. Use run_command for commands, builds, tests, formatting, and other shell operations within the persisted workspace cwd."
	guidanceVerify    = "For non-trivial work, make a short plan, implement incrementally, and verify with the repository's relevant tests, lint, typecheck, build, or other documented checks."
	guidanceRewind    = "Use rewind to inspect or recover automatic file checkpoints when an edit must be reviewed or reverted."
	guidanceRemember  = "When the user explicitly asks to remember, save, persist, or retain an eligible workspace-specific note for future sessions, call remember immediately in that same turn before replying; do not merely acknowledge or defer the request. Identify a concise scope and an optional child key: omit key for a scope-level note, and never repeat the scope as its child key. Call memory_get for that target, reconcile the current canonical note with the new information, then call remember with the complete canonical replacement note. A newer explicit user preference supersedes conflicting older memory; rewrite the entry instead of concatenating contradictory statements. Use remember only for durable workspace-specific conclusions that will help future sessions; do not store conversation history, secrets, transient status, or raw MCP session identifiers. If project_context reports memory optimization recommended and the current task permits maintenance, call optimize_memory and reconcile candidates with remember/forget instead of letting memory grow unbounded."
	guidanceComplete  = "When agent_complete is available in the current tool profile and requested CodeMCP workspace work reaches a terminal state, finish verification first, then call agent_complete for each materially worked workspace as the final CodeMCP tool call immediately before the final user response. Use completed only when the requested work is actually finished; use partial when useful work remains incomplete, blocked when a concrete blocker prevents completion, or cancelled when the work was intentionally stopped. Do not call agent_complete for intermediate steps when more work in the current request is about to continue. MCP disconnect, transport close, or model-process exit alone is never successful completion; agent_complete records terminal work state but does not close the MCP transport or model process."
	guidanceMissing   = "Do not assume instructions, rules, skill bodies, Git state, or environment details that are absent from the supplied context. Query the appropriate tool instead of guessing."
	guidanceScope     = "Preserve unrelated user changes and keep mutations scoped to the requested task."

	DefaultAgentWorkflow = agentWorkflowIntroduction + "\n\n" +
		"1. " + guidanceWorkspace + "\n" +
		"2. " + guidanceContainer + "\n" +
		"3. " + guidanceContext + "\n" +
		"4. " + guidanceRead + "\n" +
		"5. " + guidanceSkills + "\n" +
		"6. " + guidanceEdit + "\n" +
		"7. " + guidanceVerify + "\n" +
		"8. " + guidanceRewind + "\n" +
		"9. " + guidanceRemember + "\n" +
		"10. " + guidanceComplete + "\n" +
		"11. " + guidanceMissing + "\n" +
		"12. " + guidanceScope

	defaultServerInstructions = serverIntroduction + " " + serverWorkspaceBootstrap + " " + serverContextBootstrap + " " +
		guidanceWorkspace + " " + guidanceContainer + " " + guidanceContext + " " + guidanceRead + " " + guidanceSkills + " " + guidanceEdit + " " +
		guidanceVerify + " " + guidanceRewind + " " + guidanceRemember + " " + guidanceComplete + " " + guidanceMissing + " " + guidanceScope
)

var sharedGuidanceSteps = []string{
	guidanceWorkspace,
	guidanceContainer,
	guidanceContext,
	guidanceRead,
	guidanceSkills,
	guidanceEdit,
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

func SharedGuidanceSteps() []string {
	return append([]string(nil), sharedGuidanceSteps...)
}

func StaticServerInstructions() string {
	return defaultServerInstructions
}
