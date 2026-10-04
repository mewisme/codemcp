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
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/instructionsource"
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

type SkillListRequest struct {
	Scope SkillScopeRequest `json:"scope"`
}

type SkillView struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Scope       SkillManagementScope  `json:"scope,omitempty"`
	Source      string                `json:"source"`
	Path        string                `json:"path"`
	Managed     bool                  `json:"managed"`
	ReadOnly    bool                  `json:"read_only"`
	GitHub      *skills.ManagedSource `json:"github,omitempty"`
}

type SkillListResult struct {
	Effective bool        `json:"effective"`
	Skills    []SkillView `json:"skills"`
}

type SkillInfoRequest struct {
	Scope SkillScopeRequest `json:"scope"`
	Name  string            `json:"name"`
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

func NewSkillManagementService(workspaces *workspace.Manager) *SkillManagementService {
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

func (s *SkillManagementService) ResolveWorkspaceForDirectory(directory string) (workspace.Workspace, error) {
	if s == nil || s.Workspaces == nil {
		return workspace.Workspace{}, errors.New("workspace manager is unavailable")
	}
	directory, err := filepath.Abs(strings.TrimSpace(directory))
	if err != nil {
		return workspace.Workspace{}, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return workspace.Workspace{}, err
	}
	items, err := s.Workspaces.List()
	if err != nil {
		return workspace.Workspace{}, err
	}
	var matched workspace.Workspace
	matchedLength := -1
	for _, item := range items {
		root, err := filepath.EvalSymlinks(item.Path)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, directory)
		if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if len(root) > matchedLength {
			matched = item
			matchedLength = len(root)
		}
	}
	if matched.ID == "" {
		return workspace.Workspace{}, errors.New("current directory is not inside a registered workspace")
	}
	return matched, nil
}

func (s *SkillManagementService) Add(ctx context.Context, request SkillAddRequest) (SkillAddResult, error) {
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

func (s *SkillManagementService) List(request SkillListRequest) (SkillListResult, error) {
	if request.Scope.Workspace && request.Scope.Global {
		return SkillListResult{}, errors.New("workspace and global skill scopes are mutually exclusive")
	}
	if request.Scope.Workspace || request.Scope.Global {
		target, err := s.resolveScope(request.Scope)
		if err != nil {
			return SkillListResult{}, err
		}
		values, err := s.nativeSkillsForScope(target)
		if err != nil {
			return SkillListResult{}, err
		}
		views, err := skillViews(values, target.Root, target.Scope, false)
		if err != nil {
			return SkillListResult{}, err
		}
		return SkillListResult{Skills: views}, nil
	}

	if s == nil || s.Workspaces == nil {
		return SkillListResult{}, errors.New("workspace manager is unavailable")
	}
	workspaceID := strings.TrimSpace(request.Scope.WorkspaceID)
	if workspaceID == "" {
		return SkillListResult{}, errors.New("effective skill list requires workspace id")
	}
	item, err := s.Workspaces.Get(workspaceID)
	if err != nil {
		return SkillListResult{}, err
	}
	home, _ := os.UserHomeDir()
	values, err := skills.DiscoverWithUser(item.Path, home, instructionpolicy.DefaultConfig())
	if err != nil {
		return SkillListResult{}, err
	}
	workspaceRoot := workspacestate.New(item.Path).SkillsRoot()
	globalRoot := filepath.Join(configformat.RootPath(), "skills")
	workspaceMetadata, err := skills.ReadManagedSources(workspaceRoot)
	if err != nil {
		return SkillListResult{}, err
	}
	globalMetadata, err := skills.ReadManagedSources(globalRoot)
	if err != nil {
		return SkillListResult{}, err
	}
	views := make([]SkillView, 0, len(values))
	for _, value := range values {
		view := SkillView{Name: value.Name, Description: value.Description, Source: value.Source, Path: value.Path}
		switch {
		case skills.IsBuiltin(value):
			view.Source = "builtin"
			view.ReadOnly = true
		case value.Source == instructionsource.NativeSource && skillPathWithin(value.Path, workspaceRoot):
			view.Scope = SkillScopeWorkspace
			view.Source = "native"
			attachManagedSource(&view, workspaceMetadata)
		case value.Source == instructionsource.NativeSource && skillPathWithin(value.Path, globalRoot):
			view.Scope = SkillScopeGlobal
			view.Source = "native"
			attachManagedSource(&view, globalMetadata)
		default:
			view.Scope = "provider"
			view.ReadOnly = true
		}
		views = append(views, view)
	}
	return SkillListResult{Effective: true, Skills: views}, nil
}

func (s *SkillManagementService) Info(request SkillInfoRequest) (SkillView, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return SkillView{}, errors.New("skill name is required")
	}
	list, err := s.List(SkillListRequest{Scope: request.Scope})
	if err != nil {
		return SkillView{}, err
	}
	for _, view := range list.Skills {
		if view.Name == name {
			return view, nil
		}
	}
	return SkillView{}, fmt.Errorf("skill %q not found", name)
}

func (s *SkillManagementService) nativeSkillsForScope(target SkillScopeResolution) ([]skills.Skill, error) {
	var values []skills.Skill
	var err error
	if target.Scope == SkillScopeGlobal {
		home, _ := os.UserHomeDir()
		values, err = skills.DiscoverUser(home, instructionpolicy.DefaultConfig())
	} else {
		item, getErr := s.Workspaces.Get(target.WorkspaceID)
		if getErr != nil {
			return nil, getErr
		}
		values, err = skills.Discover(item.Path)
	}
	if err != nil {
		return nil, err
	}
	result := make([]skills.Skill, 0, len(values))
	for _, value := range values {
		if value.Source == instructionsource.NativeSource && !skills.IsBuiltin(value) && skillPathWithin(value.Path, target.Root) {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

func skillViews(values []skills.Skill, root string, scope SkillManagementScope, readOnly bool) ([]SkillView, error) {
	metadata, err := skills.ReadManagedSources(root)
	if err != nil {
		return nil, err
	}
	views := make([]SkillView, 0, len(values))
	for _, value := range values {
		view := SkillView{
			Name: value.Name, Description: value.Description, Scope: scope,
			Source: "native", Path: value.Path, ReadOnly: readOnly,
		}
		attachManagedSource(&view, metadata)
		views = append(views, view)
	}
	return views, nil
}

func attachManagedSource(view *SkillView, metadata skills.ManagedSources) {
	if view == nil {
		return
	}
	source, ok := metadata.Skills[view.Name]
	if !ok {
		return
	}
	copy := source
	view.Managed = true
	view.GitHub = &copy
}

func skillPathWithin(path, root string) bool {
	path, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
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
