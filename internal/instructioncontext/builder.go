package instructioncontext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/memory"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
)

type BuildOptions struct {
	Root                    string
	WorkspaceID             string
	WorkspaceRoot           string
	CWD                     string
	WorkspaceRoots          []string
	MemoryStore             memory.Store
	Memory                  MemoryLoadOptions
	Policy                  instructionpolicy.Config
	ToolProfile             ToolProfile
	MaxInstructionBytes     int
	MemoryQuery             string
	MaxMemoryEntries        int
	MaxMemoryBytes          int
	SkipGit                 bool
	SkipMemory              bool
	SkipSkills              bool
	IntegrationInstructions []IntegrationInstruction
	IntegrationDiagnostics  []IntegrationDiagnostic
	AdminEnabled            bool
	AdminPort               int
	Now                     func() time.Time
}

func Build(ctx context.Context, opts BuildOptions) (InstructionContext, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workspaceID := strings.TrimSpace(opts.WorkspaceID)
	if workspaceID == "" {
		return InstructionContext{}, errors.New("workspace id is required")
	}
	root, err := canonicalEnvironmentPath(opts.Root)
	if err != nil {
		return InstructionContext{}, err
	}
	workspaceRoot, err := canonicalEnvironmentPath(opts.WorkspaceRoot)
	if err != nil {
		return InstructionContext{}, err
	}
	roots := normalizeEnvironmentRoots(opts.WorkspaceRoots)
	if len(roots) == 0 {
		roots = []string{workspaceRoot}
	}
	if !withinAnyRoot(roots, root) {
		return InstructionContext{}, errors.New("project root is outside effective workspace roots")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	loadedAt := now().UTC()
	home := strings.TrimSpace(opts.Memory.HomeDir)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	environment, err := LoadEnvironmentSnapshot(EnvironmentOptions{
		WorkspaceID: workspaceID, WorkspaceRoot: workspaceRoot, CWD: opts.CWD, EffectiveRoots: roots,
		AdminEnabled: opts.AdminEnabled, AdminPort: opts.AdminPort,
	})
	if err != nil {
		return InstructionContext{}, err
	}
	projectMemory := ProjectMemoryBundle{Root: root, WorkspaceRoots: roots, LoadedAt: loadedAt}
	autoMemory := AutoMemorySnapshot{}
	if !opts.SkipMemory {
		memoryOpts := opts.Memory
		memoryOpts.WorkspaceRoots = roots
		memoryOpts.HomeDir = home
		memoryOpts.SourcePolicy = opts.Policy
		memoryOpts.Now = func() time.Time { return loadedAt }
		projectMemory, err = LoadProjectMemory(root, memoryOpts)
		if err != nil {
			return InstructionContext{}, err
		}
		autoMemory, err = LoadAutoMemorySelected(opts.MemoryStore, workspaceID, opts.MemoryQuery, opts.MaxMemoryEntries, opts.MaxMemoryBytes)
		if err != nil {
			return InstructionContext{}, err
		}
	}
	unconditionalRules, err := LoadUnconditionalRulesWithUserForWorkspace(root, workspaceRoot, home, opts.Policy)
	if err != nil {
		return InstructionContext{}, err
	}
	skillSummaries := []skills.Skill(nil)
	if !opts.SkipSkills {
		skillSummaries, err = LoadSkillSummariesWithUserForWorkspace(root, workspaceRoot, home, opts.Policy)
		if err != nil {
			return InstructionContext{}, err
		}
	}
	gitSnapshot := GitSnapshot{Skipped: opts.SkipGit}
	if !opts.SkipGit {
		gitSnapshot = LoadGitSnapshot(ctx, root, GitSnapshotOptions{WorkspaceRoots: roots})
	}
	globalRules := make([]rules.Rule, 0, len(opts.Policy.Rules))
	for _, rule := range opts.Policy.Rules {
		content := strings.TrimSpace(rule.Content)
		if !rule.Enabled || content == "" {
			continue
		}
		id := strings.TrimSpace(rule.ID)
		if id == "" {
			id = strings.TrimSpace(rule.Name)
		}
		if id == "" {
			id = "rule"
		}
		globalRules = append(globalRules, rules.Rule{Path: filepath.ToSlash("managed://global-rules/" + id), Source: "CodeMCP", Content: content, AlwaysApply: true})
	}
	sources := LoadedProjectSources(projectMemory, unconditionalRules, skillSummaries, workspaceRoot)
	value := InstructionContext{
		Root: root, WorkspaceID: workspaceID, WorkspaceRoots: roots, Environment: environment,
		Git: gitSnapshot, ProjectMemory: projectMemory, AutoMemory: autoMemory, GlobalContext: strings.TrimSpace(opts.Policy.Context), GlobalRules: globalRules,
		Rules: unconditionalRules, Skills: skillSummaries,
		IntegrationInstructions: append([]IntegrationInstruction(nil), opts.IntegrationInstructions...),
		IntegrationDiagnostics:  append([]IntegrationDiagnostic(nil), opts.IntegrationDiagnostics...),
		Sources:                 sources, ToolProfile: opts.ToolProfile,
		AgentWorkflow: AgentWorkflow(), LoadedAt: loadedAt,
	}
	if err := ApplyFormattedInstructionsLimit(&value, opts.MaxInstructionBytes); err != nil {
		return InstructionContext{}, err
	}
	return value, nil
}
