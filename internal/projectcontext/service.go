package projectcontext

import (
	"context"
	"errors"
	"os"
	"strings"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	"go.mewis.me/codemcp/internal/memory"
	plandoc "go.mewis.me/codemcp/internal/plan"
	"go.mewis.me/codemcp/internal/workspace"
)

const (
	MinMemoryEntries     = 1
	MaxMemoryEntries     = 100
	DefaultMemoryEntries = 12
	MinMemoryBytes       = 256
	MaxMemoryBytes       = 100_000
	DefaultMemoryBytes   = 8192
	MinInstructionBytes  = 1
	MaxInstructionBytes  = 1_000_000
	MinSectionBytes      = 1
	MaxSectionBytes      = 500_000
	MinLinesPerSection   = 1
	MaxLinesPerSection   = 5_000
)

type MemoryFile struct {
	Path      string                         `json:"path"`
	Kind      instructioncontext.SectionKind `json:"kind"`
	Source    string                         `json:"source,omitempty"`
	Truncated bool                           `json:"truncated"`
}

type GitSummary struct {
	Skipped bool   `json:"skipped,omitempty"`
	IsRepo  bool   `json:"is_repo"`
	Branch  string `json:"branch,omitempty"`
	Commits int    `json:"commits"`
}

type Summary struct {
	MemoryFiles      []MemoryFile              `json:"memory_files"`
	MemoryBytes      int                       `json:"memory_bytes"`
	InstructionBytes int                       `json:"instruction_bytes"`
	Git              GitSummary                `json:"git"`
	Rules            int                       `json:"rules"`
	Skills           int                       `json:"skills"`
	Plans            PlanContext               `json:"plans"`
	PlanExecution    *plandoc.ExecutionBinding `json:"plan_execution,omitempty"`
	Semantic         SemanticSummary           `json:"semantic,omitempty"`
}

type SemanticSummary struct {
	Used         bool   `json:"used"`
	Fallback     bool   `json:"fallback,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	Candidates   int    `json:"candidates,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
}

type Result struct {
	Root               string                                `json:"root"`
	WorkspaceID        string                                `json:"workspace_id"`
	InstructionContext instructioncontext.InstructionContext `json:"instruction_context"`
	Summary            Summary                               `json:"summary"`
}

type Options struct {
	Path                string
	MaxInstructionBytes int
	MaxSectionBytes     int
	MaxLinesPerSection  int
	MemoryQuery         string
	MaxMemoryEntries    int
	MaxMemoryBytes      int
	IncludeGit          bool
	IncludeMemory       bool
	IncludeSkills       bool
	PlanName            string
	AdminEnabled        bool
	AdminPort           int
	BackgroundWork      instructioncontext.BackgroundWorkCapabilities
}

func DefaultOptions() Options {
	return Options{
		MaxInstructionBytes: instructioncontext.DefaultInstructionMaxBytes,
		MaxSectionBytes:     instructioncontext.DefaultSectionMaxBytes,
		MaxLinesPerSection:  instructioncontext.DefaultSectionMaxLines,
		MaxMemoryEntries:    DefaultMemoryEntries,
		MaxMemoryBytes:      DefaultMemoryBytes,
		IncludeGit:          true,
		IncludeMemory:       true,
		IncludeSkills:       true,
	}
}

type Service struct {
	Workspaces                     *workspace.Manager
	MemoryStore                    memory.Store
	ToolProfile                    func() instructioncontext.ToolProfile
	Environment                    func() (bool, int)
	IntegrationProviders           []IntegrationInstructionProvider
	IntegrationProjectionProviders []IntegrationProjectionProvider
	Semantic                       semantic.Provider
}

type IntegrationInstructionProvider func(context.Context, string, string) ([]instructioncontext.IntegrationInstruction, error)

type IntegrationProjection struct {
	Instructions []instructioncontext.IntegrationInstruction
	Diagnostics  []instructioncontext.IntegrationDiagnostic
}

