package skills

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxRepositorySkillDepth         = 8
	defaultRepositoryContainerDepth = 3
	maxRepositoryEntries            = 20_000
	maxRepositoryCandidates         = 256
)

type GitHubSource struct {
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
	Identity   string `json:"identity"`
	CloneURL   string `json:"clone_url"`
}

type RepositorySkillCandidate struct {
	Skill        Skill    `json:"skill"`
	Root         string   `json:"root"`
	RelativePath string   `json:"relative_path"`
	Files        []string `json:"files"`
}

type RepositoryDiscoveryOptions struct {
	FullDepth bool
}

func ParseGitHubIdentity(raw string) (GitHubSource, error) {
	const prefix = "github:"
	if raw == "" || raw != strings.TrimSpace(raw) || !strings.HasPrefix(raw, prefix) {
		return GitHubSource{}, errors.New("github repository identity is invalid")
	}
	parsed, err := ParseGitHubSource(strings.TrimPrefix(raw, prefix))
	if err != nil || parsed.Identity != raw {
		return GitHubSource{}, errors.New("github repository identity is not canonical")
	}
	return parsed, nil
}

func ParseGitHubSource(raw string) (GitHubSource, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return GitHubSource{}, errors.New("github skill source is not canonical")
	}
	owner, repository := "", ""
	switch {
	case strings.HasPrefix(raw, "https://"):
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
			return GitHubSource{}, errors.New("github skill source must use canonical github.com https syntax")
		}
		if strings.HasSuffix(parsed.Path, "/") || strings.Contains(raw, "%") {
			return GitHubSource{}, errors.New("github skill source contains an unsupported path")
		}
		parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return GitHubSource{}, errors.New("github skill source must identify exactly one repository")
		}
		owner, repository = parts[0], parts[1]
		repository = strings.TrimSuffix(repository, ".git")
	case strings.HasPrefix(raw, "git@"):
		const prefix = "git@github.com:"
		if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, ".git") {
			return GitHubSource{}, errors.New("github skill source must use canonical github.com ssh syntax")
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, prefix), ".git"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return GitHubSource{}, errors.New("github skill source must identify exactly one repository")
		}
		owner, repository = parts[0], parts[1]
	default:
		if strings.ContainsAny(raw, ":?#%\\") || strings.HasSuffix(raw, ".git") {
			return GitHubSource{}, errors.New("github skill source must be owner/repository")
		}
		parts := strings.Split(raw, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return GitHubSource{}, errors.New("github skill source must be owner/repository")
		}
		owner, repository = parts[0], parts[1]
	}
	if err := validateGitHubOwner(owner); err != nil {
		return GitHubSource{}, err
	}
	if err := validateGitHubRepository(repository); err != nil {
		return GitHubSource{}, err
	}
	owner = strings.ToLower(owner)
	repository = strings.ToLower(repository)
	return GitHubSource{
		Owner: owner, Repository: repository,
		Identity: "github:" + owner + "/" + repository,
		CloneURL: "https://github.com/" + owner + "/" + repository + ".git",
	}, nil
}

func validateGitHubOwner(value string) error {
	if len(value) == 0 || len(value) > 100 || value[0] == '-' || value[len(value)-1] == '-' {
		return errors.New("github owner is invalid")
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return errors.New("github owner is invalid")
	}
	return nil
}

func validateGitHubRepository(value string) error {
	if len(value) == 0 || len(value) > 255 || value == "." || value == ".." {
		return errors.New("github repository is invalid")
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return errors.New("github repository is invalid")
	}
	return nil
}

func DiscoverRepositorySkills(root string) ([]RepositorySkillCandidate, error) {
	return DiscoverRepositorySkillsWithOptions(root, RepositoryDiscoveryOptions{})
}

func DiscoverRepositorySkillsWithOptions(root string, options RepositoryDiscoveryOptions) ([]RepositorySkillCandidate, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("repository root must be a regular directory")
	}
	state := repositoryDiscoveryState{
		root: root, visited: map[string]bool{}, candidates: map[string]RepositorySkillCandidate{},
	}
	if state.tryCandidate(root) {
		if !options.FullDepth {
			return state.result(), nil
		}
	}

	for _, container := range repositoryPrioritySkillContainers(root) {
		if err := state.walkContainer(container, defaultRepositoryContainerDepth, 0); err != nil {
			return nil, err
		}
	}
	if len(state.candidates) == 0 || options.FullDepth {
		if err := state.walkContainer(root, maxRepositorySkillDepth, 0); err != nil {
			return nil, err
		}
	}
	return state.result(), nil
}

type repositoryDiscoveryState struct {
	root       string
	entries    int
	visited    map[string]bool
	candidates map[string]RepositorySkillCandidate
}

func repositoryPrioritySkillContainers(root string) []string {
	values := []string{
		filepath.Join(root, "skills"),
		filepath.Join(root, "skills", ".curated"),
		filepath.Join(root, "skills", ".experimental"),
		filepath.Join(root, "skills", ".system"),
		filepath.Join(root, "data", "skills"),
		filepath.Join(root, "agent", "skills"),
	}
	entries, err := os.ReadDir(root)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), ".") || entry.Name() == ".git" {
				continue
			}
			values = append(values, filepath.Join(root, entry.Name(), "skills"))
		}
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		clean := filepath.Clean(value)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		result = append(result, clean)
	}
	return result
}

func (s *repositoryDiscoveryState) walkContainer(dir string, maxDepth, depth int) error {
	if depth > maxDepth {
		return nil
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := s.visit(path); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "build" || entry.Name() == "__pycache__" {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		if s.tryCandidate(path) {
			continue
		}
		if depth < maxDepth {
			if err := s.walkContainer(path, maxDepth, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *repositoryDiscoveryState) visit(path string) error {
	clean := filepath.Clean(path)
	if s.visited[clean] {
		return nil
	}
	s.visited[clean] = true
	s.entries++
	if s.entries > maxRepositoryEntries {
		return errors.New("repository skill discovery exceeded entry limit")
	}
	relative, err := filepath.Rel(s.root, clean)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("repository skill discovery escaped repository root")
	}
	return nil
}

func (s *repositoryDiscoveryState) tryCandidate(root string) bool {
	manifestPath := filepath.Join(root, "SKILL.md")
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	var validated ValidatedNativeSkill
	if filepath.Clean(root) == filepath.Clean(s.root) {
		validated, err = validateNativeSkillRoot(root, true)
	} else {
		validated, err = validateManagedNativeSkillDirectory(root)
	}
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(s.root, root)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	if relative == "." {
		relative = ""
	}
	key := filepath.Clean(root)
	if _, exists := s.candidates[key]; exists {
		return true
	}
	if len(s.candidates) >= maxRepositoryCandidates {
		return false
	}
	s.candidates[key] = RepositorySkillCandidate{
		Skill: validated.Skill, Root: root, RelativePath: filepath.ToSlash(relative),
		Files: append([]string(nil), validated.Files...),
	}
	return true
}

func (s *repositoryDiscoveryState) result() []RepositorySkillCandidate {
	result := make([]RepositorySkillCandidate, 0, len(s.candidates))
	for _, candidate := range s.candidates {
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Skill.Name != result[j].Skill.Name {
			return result[i].Skill.Name < result[j].Skill.Name
		}
		return result[i].RelativePath < result[j].RelativePath
	})
	return result
}
