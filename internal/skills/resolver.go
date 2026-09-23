package skills

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

var skillRoots = []struct {
	Relative string
	Source   string
}{
	{filepath.Join(".claude", "skills"), ".claude"},
	{filepath.Join(".claudes", "skills"), ".claudes"},
	{filepath.Join(".agents", "skills"), ".agents"},
	{filepath.Join(".cursor", "skills"), ".cursor"},
	{filepath.Join(".codex", "skills"), ".codex"},
}

func Discover(workspaceRoot string) ([]Skill, error) {
	return discoverAt(workspaceRoot, nil)
}

func DiscoverUser(home string, policy instructionpolicy.Config) ([]Skill, error) {
	return discoverAt(home, func(source string) bool { return policy.Enabled(source, instructionpolicy.ResourceSkills) })
}

func DiscoverWithUser(workspaceRoot, home string, policy instructionpolicy.Config) ([]Skill, error) {
	project, err := Discover(workspaceRoot)
	if err != nil {
		return nil, err
	}
	user, err := DiscoverUser(home, policy)
	if err != nil {
		return nil, err
	}
	result := append([]Skill(nil), project...)
	projectNames := map[string]bool{}
	for _, skill := range project {
		projectNames[skill.Name] = true
	}
	for _, skill := range user {
		if !projectNames[skill.Name] {
			result = append(result, skill)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func discoverAt(rootPath string, enabled func(string) bool) ([]Skill, error) {
	result := make([]Skill, 0)
	seen := map[string]bool{}
	for _, root := range skillRoots {
		if enabled != nil && !enabled(root.Source) {
			continue
		}
		walkSkills(filepath.Join(rootPath, root.Relative), root.Source, 0, &result, seen)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Path < result[j].Path
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func Load(workspaceRoot, name string, maxBytes int) (Loaded, error) {
	values, err := Discover(workspaceRoot)
	return loadFrom(values, err, name, maxBytes)
}

func LoadWithUser(workspaceRoot, home, name string, maxBytes int, policy instructionpolicy.Config) (Loaded, error) {
	values, err := DiscoverWithUser(workspaceRoot, home, policy)
	return loadFrom(values, err, name, maxBytes)
}

func loadFrom(all []Skill, err error, name string, maxBytes int) (Loaded, error) {
	if strings.TrimSpace(name) == "" {
		return Loaded{}, errors.New("skill name is required")
	}
	if maxBytes <= 0 || maxBytes > 500_000 {
		return Loaded{}, errors.New("max_bytes must be between 1 and 500000")
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
