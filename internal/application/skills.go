package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	gitpkg "go.mewis.me/codemcp/internal/git"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type SkillManagementScope string

const (
	SkillScopeWorkspace SkillManagementScope = "workspace"
	SkillScopeGlobal    SkillManagementScope = "global"
)

type SkillScopeRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Workspace   bool   `json:"workspace,omitempty"`
	Global      bool   `json:"global,omitempty"`
}

type SkillScopeResolution struct {
	Scope       SkillManagementScope `json:"scope"`
	WorkspaceID string               `json:"workspace_id,omitempty"`
	Root        string               `json:"root"`
}

type SkillAddRequest struct {
	Scope  SkillScopeRequest `json:"scope"`
	Source string            `json:"source"`
	Skill  string            `json:"skill,omitempty"`
	All    bool              `json:"all,omitempty"`
}

type SkillAddResult struct {
	Scope       SkillManagementScope `json:"scope"`
	WorkspaceID string               `json:"workspace_id,omitempty"`
	Root        string               `json:"root"`
	Source      skills.GitHubSource  `json:"source"`
	Revision    string               `json:"revision"`
	Skills      []skills.Skill       `json:"skills"`
}

type acquiredSkillRepository struct {
	Root     string
	Revision string
	Cleanup  func()
}

type SkillManagementService struct {
	Workspaces           *workspace.Manager
	AcquireRepository    func(context.Context, skills.GitHubSource) (acquiredSkillRepository, error)
	BeforeMetadataCommit func() error
}

func newSkillManagementService(workspaces *workspace.Manager) *SkillManagementService {
	return &SkillManagementService{Workspaces: workspaces, AcquireRepository: acquireGitHubSkillRepository}
}

func (s *SkillManagementService) resolveScope(request SkillScopeRequest) (SkillScopeResolution, error) {
	if request.Workspace && request.Global {
		return SkillScopeResolution{}, errors.New("workspace and global skill scopes are mutually exclusive")
	}
	if request.Global {
		return SkillScopeResolution{
			Scope: SkillScopeGlobal,
			Root:  filepath.Join(configformat.RootPath(), "skills"),
		}, nil
	}
	if s == nil || s.Workspaces == nil {
		return SkillScopeResolution{}, errors.New("workspace manager is unavailable")
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	if workspaceID == "" {
		return SkillScopeResolution{}, errors.New("workspace skill scope requires workspace id")
	}
	item, err := s.Workspaces.Get(workspaceID)
	if err != nil {
		return SkillScopeResolution{}, err
	}
	return SkillScopeResolution{
		Scope:       SkillScopeWorkspace,
		WorkspaceID: item.ID,
		Root:        workspacestate.New(item.Path).SkillsRoot(),
	}, nil
}

func (s *SkillManagementService) add(ctx context.Context, request SkillAddRequest) (SkillAddResult, error) {
	target, err := s.resolveScope(request.Scope)
	if err != nil {
		return SkillAddResult{}, err
	}
	if request.All && strings.TrimSpace(request.Skill) != "" {
		return SkillAddResult{}, errors.New("--skill and --all are mutually exclusive")
	}
	source, err := skills.ParseGitHubSource(request.Source)
	if err != nil {
		return SkillAddResult{}, err
	}
	selector := strings.TrimSpace(request.Skill)
	if selector != "" {
		if _, err := skills.ValidateNativeSkillName(selector); err != nil {
			return SkillAddResult{}, fmt.Errorf("invalid skill selector: %w", err)
		}
	}
	if s == nil || s.AcquireRepository == nil {
		return SkillAddResult{}, errors.New("github skill repository acquisition is unavailable")
	}
	acquired, err := s.AcquireRepository(ctx, source)
	if err != nil {
		return SkillAddResult{}, err
	}
	if acquired.Cleanup != nil {
		defer acquired.Cleanup()
	}
	candidates, err := skills.DiscoverRepositorySkills(acquired.Root)
	if err != nil {
		return SkillAddResult{}, err
	}
	selected, err := selectSkillCandidates(candidates, selector, request.All)
	if err != nil {
		return SkillAddResult{}, err
	}
	metadata, err := skills.ReadManagedSources(target.Root)
	if err != nil {
		return SkillAddResult{}, err
	}
	if err := rejectSkillInstallConflicts(target.Root, metadata, selected); err != nil {
		return SkillAddResult{}, err
	}

	staged, err := skills.StageManagedSkills(target.Root, selected)
	if err != nil {
		return SkillAddResult{}, err
	}
	defer skills.CleanupStagedManagedSkills(target.Root, staged)

	nextMetadata := skills.CloneManagedSources(metadata)
	for _, candidate := range selected {
		nextMetadata.Skills[candidate.Skill.Name] = skills.ManagedSource{
			Source: source.Identity, Revision: acquired.Revision, Path: candidate.RelativePath,
		}
	}
	if err := skills.CommitStagedManagedSkills(target.Root, staged, nextMetadata, s.BeforeMetadataCommit); err != nil {
		return SkillAddResult{}, err
	}

	installed := make([]skills.Skill, 0, len(selected))
	for _, candidate := range selected {
		installed = append(installed, skills.Skill{
			Name: candidate.Skill.Name, Description: candidate.Skill.Description,
			Path: filepath.Join(target.Root, candidate.Skill.Name, "SKILL.md"), Source: ".cm",
		})
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Name < installed[j].Name })
	return SkillAddResult{
		Scope: target.Scope, WorkspaceID: target.WorkspaceID, Root: target.Root,
		Source: source, Revision: acquired.Revision, Skills: installed,
	}, nil
}

