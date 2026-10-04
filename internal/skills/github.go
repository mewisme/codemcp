package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxRepositorySkillDepth = 8
	maxRepositoryEntries    = 20_000
	maxRepositoryCandidates = 256
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
	candidateRoots := make([]string, 0)
	entries := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		entries++
		if entries > maxRepositoryEntries {
			return errors.New("repository skill discovery exceeded entry limit")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("repository skill discovery escaped repository root")
		}
		segments := strings.Split(filepath.ToSlash(relative), "/")
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && len(segments) > maxRepositorySkillDepth {
			return filepath.SkipDir
		}
		if entry.Name() != "SKILL.md" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill manifest is a symlink: %s", filepath.ToSlash(relative))
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("skill manifest is not regular: %s", filepath.ToSlash(relative))
		}
		candidateRoots = append(candidateRoots, filepath.Dir(path))
		if len(candidateRoots) > maxRepositoryCandidates {
			return errors.New("repository skill discovery exceeded candidate limit")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(candidateRoots)
	result := make([]RepositorySkillCandidate, 0, len(candidateRoots))
	for _, candidateRoot := range candidateRoots {
		validated, err := ValidateNativeSkillRoot(candidateRoot)
		if err != nil {
			relative, _ := filepath.Rel(root, candidateRoot)
			return nil, fmt.Errorf("validate repository skill %s: %w", filepath.ToSlash(relative), err)
		}
		relative, err := filepath.Rel(root, candidateRoot)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("repository skill path escaped repository root")
		}
		if relative == "." {
			relative = ""
		}
		result = append(result, RepositorySkillCandidate{
			Skill: validated.Skill, Root: candidateRoot,
			RelativePath: filepath.ToSlash(relative),
			Files:        append([]string(nil), validated.Files...),
		})
	}
	return result, nil
}
