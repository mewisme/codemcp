package instructioncontext

import (
	"strings"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/skills"
)

func LoadSkillSummaries(root string) ([]skills.Skill, error) {
	values, err := skills.Discover(root)
	return filterSkillSummaries(values, err)
}

func LoadSkillSummariesWithUser(root, home string, policy instructionpolicy.Config) ([]skills.Skill, error) {
	return LoadSkillSummariesWithUserForWorkspace(root, root, home, policy)
}

func LoadSkillSummariesWithUserForWorkspace(root, workspaceRoot, home string, policy instructionpolicy.Config) ([]skills.Skill, error) {
	values, err := skills.DiscoverWithUserForWorkspace(root, workspaceRoot, home, policy)
	return filterSkillSummaries(values, err)
}

func filterSkillSummaries(values []skills.Skill, err error) ([]skills.Skill, error) {
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := make([]skills.Skill, 0, len(values))
	for _, skill := range values {
		key := strings.Join([]string{strings.TrimSpace(skill.Name), strings.TrimSpace(skill.Description), strings.TrimSpace(skill.Source)}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, skill)
	}
	return result, nil
}
