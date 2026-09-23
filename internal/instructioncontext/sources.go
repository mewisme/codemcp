package instructioncontext

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
)

func DiscoverUserSources(home string, policy instructionpolicy.Config) ([]SourceSnapshot, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	values := make([]SourceSnapshot, 0)
	contexts := append(append([]memoryCandidate(nil), primaryUserMemoryCandidates...), fallbackUserMemoryCandidates...)
	for _, candidate := range contexts {
		path := filepath.Join(home, candidate.Relative)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		values = append(values, SourceSnapshot{Provider: candidate.Source, Kind: string(instructionpolicy.ResourceContext), Paths: []string{path}, Count: 1, Enabled: policy.Enabled(candidate.Source, instructionpolicy.ResourceContext)})
	}
	allRules, err := rules.DiscoverUser(home, instructionpolicy.DefaultConfig())
	if err != nil {
		return nil, err
	}
	values = appendSourceGroups(values, allRules, string(instructionpolicy.ResourceRules), policy)
	allSkills, err := skills.DiscoverUser(home, instructionpolicy.DefaultConfig())
	if err != nil {
		return nil, err
	}
	values = appendSkillSourceGroups(values, allSkills, policy)
	sort.Slice(values, func(i, j int) bool {
		if values[i].Provider != values[j].Provider {
			return sourceProviderPriority(values[i].Provider) < sourceProviderPriority(values[j].Provider)
		}
		return values[i].Kind < values[j].Kind
	})
	return values, nil
}

func appendSourceGroups(values []SourceSnapshot, discovered []rules.Rule, kind string, policy instructionpolicy.Config) []SourceSnapshot {
	groups := map[string][]string{}
	for _, item := range discovered {
		provider := instructionpolicy.ProviderID(item.Source)
		groups[provider] = append(groups[provider], item.Path)
	}
	for provider, paths := range groups {
		sort.Strings(paths)
		values = append(values, SourceSnapshot{Provider: provider, Kind: kind, Paths: paths, Count: len(paths), Enabled: policy.Enabled(provider, instructionpolicy.ResourceRules)})
	}
	return values
}

func appendSkillSourceGroups(values []SourceSnapshot, discovered []skills.Skill, policy instructionpolicy.Config) []SourceSnapshot {
	groups := map[string][]string{}
	for _, item := range discovered {
		provider := instructionpolicy.ProviderID(item.Source)
		groups[provider] = append(groups[provider], item.Path)
	}
	for provider, paths := range groups {
		sort.Strings(paths)
		values = append(values, SourceSnapshot{Provider: provider, Kind: string(instructionpolicy.ResourceSkills), Paths: paths, Count: len(paths), Enabled: policy.Enabled(provider, instructionpolicy.ResourceSkills)})
	}
	return values
}

func markLoadedSources(values []SourceSnapshot, memory ProjectMemoryBundle, loadedRules []rules.Rule, loadedSkills []skills.Skill) []SourceSnapshot {
	loadedPaths := map[string]bool{}
	for _, section := range memory.Sections {
		if section.Kind == SectionUser {
			loadedPaths[filepath.Clean(section.Path)] = true
		}
	}
	for _, rule := range loadedRules {
		loadedPaths[filepath.Clean(rule.Path)] = true
	}
	for _, skill := range loadedSkills {
		loadedPaths[filepath.Clean(skill.Path)] = true
	}
	for index := range values {
		if !values[index].Enabled {
			continue
		}
		for _, path := range values[index].Paths {
			if loadedPaths[filepath.Clean(path)] {
				values[index].Loaded = true
				break
			}
		}
	}
	return values
}

func sourceProviderPriority(provider string) int {
	switch instructionpolicy.ProviderID(provider) {
	case "agents":
		return 0
	case "claude":
		return 1
	case "claudes":
		return 2
	case "cursor":
		return 3
	case "codex":
		return 4
	default:
		return 5
	}
}
