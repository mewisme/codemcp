package skills

import "strings"

const (
	BuiltinSource          = "codemcp"
	BuiltinCreateRuleName  = "create-rule"
	BuiltinCreateSkillName = "create-skill"
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
