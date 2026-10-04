package skills

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/instructionsource"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func Discover(workspaceRoot string) ([]Skill, error) {
	return DiscoverForWorkspace(workspaceRoot, workspaceRoot)
}

func DiscoverForWorkspace(projectRoot, workspaceRoot string) ([]Skill, error) {
	result := make([]Skill, 0)
	seen := map[string]bool{}
	walkSkills(workspacestate.New(workspaceRoot).SkillsRoot(), instructionsource.NativeSource, 0, &result, seen)
	providers, err := instructionsource.DiscoverDynamicProviders(projectRoot)
	if err != nil {
		return nil, err
	}
	for _, provider := range providers {
		if provider.SkillsDir != "" {
			walkSkills(provider.SkillsDir, provider.Name, 0, &result, seen)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return workspaceSkillLess(result[i], result[j]) })
	return appendReservedBuiltins(result), nil
}

func DiscoverUser(home string, policy instructionpolicy.Config) ([]Skill, error) {
	_ = home
	_ = policy
	result := make([]Skill, 0)
	seen := map[string]bool{}
	walkSkills(filepath.Join(configformat.RootPath(), "skills"), instructionsource.NativeSource, 0, &result, seen)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Path < result[j].Path
		}
		return result[i].Name < result[j].Name
	})
	return appendReservedBuiltins(result), nil
}

func DiscoverWithUser(workspaceRoot, home string, policy instructionpolicy.Config) ([]Skill, error) {
	return DiscoverWithUserForWorkspace(workspaceRoot, workspaceRoot, home, policy)
}

func DiscoverWithUserForWorkspace(projectRoot, workspaceRoot, home string, policy instructionpolicy.Config) ([]Skill, error) {
	_ = policy
	project, err := DiscoverForWorkspace(projectRoot, workspaceRoot)
	if err != nil {
		return nil, err
	}
	user, err := DiscoverUser(home, policy)
	if err != nil {
		return nil, err
	}
	ordered := make([]Skill, 0, len(project)+len(user))
	for _, skill := range project {
		if skill.Source == instructionsource.NativeSource && !IsBuiltin(skill) {
			ordered = append(ordered, skill)
		}
	}
	for _, skill := range user {
		if skill.Source == instructionsource.NativeSource && !IsBuiltin(skill) {
			ordered = append(ordered, skill)
		}
	}
	for _, skill := range project {
		if skill.Source != instructionsource.NativeSource && !IsBuiltin(skill) {
			ordered = append(ordered, skill)
		}
	}
	for _, skill := range user {
		if skill.Source != instructionsource.NativeSource && !IsBuiltin(skill) {
			ordered = append(ordered, skill)
		}
	}
	seen := map[string]bool{}
	result := make([]Skill, 0, len(ordered))
	for _, skill := range ordered {
		if IsReservedName(skill.Name) || seen[skill.Name] {
			continue
		}
		seen[skill.Name] = true
		result = append(result, skill)
	}
	return append(result, BuiltinSkills()...), nil
}

func workspaceSkillLess(left, right Skill) bool {
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
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.Path < right.Path
}

func Load(workspaceRoot, name string, maxBytes int) (Loaded, error) {
	values, err := Discover(workspaceRoot)
	return loadFrom(values, err, name, maxBytes)
}

func LoadWithUser(workspaceRoot, home, name string, maxBytes int, policy instructionpolicy.Config) (Loaded, error) {
	values, err := DiscoverWithUser(workspaceRoot, home, policy)
	return loadFrom(values, err, name, maxBytes)
}

func LoadFromInventory(values []Skill, name string, maxBytes int) (Loaded, error) {
	return loadFrom(values, nil, name, maxBytes)
}

func loadFrom(all []Skill, err error, name string, maxBytes int) (Loaded, error) {
	if strings.TrimSpace(name) == "" {
		return Loaded{}, errors.New("skill name is required")
	}
	if maxBytes <= 0 || maxBytes > 500_000 {
		return Loaded{}, errors.New("max_bytes must be between 1 and 500000")
	}
	if builtin, ok := builtinLoaded(name, maxBytes); ok {
		return builtin, nil
	}
	if err != nil {
		return Loaded{}, err
	}
	for _, skill := range all {
		if skill.Name != name {
			continue
		}
		info, err := os.Lstat(skill.Path)
		if err != nil {
			return Loaded{}, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return Loaded{}, errors.New("skill file is not a regular non-symlink file")
		}
		data, err := os.ReadFile(skill.Path)
		if err != nil {
			return Loaded{}, err
		}
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
		}
		return Loaded{Skill: skill, Content: string(data), Truncated: truncated}, nil
	}
	return Loaded{}, errors.New("unknown project skill: " + name)
}

func walkSkills(dir, source string, depth int, result *[]Skill, seen map[string]bool) {
	if depth > 3 {
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
		skillFile := filepath.Join(full, "SKILL.md")
		info, err := os.Lstat(skillFile)
		if errors.Is(err, os.ErrNotExist) {
			walkSkills(full, source, depth+1, result, seen)
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if seen[skillFile] {
			continue
		}
		validated, err := ValidateNativeSkillManifestDirectory(full)
		if err != nil {
			continue
		}
		seen[skillFile] = true
		skill := validated.Skill
		skill.Description = strings.Join(strings.Fields(skill.Description), " ")
		skill.Source = source
		*result = append(*result, skill)
	}
}
