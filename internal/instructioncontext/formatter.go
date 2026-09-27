package instructioncontext

import (
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
)

const DefaultInstructionMaxBytes = 100_000

const QuickPointers = `- Use load_path_rules(path) before editing files covered by path-scoped rules.
- Use load_skill(name) only for skills whose summaries match the current task.
- Use workspace_status when workspace root, persisted cwd, or allowed directories need to be re-checked.
- At the start of every MCP session, call project_context with memory enabled before substantial workspace work; repeat it before first work in each additional workspace.
- When the user explicitly asks to remember/save/persist an eligible workspace note, identify a scope and an optional child key. Omit key for a scope-level note and never repeat the scope as its child key. Call memory_get for the target scope/key, reconcile current and new information, then call remember with the complete canonical replacement. New explicit user preferences supersede conflicting older memory; never concatenate contradictions. Use rewind for checkpoint inspection or recovery.`

func FormatInstructions(value InstructionContext) (string, int) {
	return renderInstructionBlocks(instructionBlocks(value))
}

type instructionBlock struct {
	title    string
	content  string
	required bool
}

func instructionBlocks(value InstructionContext) []instructionBlock {
	workflow := strings.TrimSpace(value.AgentWorkflow)
	if workflow == "" {
		workflow = AgentWorkflow()
	}
	blocks := []instructionBlock{
		{title: "Agent workflow", content: workflow, required: true},
		{title: "Tool profile", content: formatToolProfile(value.ToolProfile), required: true},
		{title: "Environment", content: formatEnvironment(value.Environment), required: true},
	}
	if !value.Git.Skipped {
		blocks = append(blocks, instructionBlock{title: "Git", content: formatGit(value.Git)})
	}
	if value.AutoMemory.Loaded && strings.TrimSpace(value.AutoMemory.Content) != "" {
		blocks = append(blocks, instructionBlock{title: "Auto memory", content: shiftMarkdownHeadings(strings.TrimSpace(value.AutoMemory.Content), 1)})
	}
	if strings.TrimSpace(value.GlobalContext) != "" {
		blocks = append(blocks, instructionBlock{title: "Global context", content: strings.TrimSpace(value.GlobalContext)})
	}
	user, project := splitMemorySections(value.ProjectMemory.Sections)
	if user != "" {
		blocks = append(blocks, instructionBlock{title: "User instructions", content: user})
	}
	if project != "" {
		blocks = append(blocks, instructionBlock{title: "Project instructions", content: project})
	}
	if globalRulesText := formatRules(value.GlobalRules); globalRulesText != "" {
		blocks = append(blocks, instructionBlock{title: "Global rules", content: globalRulesText})
	}
	if rulesText := formatRules(value.Rules); rulesText != "" {
		blocks = append(blocks, instructionBlock{title: "Always-on rules", content: rulesText})
	}
	if skillsText := formatSkills(value.Skills); skillsText != "" {
		blocks = append(blocks, instructionBlock{title: "Skills", content: skillsText})
	}
	if integrationText := formatIntegrationInstructions(value.IntegrationInstructions); integrationText != "" {
		blocks = append(blocks, instructionBlock{title: "Integration instructions", content: integrationText, required: true})
	}
	blocks = append(blocks, instructionBlock{title: "Quick pointers", content: QuickPointers, required: true})
	return blocks
}

func renderInstructionBlocks(blocks []instructionBlock) (string, int) {
	rendered := make([]string, 0, len(blocks))
	for _, block := range blocks {
		rendered = append(rendered, formatBlock(block.title, block.content))
	}
	text := "# CodeMCP project context\n\n" + strings.Join(rendered, "\n\n")
	return text, len([]byte(text))
}

func formatIntegrationInstructions(values []IntegrationInstruction) string {
	sections := make([]string, 0, len(values))
	for _, value := range values {
		content := strings.TrimSpace(value.Content)
		if content == "" {
			continue
		}
		title := strings.TrimSpace(value.ID)
		if title == "" {
			title = "integration"
		}
		if source := strings.TrimSpace(value.Source); source != "" {
			title += " [" + source + "]"
		}
		sections = append(sections, "### "+title+"\n"+content)
	}
	return strings.Join(sections, "\n\n")
}

func ApplyFormattedInstructions(value *InstructionContext) {
	if value == nil {
		return
	}
	if strings.TrimSpace(value.AgentWorkflow) == "" {
		value.AgentWorkflow = AgentWorkflow()
	}
	value.InstructionsText, value.InstructionBytes = FormatInstructions(*value)
	value.InstructionTruncated = false
	value.InstructionBudget = nil
}

