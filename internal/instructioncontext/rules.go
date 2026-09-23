package instructioncontext

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/rules"
)

var ruleSourcePriority = map[string]int{
	".agents":  0,
	".claude":  1,
	".claudes": 2,
	".cursor":  3,
	".codex":   4,
}

func LoadUnconditionalRules(root string) ([]rules.Rule, error) {
	all, err := rules.Discover(root)
	return filterUnconditionalRules(all, err)
}

func LoadUnconditionalRulesWithUser(root, home string, policy instructionpolicy.Config) ([]rules.Rule, error) {
	all, err := rules.DiscoverWithUser(root, home, policy)
	return filterUnconditionalRules(all, err)
}

func filterUnconditionalRules(all []rules.Rule, err error) ([]rules.Rule, error) {
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		left, right := rulePriority(all[i].Source), rulePriority(all[j].Source)
		if left != right {
			return left < right
		}
		return all[i].Path < all[j].Path
	})
	filtered := make([]rules.Rule, 0, len(all))
	seen := map[string]bool{}
	for _, rule := range all {
		if !rule.AlwaysApply && len(rule.Patterns) > 0 {
			continue
		}
		contentID := ruleContentID(rule.Content)
		if seen[contentID] {
			continue
		}
		seen[contentID] = true
		filtered = append(filtered, rule)
	}
	return filtered, nil
}

func rulePriority(source string) int {
	if priority, ok := ruleSourcePriority[source]; ok {
		return priority
	}
	return len(ruleSourcePriority)
}

func ruleContentID(content string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
