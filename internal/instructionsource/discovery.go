package instructionsource

import (
	"os"
	"path/filepath"
	"slices"
)

type ProviderRoot struct {
	Name         string
	Path         string
	ContextFiles []string
	RulesDir     string
	SkillsDir    string
}

func DiscoverDynamicProviders(projectRoot string) ([]ProviderRoot, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]ProviderRoot, 0)
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(projectRoot, name)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		candidate := ProviderCandidate{Name: name, Immediate: true, Directory: info.IsDir(), Symlink: info.Mode()&os.ModeSymlink != 0}
		contextFiles := directContextFiles(path)
		rulesDir := directDirectory(filepath.Join(path, "rules"))
		skillsDir := directDirectory(filepath.Join(path, "skills"))
		candidate.HasContext, candidate.HasRules, candidate.HasSkills = len(contextFiles) > 0, rulesDir != "", skillsDir != ""
		if !EligibleDynamicProvider(candidate) {
			continue
		}
		result = append(result, ProviderRoot{Name: name, Path: path, ContextFiles: contextFiles, RulesDir: rulesDir, SkillsDir: skillsDir})
	}
	slices.SortFunc(result, func(left, right ProviderRoot) int {
		return Compare(Source{Class: ClassDynamicProvider, Provider: left.Name, Path: left.Path}, Source{Class: ClassDynamicProvider, Provider: right.Name, Path: right.Path})
	})
	return result, nil
}

func directContextFiles(root string) []string {
	result := make([]string, 0, 2)
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			result = append(result, path)
		}
	}
	return result
}

func directDirectory(path string) string {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	return path
}
