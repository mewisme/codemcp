package instructioncontext

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/rules"
)

func LoadUnconditionalRules(root string) ([]rules.Rule, error) {
	all, err := rules.Discover(root)
	return filterUnconditionalRules(all, err)
}

func LoadUnconditionalRulesWithUser(root, home string, policy instructionpolicy.Config) ([]rules.Rule, error) {
	return LoadUnconditionalRulesWithUserForWorkspace(root, root, home, policy)
}

func LoadUnconditionalRulesWithUserForWorkspace(root, workspaceRoot, home string, policy instructionpolicy.Config) ([]rules.Rule, error) {
	all, err := rules.DiscoverWithUserForWorkspace(root, workspaceRoot, home, policy)
	return filterUnconditionalRules(all, err)
}

func filterUnconditionalRules(all []rules.Rule, err error) ([]rules.Rule, error) {
	if err != nil {
		return nil, err
	}
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

func ruleContentID(content string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
