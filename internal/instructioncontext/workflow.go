package instructioncontext

import (
	"errors"
	"strings"
)

const (
	agentWorkflowIntroduction = "Use CodeMCP as a multi-workspace coding agent with explicit workspace targeting."
	serverIntroduction        = "Use CodeMCP for local, workspace-aware coding and project operations."
	serverWorkspaceBootstrap  = "For project work, use the workspace_id supplied by the MCP profile when available; otherwise use workspace_list to select an already registered workspace. Use workspace_status to inspect its registered root, persisted shell cwd, and allowed directories. Workspace registration is operator-owned and is not exposed as an agent tool. If the user provides a wsc_* workspace container, call workspace_container_context first and choose concrete member ws_* workspace IDs for actual work."
	serverContextBootstrap    = "Call agent_status when runtime or permission details are needed. If the client negotiated the native Skills extension, use skills/list and skills/get for Skill discovery/content and do not fetch the same Skill again through Tool fallback; otherwise use list_skills/load_skill."

	guidanceWorkspace        = "One MCP session may work across multiple registered workspaces. Every workspace-scoped call must explicitly target workspace_id; keep each workspace's project context, rules, memory, persisted shell cwd, REPL state, checkpoints, and assumptions isolated and never carry workspace-specific state into another workspace."
	guidanceContainer        = "Treat ws_* as an execution/filesystem workspace and wsc_* as a workspace-container orchestration scope. For wsc_*, call workspace_container_context first, choose member ws_* for concrete work, and call project_context with memory enabled per member. Never pass wsc_* as workspace_id to filesystem, Git, shell, checkpoint, memory, rule, or project tools; never merge member cwd, permissions, rules, memory, checkpoints, or assumptions."
	guidanceContext          = "At the start of every MCP session, fetch workspace memory by calling project_context with memory enabled before substantial work; repeat project_context before first work in each additional workspace targeted by that session. Treat project_context as the workspace instruction bundle and follow project/user instructions and unconditional rules from it before acting."
	guidanceRead             = "Before edits inspect relevant files with read_files/read_text_file and load_path_rules for matching path-scoped rules."
	guidanceSkills           = "When a skill is applicable, load body. Prefer skills/list+skills/get; else list_skills/load_skill. /plan|/create-plan=Plan Mode; /skill|/create-skill=create-skill; /rule|/create-rule=create-rule; /<skill-name>=exact, never fuzzy. No /<rule-name>. Execution: project_context plan_execution=true. raw-prompt interpretation belongs to the host agent."
	guidanceAgentDelegation  = "When fanout_turn is exposed for project work, consult it with the exact current user prompt. /fanout optionally selects a transient Fanout strategy for the current controller/workspace; it does not execute agent work. When returned Fanout guidance is active, automatically delegate safe, meaningful independent workstreams as soon as they are available; do not wait for the user to request fanout or choose a job count. Work directly only when no useful delegation exists or runtime/backend readiness, capacity, workspace authority, security, plan, approval, lifecycle, or completion rules block it. Generic MCP Tool calls do not carry the original raw user prompt, so slash interpretation belongs to the host agent."
	guidancePlanMode         = "Treat /plan and /create-plan as Plan Mode aliases when an exact standalone whitespace-delimited token. In Plan Mode: establish workspace; call project_context with memory enabled; inspect applicable rules and skills; audit relevant source plus persisted plan state; persist through create_plan and do not return only an unpersisted prose plan. Do not perform implementation mutations except workspace bootstrap and the final create_plan mutation; Plan Mode dominates contradictory same-request implementation wording: stop before implementation. If persistence fails, do not fall through to implementation. Normal agent_complete terminal semantics apply. Generic MCP servers cannot hard-block unrelated Tool calls because Tool requests lack the original raw user prompt; raw-prompt interpretation belongs to the host agent; first-party harnesses may map this to typed local Plan Mode."
	guidancePlanContinuation = "When continuing a persisted plan, exact plan_name wins. Without it, infer only when exactly one non-completed plan exists; if multiple non-completed plans exist, discovery is truncated, or diagnostics exist, do not guess from recency or filesystem order. Completed plans are not inferred. Before mutations call project_context with plan_execution=true so the bound next phase targets the trusted MCP session and workspace. Then read the full canonical .cm/plans/<name>.md document before implementation and its embedded Implementation order; there is no implement_plan tool. Treat plan name as cross-session identity and content_id as revision identity. Implement only the bound next phase. After validation persist the matching embedded Ordered phases entry with create_plan mode=update and the latest expected_content_id; verify completed_phase_count and next_phase, commit when required, then call agent_complete. status=completed is rejected while incomplete; partial, blocked, or cancelled release the ephemeral binding. Never update plan progress through generic file edits."
	guidanceEdit             = "Use the Tool capabilities block from project_context as a compact discovery summary for specialized tools. Prefer a relevant purpose-built tool when it materially helps, but capability discovery is guidance, not a requirement. The MCP tool schema and description are the source of truth for invocation and availability. Prefer apply_patch, edit_file, or multi_edit for file mutations; run_command remains valid for shell work in the persisted workspace cwd, and capability discovery never forbids shell equivalents."
	guidanceBackground       = "Use start_process for long work; completion is lifecycle-driven. Do not poll process_status/process_output or Tasks to wait. Status/output are inspection/recovery; stop_process is explicit cancellation/intervention. No model continuation is proven, so return control after starting background work. Intentional in-process sleep is valid."
	guidanceVerify           = "For non-trivial work, plan briefly, implement incrementally, and verify with relevant repository checks."
	guidanceRewind           = "Use rewind to inspect or recover automatic file checkpoints when an edit must be reviewed or reverted."
	guidanceRemember         = "When the user explicitly asks to remember eligible workspace-specific information, call remember immediately in that same turn; do not merely acknowledge or defer. Choose a concise scope and optional child key; never repeat the scope as its child key. Use memory_get, reconcile, then remember the complete canonical replacement note. A newer explicit preference supersedes conflicting older memory; rewrite instead of concatenating contradictory statements. Store only durable workspace-specific conclusions; do not store conversation history, secrets, transient state, or raw session IDs. When project_context recommends memory optimization and maintenance fits the task, use optimize_memory then reconcile with remember/forget."
	guidanceComplete         = "When agent_complete is available in the current tool profile and workspace work reaches a terminal state, finish verification first, then call it for each materially worked workspace as the final CodeMCP tool call before replying. Use completed only when finished; otherwise partial, blocked, or cancelled as applicable. Never call it for intermediate steps. MCP disconnect, transport close, or model-process exit alone is never successful completion; agent_complete records state but does not close the MCP transport or model process."
	guidanceMissing          = "Do not assume instructions, rules, skill bodies, Git state, or environment details that are absent from the supplied context. Query the appropriate tool instead of guessing."
	guidanceScope            = "Preserve unrelated user changes and keep mutations scoped to the requested task."

	DefaultAgentWorkflow = agentWorkflowIntroduction + "\n\n" +
		"1. " + guidanceWorkspace + "\n" +
		"2. " + guidanceContainer + "\n" +
		"3. " + guidanceContext + "\n" +
		"4. " + guidanceRead + "\n" +
		"5. " + guidanceSkills + "\n" +
		"6. " + guidanceAgentDelegation + "\n" +
		"7. " + guidanceEdit + "\n" +
		"8. " + guidanceBackground + "\n" +
		"9. " + guidanceVerify + "\n" +
		"10. " + guidanceRewind + "\n" +
		"11. " + guidanceRemember + "\n" +
		"12. " + guidanceComplete + "\n" +
		"13. " + guidanceMissing + "\n" +
		"14. " + guidanceScope
)