func acquireGitHubSkillRepository(ctx context.Context, source skills.GitHubSource) (acquiredSkillRepository, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	temp, err := os.MkdirTemp("", "cm-github-skill-*")
	if err != nil {
		return acquiredSkillRepository{}, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	repo := filepath.Join(temp, "repo")
	if _, err := gitpkg.OrThrow(ctx, temp, "clone", "--quiet", "--depth=1", source.CloneURL, repo); err != nil {
		cleanup()
		return acquiredSkillRepository{}, fmt.Errorf("clone github skill repository: %w", err)
	}
	revisionResult, err := gitpkg.OrThrow(ctx, repo, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		cleanup()
		return acquiredSkillRepository{}, fmt.Errorf("resolve github skill revision: %w", err)
	}
	revision := strings.ToLower(strings.TrimSpace(revisionResult.Stdout))
	if err := skills.ValidateGitRevision(revision); err != nil {
		cleanup()
		return acquiredSkillRepository{}, fmt.Errorf("validate github skill revision: %w", err)
	}
	return acquiredSkillRepository{Root: repo, Revision: revision, Cleanup: cleanup}, nil
}

func selectSkillCandidates(candidates []skills.RepositorySkillCandidate, selector string, all bool) ([]skills.RepositorySkillCandidate, error) {
	if len(candidates) == 0 {
		return nil, errors.New("github repository contains no valid SKILL.md candidates")
	}
	byName := map[string][]skills.RepositorySkillCandidate{}
	for _, candidate := range candidates {
		byName[candidate.Skill.Name] = append(byName[candidate.Skill.Name], candidate)
	}
	for name, values := range byName {
		if len(values) > 1 {
			return nil, fmt.Errorf("github repository contains duplicate skill name %q", name)
		}
	}
	if selector != "" {
		values := byName[selector]
		if len(values) != 1 {
			return nil, fmt.Errorf("github repository does not contain skill %q", selector)
		}
		return []skills.RepositorySkillCandidate{values[0]}, nil
	}
	if all {
		result := append([]skills.RepositorySkillCandidate(nil), candidates...)
		sort.Slice(result, func(i, j int) bool { return result[i].Skill.Name < result[j].Skill.Name })
		return result, nil
	}
	if len(candidates) == 1 {
		return []skills.RepositorySkillCandidate{candidates[0]}, nil
	}
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Skill.Name)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("github repository contains multiple skills (%s); use --skill <name> or --all", strings.Join(names, ", "))
}

func rejectSkillInstallConflicts(root string, metadata skills.ManagedSources, candidates []skills.RepositorySkillCandidate) error {
	for _, candidate := range candidates {
		name := candidate.Skill.Name
		path := filepath.Join(root, name)
		if _, err := os.Lstat(path); err == nil {
			if skills.IsManaged(metadata, name) {
				return fmt.Errorf("managed skill %q already exists", name)
			}
			return fmt.Errorf("unmanaged skill %q already exists", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
