package rules

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/instructionsource"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func Discover(workspaceRoot string) ([]Rule, error) {
	return DiscoverForWorkspace(workspaceRoot, workspaceRoot)
}

func DiscoverForWorkspace(projectRoot, workspaceRoot string) ([]Rule, error) {
	result := make([]Rule, 0)
	walkRules(workspacestate.New(workspaceRoot).RulesRoot(), instructionsource.NativeSource, 0, &result)
	providers, err := instructionsource.DiscoverDynamicProviders(projectRoot)
	if err != nil {
		return nil, err
	}
	for _, provider := range providers {
		if provider.RulesDir != "" {
			walkRules(provider.RulesDir, provider.Name, 0, &result)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return workspaceRuleLess(result[i], result[j])
	})
	return result, nil
}

func DiscoverUser(home string, policy instructionpolicy.Config) ([]Rule, error) {
	_ = home
	_ = policy
	result := make([]Rule, 0)
	walkRules(filepath.Join(configformat.RootPath(), "rules"), instructionsource.NativeSource, 0, &result)
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func DiscoverWithUser(workspaceRoot, home string, policy instructionpolicy.Config) ([]Rule, error) {
	return DiscoverWithUserForWorkspace(workspaceRoot, workspaceRoot, home, policy)
}

func DiscoverWithUserForWorkspace(projectRoot, workspaceRoot, home string, policy instructionpolicy.Config) ([]Rule, error) {
	project, err := DiscoverForWorkspace(projectRoot, workspaceRoot)
	if err != nil {
		return nil, err
	}
	user, err := DiscoverUser(home, policy)
	if err != nil {
		return nil, err
	}
	result := make([]Rule, 0, len(project)+len(user))
	for _, rule := range project {
		if rule.Source == instructionsource.NativeSource {
			result = append(result, rule)
		}
	}
	for _, rule := range user {
		if rule.Source == instructionsource.NativeSource {
			result = append(result, rule)
		}
	}
	for _, rule := range project {
		if rule.Source != instructionsource.NativeSource && policy.Enabled(rule.Source, instructionpolicy.ResourceRules) {
			result = append(result, rule)
		}
	}
	return result, nil
}

func workspaceRuleLess(left, right Rule) bool {
	leftSource := instructionsource.Source{Class: instructionsource.ClassDynamicProvider, Provider: left.Source, Path: left.Path}
	rightSource := instructionsource.Source{Class: instructionsource.ClassDynamicProvider, Provider: right.Source, Path: right.Path}
	if left.Source == instructionsource.NativeSource {
		leftSource.Class = instructionsource.ClassWorkspaceNative
	}
	if right.Source == instructionsource.NativeSource {
		rightSource.Class = instructionsource.ClassWorkspaceNative
	}
	if value := instructionsource.Compare(leftSource, rightSource); value != 0 {
		return value < 0
	}
	return left.Path < right.Path
}

func LoadForFile(workspaceRoot, file string) ([]Rule, error) {
	all, err := Discover(workspaceRoot)
	if err != nil {
		return nil, err
	}
	matched := make([]Rule, 0)
	for _, rule := range all {
		if Match(rule, workspaceRoot, file) {
			matched = append(matched, rule)
		}
	}
	return matched, nil
}

func LoadForFileWithUser(workspaceRoot, file, home string, policy instructionpolicy.Config) ([]Rule, error) {
	all, err := DiscoverWithUser(workspaceRoot, home, policy)
	if err != nil {
		return nil, err
	}
	matched := make([]Rule, 0)
	for _, rule := range all {
		if Match(rule, workspaceRoot, file) {
			matched = append(matched, rule)
		}
	}
	return matched, nil
}

func walkRules(dir, source string, depth int, result *[]Rule) {
	if depth > 3 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			walkRules(path, source, depth+1, result)
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".md" && extension != ".mdc" {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		patterns, alwaysApply, body := parseRule(string(data))
		if strings.TrimSpace(body) == "" {
			continue
		}
		if len(body) > 4000 {
			body = body[:4000]
		}
		*result = append(*result, Rule{
			Path: path, Source: source, Patterns: patterns, Content: body, AlwaysApply: alwaysApply,
		})
	}
}

func parseRule(raw string) ([]string, bool, string) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, false, strings.TrimSpace(normalized)
	}
	rest := normalized[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, false, strings.TrimSpace(normalized)
	}
	header := rest[:end]
	body := strings.TrimSpace(rest[end+len("\n---"):])
	patterns := make([]string, 0)
	alwaysApply := false
	activeList := ""
	for _, line := range strings.Split(header, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") && activeList != "" {
			value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), `"'`)
			if value != "" {
				patterns = append(patterns, value)
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			activeList = ""
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "paths", "globs":
			activeList = key
			if value != "" {
				for _, item := range strings.Split(strings.Trim(value, "[]"), ",") {
					item = strings.Trim(strings.TrimSpace(item), `"'`)
					if item != "" {
						patterns = append(patterns, item)
					}
				}
			}
		case "alwaysApply", "always_apply":
			activeList = ""
			alwaysApply = strings.EqualFold(strings.Trim(value, `"'`), "true")
		default:
			activeList = ""
		}
	}
	return unique(patterns), alwaysApply, body
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