const PlanModeDirectiveToken = "/plan"

type SlashDirectiveKind string

const (
	SlashDirectiveNone           SlashDirectiveKind = ""
	SlashDirectivePlanMode       SlashDirectiveKind = "plan"
	SlashDirectiveSkillAuthoring SlashDirectiveKind = "skill-authoring"
	SlashDirectiveRuleAuthoring  SlashDirectiveKind = "rule-authoring"
	SlashDirectiveSkill          SlashDirectiveKind = "skill"
)

var ErrAmbiguousSlashDirective = errors.New("ambiguous slash directive")

type SlashDirective struct {
	Kind       SlashDirectiveKind
	SkillName  string
	FanoutMode string
}

type slashDirectiveAlias struct {
	name      string
	directive SlashDirective
}

var coreSlashDirectiveAliases = []slashDirectiveAlias{
	{name: "plan", directive: SlashDirective{Kind: SlashDirectivePlanMode}},
	{name: "create-plan", directive: SlashDirective{Kind: SlashDirectivePlanMode}},
	{name: "skill", directive: SlashDirective{Kind: SlashDirectiveSkillAuthoring, SkillName: "create-skill"}},
	{name: "create-skill", directive: SlashDirective{Kind: SlashDirectiveSkillAuthoring, SkillName: "create-skill"}},
	{name: "rule", directive: SlashDirective{Kind: SlashDirectiveRuleAuthoring, SkillName: "create-rule"}},
	{name: "create-rule", directive: SlashDirective{Kind: SlashDirectiveRuleAuthoring, SkillName: "create-rule"}},
}

