package instructioncontext

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/instructionsource"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func DiscoverUserSources(home string, policy instructionpolicy.Config) ([]SourceSnapshot, error) {
	_ = policy
	providers, err := instructionsource.DiscoverDynamicProviders(home)
	if err != nil {
		return nil, err
	}
	values := make([]SourceSnapshot, 0, len(providers)*3)
	for _, provider := range providers {
		appendSnapshot := func(kind string, paths []string, enabled bool) {
			if len(paths) == 0 {
				return
			}
			paths = append([]string(nil), paths...)
			sort.Strings(paths)
			values = append(values, SourceSnapshot{
				Provider: instructionpolicy.ProviderID(provider.Name), Kind: kind, Scope: "user-provider",
				Paths: paths, Count: len(paths), Enabled: enabled, Loaded: false,
			})
		}
		appendSnapshot(string(instructionpolicy.ResourceContext), provider.ContextFiles, true)
		if provider.RulesDir != "" {
			appendSnapshot(string(instructionpolicy.ResourceRules), discoverRegularFiles(provider.RulesDir, 3, map[string]bool{".md": true, ".mdc": true}), true)
		}
		if provider.SkillsDir != "" {
			appendSnapshot(string(instructionpolicy.ResourceSkills), discoverSkillFiles(provider.SkillsDir, 3), true)
		}
	}
	sortSnapshots(values)
	return values, nil
}

func LoadedProjectSources(memory ProjectMemoryBundle, loadedRules []rules.Rule, loadedSkills []skills.Skill, workspaceRoot string) []SourceSnapshot {
	type key struct{ provider, kind, scope string }
	groups := map[key][]string{}
	scopeOf := func(source, path string) string {
		clean := filepath.Clean(path)
		if source == skills.BuiltinSource {
			return "builtin"
		}
		if source == instructionsource.NativeSource {
			if withinSourceRoot(clean, workspacestate.New(workspaceRoot).Root()) {
				return "workspace-native"
			}
			if withinSourceRoot(clean, configformat.RootPath()) {
				return "global-native"
			}
		}
		if strings.HasPrefix(source, ".") {
			return "workspace-provider"
		}
		return "project-root"
	}
	for _, section := range memory.Sections {
		k := key{section.Source, string(instructionpolicy.ResourceContext), scopeOf(section.Source, section.Path)}
		groups[k] = append(groups[k], section.Path)
	}
	for _, rule := range loadedRules {
		k := key{rule.Source, string(instructionpolicy.ResourceRules), scopeOf(rule.Source, rule.Path)}
		groups[k] = append(groups[k], rule.Path)
	}
	for _, skill := range loadedSkills {
		k := key{skill.Source, string(instructionpolicy.ResourceSkills), scopeOf(skill.Source, skill.Path)}
		groups[k] = append(groups[k], skill.Path)
	}
	values := make([]SourceSnapshot, 0, len(groups)+1)
	for k, paths := range groups {
		sort.Strings(paths)
		values = append(values, SourceSnapshot{Provider: k.provider, Kind: k.kind, Scope: k.scope, Paths: paths, Count: len(paths), Enabled: true, Loaded: true})
	}
	prompts := discoverPromptDefinitions(workspacestate.New(workspaceRoot).PromptRoot())
	if len(prompts) > 0 {
		values = append(values, SourceSnapshot{Provider: instructionsource.NativeSource, Kind: "prompts", Scope: "workspace-native", Paths: prompts, Count: len(prompts), Enabled: true, Loaded: false})
	}
	sortSnapshots(values)
	return values
}

func discoverPromptDefinitions(root string) []string {
	return discoverPromptDefinitionPaths(root)
}

func discoverRegularFiles(root string, maxDepth int, extensions map[string]bool) []string {
	result := make([]string, 0)
	var walk func(string, int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
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
				walk(path, depth+1)
				continue
			}
			if !extensions[strings.ToLower(filepath.Ext(entry.Name()))] {
				continue
			}
			info, err := os.Lstat(path)
			if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				result = append(result, path)
			}
		}
	}
	walk(root, 0)
	sort.Strings(result)
	return result
}

func discoverSkillFiles(root string, maxDepth int) []string {
	result := make([]string, 0)
	var walk func(string, int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			full := filepath.Join(dir, entry.Name())
			found := false
			for _, name := range []string{"SKILL.md", "skill.md"} {
				path := filepath.Join(full, name)
				info, err := os.Lstat(path)
				if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
					result = append(result, path)
					found = true
					break
				}
			}
			if !found {
				walk(full, depth+1)
			}
		}
	}
	walk(root, 0)
	sort.Strings(result)
	return result
}

func sortSnapshots(values []SourceSnapshot) {
	sort.Slice(values, func(i, j int) bool {
		left, right := snapshotSource(values[i]), snapshotSource(values[j])
		if value := instructionsource.Compare(left, right); value != 0 {
			return value < 0
		}
		if values[i].Kind != values[j].Kind {
			return values[i].Kind < values[j].Kind
		}
		return values[i].Provider < values[j].Provider
	})
}

func snapshotSource(value SourceSnapshot) instructionsource.Source {
	class := instructionsource.ClassDynamicProvider
	switch value.Scope {
	case "workspace-native":
		class = instructionsource.ClassWorkspaceNative
	case "global-native":
		class = instructionsource.ClassGlobalNative
	case "user-provider":
		class = instructionsource.ClassDynamicProvider
	case "project-root":
		class = instructionsource.ClassProjectRoot
	case "builtin":
		class = instructionsource.ClassBuiltin
	}
	return instructionsource.Source{Class: class, Provider: value.Provider, Path: firstSourcePath(value.Paths)}
}

func firstSourcePath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func withinSourceRoot(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