type IntegrationProjectionProvider func(context.Context, string, string) (IntegrationProjection, error)

type ServiceOptions struct {
	Workspaces                     *workspace.Manager
	MemoryStore                    *memory.Store
	ToolProfile                    func() instructioncontext.ToolProfile
	Environment                    func() (bool, int)
	IntegrationProviders           []IntegrationInstructionProvider
	IntegrationProjectionProviders []IntegrationProjectionProvider
	Semantic                       semantic.Provider
}

func NewService(options ServiceOptions) *Service {
	service := &Service{
		Workspaces:                     options.Workspaces,
		ToolProfile:                    options.ToolProfile,
		Environment:                    options.Environment,
		IntegrationProviders:           append([]IntegrationInstructionProvider(nil), options.IntegrationProviders...),
		IntegrationProjectionProviders: append([]IntegrationProjectionProvider(nil), options.IntegrationProjectionProviders...),
		Semantic:                       options.Semantic,
	}
	if options.MemoryStore != nil {
		service.MemoryStore = *options.MemoryStore
	} else {
		service.MemoryStore = memory.NewWorkspaceStore(memory.DefaultRoot(), options.Workspaces)
	}
	return service
}

func New(workspaces *workspace.Manager, toolProfile func() instructioncontext.ToolProfile) *Service {
	return NewService(ServiceOptions{Workspaces: workspaces, ToolProfile: toolProfile})
}