const fanoutSlashDirectiveName = "fanout"

var ErrAmbiguousFanoutMode = errors.New("ambiguous fanout mode")

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
			{ID: "agent-delegation", Text: guidanceAgentDelegation},
			{ID: "plan-mode", Text: guidancePlanMode},
			{ID: "plan-continuation", Text: guidancePlanContinuation},
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
	guidanceAgentDelegation,
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

func CoreSlashDirectiveNames() []string {
	result := make([]string, 0, len(coreSlashDirectiveAliases)+1)
	for _, alias := range coreSlashDirectiveAliases {
		result = append(result, alias.name)
	}
	result = append(result, fanoutSlashDirectiveName)
	return result
}

func ResolveSlashDirective(prompt string, skillNames []string) (SlashDirective, error) {
	knownSkills := make(map[string]struct{}, len(skillNames))
	for _, name := range skillNames {
		name = strings.TrimSpace(name)
		if name != "" {
			knownSkills[name] = struct{}{}
		}
	}

	fields := strings.Fields(prompt)
	recognized := make([]SlashDirective, 0)
	fanoutMode := ""
	fanoutConflict := false
	for index := 0; index < len(fields); index++ {
		token := fields[index]
		name, ok := slashDirectiveName(token)
		if !ok {
			continue
		}
		if name == fanoutSlashDirectiveName {
			mode := "default"
			if index+1 < len(fields) && isFanoutMode(fields[index+1]) {
				mode = fields[index+1]
				index++
			}
			if fanoutMode != "" && fanoutMode != mode {
				fanoutConflict = true
			} else {
				fanoutMode = mode
			}
			continue
		}
		directive, isCore := coreSlashDirective(name)
		if !isCore {
			if _, exists := knownSkills[name]; !exists {
				continue
			}
			directive = SlashDirective{Kind: SlashDirectiveSkill, SkillName: name}
		}
		if directive.Kind == SlashDirectivePlanMode {
			return directive, nil
		}
		recognized = append(recognized, directive)
	}

	resolved := SlashDirective{}
	for _, directive := range recognized {
		if resolved.Kind == SlashDirectiveNone {
			resolved = directive
			continue
		}
		if resolved != directive {
			return SlashDirective{}, ErrAmbiguousSlashDirective
		}
	}
	if fanoutConflict {
		return SlashDirective{}, ErrAmbiguousFanoutMode
	}
	resolved.FanoutMode = fanoutMode
	return resolved, nil
}

func RequestsPlanMode(prompt string) bool {
	directive, err := ResolveSlashDirective(prompt, nil)
	return err == nil && directive.Kind == SlashDirectivePlanMode
}

func coreSlashDirective(name string) (SlashDirective, bool) {
	for _, alias := range coreSlashDirectiveAliases {
		if alias.name == name {
			return alias.directive, true
		}
	}
	return SlashDirective{}, false
}

func slashDirectiveName(token string) (string, bool) {
	if len(token) < 2 || token[0] != '/' || token[1] == '/' {
		return "", false
	}
	name := token[1:]
	if strings.ContainsRune(name, '/') {
		return "", false
	}
	return name, true
}

func isFanoutMode(token string) bool {
	switch token {
	case "off", "auto", "conservative", "aggressive":
		return true
	default:
		return false
	}
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
