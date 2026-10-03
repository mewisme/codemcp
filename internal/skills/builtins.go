package skills

import "strings"

const (
	BuiltinSource          = "codemcp"
	BuiltinCreateRuleName  = "create-rule"
	BuiltinCreateSkillName = "create-skill"
	BuiltinCreatePlanName  = "create-plan"
)

const builtinCreateRuleContent = `---
name: create-rule
description: Create or update a CodeMCP-native workspace rule through the canonical authoring tool.
---
# Create a CodeMCP rule

Use the create_rule tool for CodeMCP-native rule authoring.

- The agent tool is workspace-scoped. Pass the registered workspace_id; do not request a global destination.
- Set mode to create for a new rule or update for an existing native rule.
- Use a canonical lowercase name containing letters, digits, and hyphens.
- For an always-on rule set always_apply: true and omit globs.
- For a scoped rule set always_apply: false and provide one or more forward-slash globs.
- Put the rule body in content. Use dry_run: true to validate without mutation when useful.
- CodeMCP chooses and protects the native destination; do not construct a provider path yourself.

If native authoring fails, report the authoring error. Do not use generic file, patch, or shell tools as an equivalent fallback to write the rule.
`

const builtinCreateSkillContent = `---
name: create-skill
description: Create or update a CodeMCP-native workspace skill through the canonical authoring tool.
---
# Create a CodeMCP skill

Use the create_skill tool for CodeMCP-native skill authoring.

- The agent tool is workspace-scoped. Pass the registered workspace_id; do not request a global destination.
- Set mode to create or update.
- Use a canonical lowercase name containing letters, digits, and hyphens.
- Provide a concise one-line description and the complete skill body in instructions.
- Supporting files use relative forward-slash paths only. Do not use absolute paths, parent traversal, volume prefixes, or backslashes.
- Mark a supporting file executable: true only when executable metadata is intentionally required.
- Use dry_run: true to validate the complete skill tree without mutation when useful.
- CodeMCP stages and activates the native skill tree atomically; do not construct provider paths yourself.

If native authoring fails, report the authoring error. Do not use generic file, patch, or shell tools as an equivalent fallback to write the skill.
`

const builtinCreatePlanContent = `---
name: create-plan
description: Create or update a canonical CodeMCP workspace implementation plan through the plan authoring tool.
---
# Create a CodeMCP plan

Use the create_plan tool for canonical workspace plan authoring.

- Treat Plan Mode as a planning workflow: inspect the current workspace architecture, relevant constraints, and existing implementation state before authoring or revising the plan.
- The agent tool is workspace-scoped. Pass the registered workspace_id; global and provider-owned instruction destinations are not valid plan targets.
- Use a canonical lowercase name containing letters, digits, and hyphens. The name is the durable semantic identity of the plan.
- Put the human-readable implementation plan in plan_content. Include the goal, architecture contract, ordered implementation phases, and acceptance criteria needed to execute the work safely.
- Put the execution sequence in implementation_order. It must contain execution rules, ordering rationale, ordered phase checklists, and terminal acceptance.
- Set mode to create for a new plan. For update, pass the current expected_content_id so stale sessions cannot overwrite newer progress.
- Represent progress through the canonical checklist state in the document. Do not maintain a separate active-plan pointer or progress file.
- Plan Mode is planning-only: author or revise the canonical document through create_plan, then stop before implementation. Do not enable plan execution binding merely because a plan was created or edited.
- To implement a persisted plan, select the exact plan when multiple non-completed plans exist and call project_context with plan_execution=true before implementation mutations. This binds the canonical current next phase to the trusted MCP session and workspace.
- Implement exactly the bound next phase. Complete its validation before changing progress state.
- Before agent_complete(status=completed), call create_plan mode=update with the latest expected_content_id and mark every finished task/validation item in the phase plus the matching phase under the embedded Ordered phases checklist. Verify the returned completed_phase_count and next_phase reflect exactly one phase advance.
- If create_plan reports stale state, refresh project_context/read the latest canonical plan and retry from the newest content_id; never bypass the conflict with generic file edits.
- If the phase ends partial, blocked, or cancelled, do not check unfinished plan items. The terminal completion releases the ephemeral execution binding so a later session can resume the same canonical next phase.
- Use dry_run: true to validate target resolution, document structure, conflicts, and stale state without mutation when useful.
- CodeMCP owns the canonical plan destination. Do not use generic file, patch, move, delete, or shell tools to mutate .cm/plans.

If plan authoring fails, report the bounded authoring error and resolve the conflict or stale state explicitly. Do not bypass create_plan with an equivalent filesystem mutation.
`

type builtinSkill struct {
	skill   Skill
	content string
}

var builtinSkillCatalog = []builtinSkill{
	{
		skill: Skill{
			Name:        BuiltinCreateRuleName,
			Description: "Create or update a CodeMCP-native workspace rule through the canonical authoring tool.",
			Path:        "codemcp://skills/create-rule",
			Source:      BuiltinSource,
		},
		content: builtinCreateRuleContent,
	},
	{
		skill: Skill{
			Name:        BuiltinCreateSkillName,
			Description: "Create or update a CodeMCP-native workspace skill through the canonical authoring tool.",
			Path:        "codemcp://skills/create-skill",
			Source:      BuiltinSource,
		},
		content: builtinCreateSkillContent,
	},
	{
		skill: Skill{
			Name:        BuiltinCreatePlanName,
			Description: "Create or update a canonical CodeMCP workspace implementation plan through the plan authoring tool.",
			Path:        "codemcp://skills/create-plan",
			Source:      BuiltinSource,
		},
		content: builtinCreatePlanContent,
	},
}

func BuiltinSkills() []Skill {
	result := make([]Skill, 0, len(builtinSkillCatalog))
	for _, item := range builtinSkillCatalog {
		result = append(result, item.skill)
	}
	return result
}

func IsReservedName(name string) bool {
	name = strings.TrimSpace(name)
	for _, item := range builtinSkillCatalog {
		if item.skill.Name == name {
			return true
		}
	}
	return false
}

func IsBuiltin(skill Skill) bool {
	if skill.Source != BuiltinSource {
		return false
	}
	for _, item := range builtinSkillCatalog {
		if item.skill.Name == skill.Name && item.skill.Path == skill.Path {
			return true
		}
	}
	return false
}

func builtinLoaded(name string, maxBytes int) (Loaded, bool) {
	for _, item := range builtinSkillCatalog {
		if item.skill.Name != name {
			continue
		}
		data := []byte(item.content)
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
		}
		return Loaded{Skill: item.skill, Content: string(data), Truncated: truncated}, true
	}
	return Loaded{}, false
}

func appendReservedBuiltins(values []Skill) []Skill {
	result := make([]Skill, 0, len(values)+len(builtinSkillCatalog))
	for _, skill := range values {
		if IsReservedName(skill.Name) {
			continue
		}
		result = append(result, skill)
	}
	return append(result, BuiltinSkills()...)
}