func (s *Service) Build(ctx context.Context, workspaceID string, opts Options) (Result, error) {
	if s == nil || s.Workspaces == nil {
		return Result{}, errors.New("workspace manager is unavailable")
	}
	item, err := s.Workspaces.Get(workspaceID)
	if err != nil {
		return Result{}, err
	}
	plans, err := loadWorkspacePlans(item.Path, opts.PlanName)
	if err != nil {
		return Result{}, err
	}
	root := item.Path
	if path := strings.TrimSpace(opts.Path); path != "" {
		root, err = s.Workspaces.ResolvePath(item.ID, item.Path, path, true)
		if err != nil {
			return Result{}, err
		}
		info, err := os.Stat(root)
		if err != nil {
			return Result{}, err
		}
		if !info.IsDir() {
			return Result{}, errors.New("project context path must be a directory")
		}
	}
	roots, err := s.Workspaces.EffectiveRoots(item.ID)
	if err != nil {
		return Result{}, err
	}
	profile := instructioncontext.ToolProfile{}
	if s.ToolProfile != nil {
		profile = s.ToolProfile()
	}
	adminEnabled, adminPort := opts.AdminEnabled, opts.AdminPort
	if !adminEnabled && adminPort == 0 && s.Environment != nil {
		adminEnabled, adminPort = s.Environment()
	}
	integrationInstructions := make([]instructioncontext.IntegrationInstruction, 0)
	seenIntegrationIDs := map[string]bool{}
	for _, provider := range s.IntegrationProviders {
		if provider == nil {
			continue
		}
		values, err := provider(ctx, item.ID, root)
		if err != nil {
			return Result{}, err
		}
		for _, value := range values {
			id := strings.TrimSpace(value.ID)
			if id != "" {
				if seenIntegrationIDs[id] {
					return Result{}, errors.New("duplicate project context integration instruction id: " + id)
				}
				seenIntegrationIDs[id] = true
			}
			if strings.TrimSpace(value.Content) != "" {
				integrationInstructions = append(integrationInstructions, value)
			}
		}
	}
	integrationDiagnostics := make([]instructioncontext.IntegrationDiagnostic, 0)
	seenDiagnosticIDs := map[string]bool{}
	for _, provider := range s.IntegrationProjectionProviders {
		if provider == nil {
			continue
		}
		projection, err := provider(ctx, item.ID, root)
		if err != nil {
			return Result{}, err
		}
		for _, value := range projection.Instructions {
			id := strings.TrimSpace(value.ID)
			if id != "" {
				if seenIntegrationIDs[id] {
					return Result{}, errors.New("duplicate project context integration instruction id: " + id)
				}
				seenIntegrationIDs[id] = true
			}
			if strings.TrimSpace(value.Content) != "" {
				integrationInstructions = append(integrationInstructions, value)
			}
		}
		for _, value := range projection.Diagnostics {
			id := strings.TrimSpace(value.ID)
			if id != "" {
				if seenDiagnosticIDs[id] {
					return Result{}, errors.New("duplicate project context integration diagnostic id: " + id)
				}
				seenDiagnosticIDs[id] = true
			}
			if strings.TrimSpace(value.Message) != "" {
				integrationDiagnostics = append(integrationDiagnostics, value)
			}
		}
	}
	value, err := instructioncontext.Build(ctx, instructioncontext.BuildOptions{
		Root: root, WorkspaceID: item.ID, WorkspaceRoot: item.Path, CWD: item.Path, WorkspaceRoots: roots, MemoryStore: s.MemoryStore,
		Memory:      instructioncontext.MemoryLoadOptions{ImportMaxDepth: instructioncontext.DefaultImportMaxDepth, MaxBytesPerSection: opts.MaxSectionBytes, MaxLinesPerSection: opts.MaxLinesPerSection},
		ToolProfile: profile, MaxInstructionBytes: opts.MaxInstructionBytes,
		BackgroundWork: opts.BackgroundWork,
		MemoryQuery:    opts.MemoryQuery, MaxMemoryEntries: opts.MaxMemoryEntries, MaxMemoryBytes: opts.MaxMemoryBytes,
		SkipGit: !opts.IncludeGit, SkipMemory: !opts.IncludeMemory, SkipSkills: !opts.IncludeSkills,
		IntegrationInstructions: integrationInstructions,
		IntegrationDiagnostics:  integrationDiagnostics,
		AdminEnabled:            adminEnabled, AdminPort: adminPort,
	})
	if err != nil {
		return Result{}, err
	}
	semanticSummary := SemanticSummary{}
	if s.Semantic != nil && strings.TrimSpace(opts.MemoryQuery) != "" {
		priority, summary := rankOptionalContext(ctx, s.Semantic, opts.MemoryQuery, value)
		semanticSummary = summary
		if len(priority) > 0 {
			if err := instructioncontext.ApplyFormattedInstructionsLimitWithPriority(&value, opts.MaxInstructionBytes, priority); err != nil {
				return Result{}, err
			}
		}
	}
	result := FromInstructionContext(value)
	result.Summary.Plans = plans
	result.Summary.Semantic = semanticSummary
	return result, nil
}

func FromInstructionContext(value instructioncontext.InstructionContext) Result {
	files := make([]MemoryFile, 0, len(value.ProjectMemory.Sections)+len(value.ProjectMemory.Imports))
	appendSection := func(section instructioncontext.Section) {
		files = append(files, MemoryFile{Path: section.Path, Kind: section.Kind, Source: section.Source, Truncated: section.Truncated})
	}
	for _, section := range value.ProjectMemory.Sections {
		appendSection(section)
	}
	for _, section := range value.ProjectMemory.Imports {
		appendSection(section)
	}
	return Result{
		Root: value.Root, WorkspaceID: value.WorkspaceID, InstructionContext: value,
		Summary: Summary{
			MemoryFiles: files, MemoryBytes: value.ProjectMemory.TotalBytes, InstructionBytes: value.InstructionBytes,
			Git:   GitSummary{Skipped: value.Git.Skipped, IsRepo: value.Git.IsRepo, Branch: value.Git.Branch, Commits: len(value.Git.RecentCommits)},
			Rules: len(value.Rules), Skills: len(value.Skills),
		},
	}
}
