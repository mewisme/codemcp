package instructioncontext

import (
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/skills"
)

var skillSourcePriority = map[string]int{
	".agents":  0,
	".claude":  1,
	".claudes": 2,
	".cursor":  3,
	".codex":   4,
}

func LoadSkillSummaries(root string) ([]skills.Skill, error) {
	values, err := skills.Discover(root)
	return filterSkillSummaries(values, err)
}

func LoadSkillSummariesWithUser(root, home string, policy instructionpolicy.Config) ([]skills.Skill, error) {
	values, err := skills.DiscoverWithUser(root, home, policy)
	return filterSkillSummaries(values, err)
}

func filterSkillSummaries(values []skills.Skill, err error) ([]skills.Skill, error) {
	if err != nil {
		return nil, err
	}
	sort.SliceStable(values, func(i, j int) bool {
		left, right := skillPriority(values[i].Source), skillPriority(values[j].Source)
		if left != right {
			return left < right
		}
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].Path < values[j].Path
	})
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

func skillPriority(source string) int {
	if priority, ok := skillSourcePriority[source]; ok {
		return priority
	}
	return len(skillSourcePriority)
}