func ApplyFormattedInstructionsLimit(value *InstructionContext, maxBytes int) error {
	return ApplyFormattedInstructionsLimitWithPriority(value, maxBytes, nil)
}

func ApplyFormattedInstructionsLimitWithPriority(value *InstructionContext, maxBytes int, optionalPriority []string) error {
	if value == nil {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = DefaultInstructionMaxBytes
	}
	if strings.TrimSpace(value.AgentWorkflow) == "" {
		value.AgentWorkflow = AgentWorkflow()
	}
	blocks := instructionBlocks(*value)
	required := make([]instructionBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.required {
			required = append(required, block)
		}
	}
	_, minimumBytes := renderInstructionBlocks(required)
	if minimumBytes > maxBytes {
		return fmt.Errorf("max instruction bytes %d cannot hold required project context minimum %d", maxBytes, minimumBytes)
	}
	selectedTitles := map[string]bool{}
	for _, block := range required {
		selectedTitles[block.title] = true
	}
	selectionBlocks := optionalSelectionOrder(blocks, optionalPriority)
	for _, block := range selectionBlocks {
		if block.required {
			continue
		}
		candidateTitles := make(map[string]bool, len(selectedTitles)+1)
		for title := range selectedTitles {
			candidateTitles[title] = true
		}
		candidateTitles[block.title] = true
		candidate := canonicalInstructionBlockSubset(blocks, candidateTitles)
		_, size := renderInstructionBlocks(candidate)
		if size <= maxBytes {
			selectedTitles[block.title] = true
		}
	}
	selected := canonicalInstructionBlockSubset(blocks, selectedTitles)
	text, size := renderInstructionBlocks(selected)
	if size > maxBytes {
		return errors.New("project context instruction budget invariant violated")
	}
	value.InstructionsText, value.InstructionBytes = text, size
	value.InstructionTruncated = len(selected) != len(blocks)
	value.InstructionBudget = make([]InstructionBlockBudget, 0, len(blocks))
	for _, block := range blocks {
		rendered := formatBlock(block.title, block.content)
		included := selectedTitles[block.title]
		renderedBytes := 0
		if included {
			renderedBytes = len([]byte(rendered))
		}
		value.InstructionBudget = append(value.InstructionBudget, InstructionBlockBudget{
			Title: block.title, Required: block.required, Included: included, Truncated: !included,
			OriginalBytes: len([]byte(rendered)), RenderedBytes: renderedBytes,
		})
	}
	return nil
}

func optionalSelectionOrder(blocks []instructionBlock, priority []string) []instructionBlock {
	if len(priority) == 0 {
		return blocks
	}
	byTitle := make(map[string]instructionBlock, len(blocks))
	for _, block := range blocks {
		byTitle[block.title] = block
	}
	result := make([]instructionBlock, 0, len(blocks))
	seen := map[string]bool{}
	appendTitle := func(title string) {
		block, ok := byTitle[title]
		if !ok || seen[title] {
			return
		}
		seen[title] = true
		result = append(result, block)
	}
	for _, block := range blocks {
		if block.required {
			appendTitle(block.title)
		}
	}
	// Policy/rule blocks remain deterministic and outrank semantic context.
	appendTitle("Global rules")
	appendTitle("Always-on rules")
	for _, title := range priority {
		appendTitle(strings.TrimSpace(title))
	}
	for _, block := range blocks {
		appendTitle(block.title)
	}
	return result
}

func canonicalInstructionBlockSubset(all []instructionBlock, titles map[string]bool) []instructionBlock {
	result := make([]instructionBlock, 0, len(titles))
	for _, block := range all {
		if titles[block.title] {
			result = append(result, block)
		}
	}
	return result
}

func formatBlock(title, content string) string {
	return "## " + title + "\n" + strings.TrimSpace(content)
}

func shiftMarkdownHeadings(value string, levels int) string {
	if levels <= 0 {
		return value
	}
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || trimmed[0] != '#' {
			continue
		}
		headingLevel := 0
		for headingLevel < len(trimmed) && headingLevel < 6 && trimmed[headingLevel] == '#' {
			headingLevel++
		}
		if headingLevel == 0 || headingLevel >= len(trimmed) || trimmed[headingLevel] != ' ' {
			continue
		}
		shiftedLevel := headingLevel + levels
		if shiftedLevel > 6 {
			shiftedLevel = 6
		}
		indent := line[:len(line)-len(trimmed)]
		lines[index] = indent + strings.Repeat("#", shiftedLevel) + trimmed[headingLevel:]
	}
	return strings.Join(lines, "\n")
}

