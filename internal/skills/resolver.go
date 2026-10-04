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
		skillFile := ""
		for _, candidate := range []string{"SKILL.md", "skill.md"} {
			path := filepath.Join(full, candidate)
			info, err := os.Lstat(path)
			if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				skillFile = path
				break
			}
		}
		if skillFile == "" {
			walkSkills(full, source, depth+1, result, seen)
			continue
		}
		if seen[skillFile] {
			continue
		}
		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		name, description := parseFrontmatter(string(data))
		if name == "" {
			name = entry.Name()
		}
		if description == "" {
			description = firstDescriptionLine(string(data))
		}
		if description == "" {
			description = name
		}
		if len(description) > 200 {
			description = description[:200]
		}
		seen[skillFile] = true
		*result = append(*result, Skill{Name: name, Description: description, Path: skillFile, Source: source})
	}
}

func parseFrontmatter(content string) (string, string) {
	if !strings.HasPrefix(content, "---") {
		return "", ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	name := ""
	description := ""
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			name = trimYAMLScalar(value)
		case "description":
			description = trimYAMLScalar(value)
		}
	}
	return name, description
}

func firstDescriptionLine(content string) string {
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		value := strings.TrimSpace(line)
		if value == "" || strings.HasPrefix(value, "#") || value == "---" || strings.Contains(value, ":") {
			continue
		}
		return value
	}
	return ""
}

func trimYAMLScalar(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'`)
}