func formatToolProfile(profile ToolProfile) string {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("- name: %s\n- tools: %d", name, profile.Count)
}

func formatEnvironment(env EnvironmentSnapshot) string {
	lines := []string{
		"- platform: " + displayValue(env.Platform),
		"- os: " + displayValue(env.OS),
		"- arch: " + displayValue(env.Arch),
		"- go: " + displayValue(env.Go),
		fmt.Sprintf("- pid: %d", env.PID),
		"- workspace_id: " + displayValue(env.WorkspaceID),
		"- workspace_root: " + displayValue(env.WorkspaceRoot),
		"- cwd: " + displayValue(env.CWD),
	}
	if len(env.EffectiveRoots) == 0 {
		lines = append(lines, "- effective_roots: none")
	} else {
		lines = append(lines, "- effective_roots:")
		for _, root := range env.EffectiveRoots {
			lines = append(lines, "  - "+root)
		}
	}
	if env.Admin.Enabled {
		lines = append(lines, "- admin: "+displayValue(env.Admin.URL))
	} else {
		lines = append(lines, "- admin: disabled")
	}
	return strings.Join(lines, "\n")
}

func formatGit(snapshot GitSnapshot) string {
	if !snapshot.IsRepo {
		if strings.TrimSpace(snapshot.Error) != "" {
			return "- repository: false\n- error: " + strings.TrimSpace(snapshot.Error)
		}
		return "- repository: false"
	}
	branch := strings.TrimSpace(snapshot.Branch)
	if branch == "" {
		branch = "(detached)"
	}
	lines := []string{"- repository: true", "- root: " + displayValue(snapshot.Root), "- branch: " + branch}
	if strings.TrimSpace(snapshot.StatusShort) != "" {
		lines = append(lines, "- status:", indentLines(snapshot.StatusShort, "    "))
	}
	if snapshot.StatusTruncated {
		lines = append(lines, "- status_truncated: true")
	}
	if len(snapshot.RecentCommits) > 0 {
		lines = append(lines, "- recent_commits:")
		for _, commit := range snapshot.RecentCommits {
			if commit = strings.TrimSpace(commit); commit != "" {
				lines = append(lines, "  - "+commit)
			}
		}
	}
	if strings.TrimSpace(snapshot.Error) != "" {
		lines = append(lines, "- error: "+strings.TrimSpace(snapshot.Error))
	}
	return strings.Join(lines, "\n")
}

func splitMemorySections(sections []Section) (string, string) {
	user := make([]string, 0)
	project := make([]string, 0)
	for _, section := range sections {
		formatted := formatMemorySection(section)
		if formatted == "" {
			continue
		}
		switch section.Kind {
		case SectionUser:
			user = append(user, formatted)
		case SectionProject:
			project = append(project, formatted)
		}
	}
	return strings.Join(user, "\n\n"), strings.Join(project, "\n\n")
}

func formatMemorySection(section Section) string {
	content := strings.TrimSpace(section.Content)
	if content == "" {
		return ""
	}
	meta := "### " + displayValue(section.Path)
	if source := strings.TrimSpace(section.Source); source != "" {
		meta += " [" + source + "]"
	}
	if section.Truncated {
		meta += " (truncated)"
	}
	return meta + "\n" + content
}

func formatRules(values []rules.Rule) string {
	sections := make([]string, 0, len(values))
	for _, rule := range values {
		content := strings.TrimSpace(rule.Content)
		if content == "" {
			continue
		}
		title := "### " + displayValue(rule.Path)
		if source := strings.TrimSpace(rule.Source); source != "" {
			title += " [" + source + "]"
		}
		sections = append(sections, title+"\n"+content)
	}
	return strings.Join(sections, "\n\n")
}

func formatSkills(values []skills.Skill) string {
	lines := make([]string, 0, len(values)+1)
	for _, skill := range values {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		description := strings.TrimSpace(skill.Description)
		if description == "" {
			description = name
		}
		line := "- " + name + ": " + description
		if source := strings.TrimSpace(skill.Source); source != "" {
			line += " [" + source + "]"
		}
		if path := strings.TrimSpace(skill.Path); path != "" {
			line += " (" + path + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	lines = append(lines, "Load an applicable skill with load_skill using its exact name before following its workflow.")
	return strings.Join(lines, "\n")
}

func indentLines(value, prefix string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(value), "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func displayValue(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unknown"
}
