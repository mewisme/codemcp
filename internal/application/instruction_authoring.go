package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	maxAuthoredRuleBytes       = 4_000
	maxAuthoredRuleGlobs       = 64
	maxAuthoredRuleGlobBytes   = 256
	maxAuthoredSkillBytes      = 500_000
	maxAuthoredSkillFiles      = 32
	maxAuthoredSkillFileBytes  = 256_000
	maxAuthoredSkillTotalBytes = 1_000_000
	maxExistingArtifactBytes   = 1_000_000
)

var instructionArtifactNamePattern = regexp.MustCompile("^[a-z0-9][a-z0-9-]{0,63}$")

type InstructionAuthoringScope string

const (
	InstructionScopeWorkspace InstructionAuthoringScope = "workspace"
	InstructionScopeGlobal    InstructionAuthoringScope = "global"
)

type InstructionAuthoringMode string

const (
	InstructionCreate InstructionAuthoringMode = "create"
	InstructionUpdate InstructionAuthoringMode = "update"
)

type InstructionArtifactKind string

const (
	InstructionArtifactRule  InstructionArtifactKind = "rule"
	InstructionArtifactSkill InstructionArtifactKind = "skill"
)

type RuleAuthoringRequest struct {
	Scope       InstructionAuthoringScope `json:"scope"`
	Mode        InstructionAuthoringMode  `json:"mode"`
	WorkspaceID string                    `json:"workspace_id,omitempty"`
	Name        string                    `json:"name"`
	AlwaysApply bool                      `json:"always_apply"`
	Globs       []string                  `json:"globs,omitempty"`
	Content     string                    `json:"content"`
	DryRun      bool                      `json:"dry_run,omitempty"`
}

type SkillSupportingFile struct {
	Path       string `json:"path"`
	Content    []byte `json:"content"`
	Executable bool   `json:"executable,omitempty"`
}

type SkillAuthoringRequest struct {
	Scope           InstructionAuthoringScope `json:"scope"`
	Mode            InstructionAuthoringMode  `json:"mode"`
	WorkspaceID     string                    `json:"workspace_id,omitempty"`
	Name            string                    `json:"name"`
	Description     string                    `json:"description"`
	Instructions    string                    `json:"instructions"`
	SupportingFiles []SkillSupportingFile     `json:"supporting_files,omitempty"`
	DryRun          bool                      `json:"dry_run,omitempty"`
}

type InstructionAuthoringResult struct {
	Kind      InstructionArtifactKind   `json:"kind"`
	Scope     InstructionAuthoringScope `json:"scope"`
	Mode      InstructionAuthoringMode  `json:"mode"`
	Name      string                    `json:"name"`
	Path      string                    `json:"path"`
	ContentID string                    `json:"content_id"`
	DryRun    bool                      `json:"dry_run"`
}

type InstructionAuthoringService struct {
	Workspaces      *workspace.Manager
	GlobalAuthorize func(context.Context) bool
	Changes         *instructioncontext.ChangeStream

	mu             sync.Mutex
	activationHook func(point string) error
}

type authoredFile struct {
	path string
	data []byte
	perm fs.FileMode
}

type authoringTarget struct {
	basePath      string
	resourceDir   string
	targetRel     string
	workspaceID   string
	workspaceRoot string
}

func NewInstructionAuthoringService(workspaces *workspace.Manager, globalAuthorize func(context.Context) bool, streams ...*instructioncontext.ChangeStream) *InstructionAuthoringService {
	var changes *instructioncontext.ChangeStream
	if len(streams) > 0 {
		changes = streams[0]
	}
	return &InstructionAuthoringService{Workspaces: workspaces, GlobalAuthorize: globalAuthorize, Changes: changes}
}

func (s *InstructionAuthoringService) WriteRule(ctx context.Context, request RuleAuthoringRequest) (InstructionAuthoringResult, error) {
	if s == nil {
		return InstructionAuthoringResult{}, errors.New("instruction authoring service is unavailable")
	}
	name, err := validateArtifactName(request.Name)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := validateAuthoringMode(request.Mode); err != nil {
		return InstructionAuthoringResult{}, err
	}
	content, globs, err := validateRuleRequest(request)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	rendered := renderRule(content, request.AlwaysApply, globs)
	target, err := s.resolveTarget(ctx, request.Scope, request.WorkspaceID, "rules", name+".md")
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	result := InstructionAuthoringResult{
		Kind: InstructionArtifactRule, Scope: request.Scope, Mode: request.Mode, Name: name,
		Path: filepath.Join(target.basePath, target.targetRel), ContentID: contentID(rendered), DryRun: request.DryRun,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.revalidateTarget(target); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := inspectResourceRoot(target.basePath, target.resourceDir); err != nil {
		return InstructionAuthoringResult{}, err
	}
	currentID, exists, err := snapshotRegularFile(result.Path)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := validateModeAgainstExistence(request.Mode, exists); err != nil {
		return InstructionAuthoringResult{}, fmt.Errorf("%s %q: %w", request.Mode, name, err)
	}
	if request.DryRun {
		return s.completeInstructionMutation(result, target), nil
	}

	root, err := openStableDirectory(target.basePath, request.Scope == InstructionScopeGlobal)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	defer root.Close()
	if err := ensureStableSubdirectory(root, target.resourceDir); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if request.Mode == InstructionCreate {
		stageRel, err := stageRootFile(root, target.resourceDir, name, rendered, 0o600)
		if err != nil {
			return InstructionAuthoringResult{}, err
		}
		defer root.Remove(stageRel)
		if err := s.callActivationHook("before-activate"); err != nil {
			return InstructionAuthoringResult{}, err
		}
		if err := s.revalidateTarget(target); err != nil {
			return InstructionAuthoringResult{}, err
		}
		if err := ensureStableSubdirectory(root, target.resourceDir); err != nil {
			return InstructionAuthoringResult{}, err
		}
		if _, err := root.Lstat(target.targetRel); err == nil {
			return InstructionAuthoringResult{}, fmt.Errorf("create %q: target already exists", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return InstructionAuthoringResult{}, err
		}
		if err := root.Link(stageRel, target.targetRel); err != nil {
			return InstructionAuthoringResult{}, fmt.Errorf("create rule %q: %w", name, err)
		}
		if err := root.Remove(stageRel); err != nil {
			return InstructionAuthoringResult{}, fmt.Errorf("remove staged rule %q: %w", name, err)
		}
		return s.completeInstructionMutation(result, target), nil
	}

	stageRel, err := stageRootFile(root, target.resourceDir, name, rendered, 0o600)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	defer root.Remove(stageRel)
	if err := s.callActivationHook("before-activate"); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := s.revalidateTarget(target); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := ensureStableSubdirectory(root, target.resourceDir); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := s.activateUpdate(root, target, currentID, stageRel, false); err != nil {
		return InstructionAuthoringResult{}, err
	}
	return s.completeInstructionMutation(result, target), nil
}

func (s *InstructionAuthoringService) WriteSkill(ctx context.Context, request SkillAuthoringRequest) (InstructionAuthoringResult, error) {
	if s == nil {
		return InstructionAuthoringResult{}, errors.New("instruction authoring service is unavailable")
	}
	name, err := validateArtifactName(request.Name)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	if skills.IsReservedName(name) {
		return InstructionAuthoringResult{}, fmt.Errorf("skill name %q is reserved by CodeMCP", name)
	}
	if err := validateAuthoringMode(request.Mode); err != nil {
		return InstructionAuthoringResult{}, err
	}
	files, desiredID, err := validateAndRenderSkill(request, name)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	target, err := s.resolveTarget(ctx, request.Scope, request.WorkspaceID, "skills", name)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	result := InstructionAuthoringResult{
		Kind: InstructionArtifactSkill, Scope: request.Scope, Mode: request.Mode, Name: name,
		Path: filepath.Join(target.basePath, target.targetRel), ContentID: desiredID, DryRun: request.DryRun,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.revalidateTarget(target); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := inspectResourceRoot(target.basePath, target.resourceDir); err != nil {
		return InstructionAuthoringResult{}, err
	}
	currentID, exists, err := snapshotSkillTree(result.Path)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := validateModeAgainstExistence(request.Mode, exists); err != nil {
		return InstructionAuthoringResult{}, fmt.Errorf("%s %q: %w", request.Mode, name, err)
	}
	if request.DryRun {
		return s.completeInstructionMutation(result, target), nil
	}

	root, err := openStableDirectory(target.basePath, request.Scope == InstructionScopeGlobal)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	defer root.Close()
	if err := ensureStableSubdirectory(root, target.resourceDir); err != nil {
		return InstructionAuthoringResult{}, err
	}
	stageRel, err := stageSkillTree(root, target.resourceDir, name, files)
	if err != nil {
		return InstructionAuthoringResult{}, err
	}
	defer root.RemoveAll(stageRel)
	if err := s.callActivationHook("before-activate"); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := s.revalidateTarget(target); err != nil {
		return InstructionAuthoringResult{}, err
	}
	if err := ensureStableSubdirectory(root, target.resourceDir); err != nil {
		return InstructionAuthoringResult{}, err
	}

	if request.Mode == InstructionCreate {
		if _, err := root.Lstat(target.targetRel); err == nil {
			return InstructionAuthoringResult{}, fmt.Errorf("create %q: target already exists", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return InstructionAuthoringResult{}, err
		}
		if err := root.Rename(stageRel, target.targetRel); err != nil {
			return InstructionAuthoringResult{}, fmt.Errorf("activate skill %q: %w", name, err)
		}
		return s.completeInstructionMutation(result, target), nil
	}
	if err := s.activateUpdate(root, target, currentID, stageRel, true); err != nil {
		return InstructionAuthoringResult{}, err
	}
	return s.completeInstructionMutation(result, target), nil
}

func (s *InstructionAuthoringService) completeInstructionMutation(result InstructionAuthoringResult, target authoringTarget) InstructionAuthoringResult {
	if s != nil && s.Changes != nil && !result.DryRun {
		s.Changes.Publish(instructioncontext.Change{
			Kind: string(result.Kind), Scope: string(result.Scope), WorkspaceID: target.workspaceID,
			Name: result.Name, Operation: string(result.Mode),
		})
	}
	return result
}

func (s *InstructionAuthoringService) resolveTarget(ctx context.Context, scope InstructionAuthoringScope, workspaceID, resourceDir, targetName string) (authoringTarget, error) {
	switch scope {
	case InstructionScopeWorkspace:
		if s.Workspaces == nil {
			return authoringTarget{}, errors.New("workspace manager is unavailable")
		}
		workspaceID = strings.TrimSpace(workspaceID)
		if workspaceID == "" {
			return authoringTarget{}, errors.New("workspace id is required for workspace instruction authoring")
		}
		item, err := s.Workspaces.Get(workspaceID)
		if err != nil {
			return authoringTarget{}, err
		}
		store, err := s.Workspaces.LocalState(item.ID)
		if err != nil {
			return authoringTarget{}, err
		}
		identity, err := store.LoadIdentity()
		if err != nil {
			return authoringTarget{}, fmt.Errorf("verify workspace local state: %w", err)
		}
		if identity.ID != item.ID {
			return authoringTarget{}, fmt.Errorf("workspace local state identity mismatch: local %s, registered %s", identity.ID, item.ID)
		}
		return authoringTarget{
			basePath: store.Root(), resourceDir: resourceDir, targetRel: filepath.Join(resourceDir, targetName),
			workspaceID: item.ID, workspaceRoot: item.Path,
		}, nil
	case InstructionScopeGlobal:
		if s.GlobalAuthorize == nil || !s.GlobalAuthorize(ctx) {
			return authoringTarget{}, errors.New("global instruction authoring requires operator authorization")
		}
		if strings.TrimSpace(workspaceID) != "" {
			return authoringTarget{}, errors.New("workspace id is not valid for global instruction authoring")
		}
		return authoringTarget{
			basePath: configformat.RootPath(), resourceDir: resourceDir, targetRel: filepath.Join(resourceDir, targetName),
		}, nil
	default:
		return authoringTarget{}, fmt.Errorf("unsupported instruction authoring scope %q", scope)
	}
}

func (s *InstructionAuthoringService) revalidateTarget(target authoringTarget) error {
	if target.workspaceID == "" {
		return nil
	}
	item, err := s.Workspaces.Get(target.workspaceID)
	if err != nil {
		return err
	}
	if filepath.Clean(item.Path) != filepath.Clean(target.workspaceRoot) {
		return errors.New("workspace root changed during instruction authoring")
	}
	store := workspacestate.New(item.Path)
	identity, err := store.LoadIdentity()
	if err != nil {
		return fmt.Errorf("revalidate workspace local state: %w", err)
	}
	if identity.ID != target.workspaceID || filepath.Clean(store.Root()) != filepath.Clean(target.basePath) {
		return errors.New("workspace local state changed during instruction authoring")
	}
	return nil
}

func (s *InstructionAuthoringService) activateUpdate(root *os.Root, target authoringTarget, expectedID, stageRel string, tree bool) error {
	backupRel, err := uniqueArtifactPath(target.resourceDir, filepath.Base(target.targetRel), "backup")
	if err != nil {
		return err
	}
	if err := root.Rename(target.targetRel, backupRel); err != nil {
		return fmt.Errorf("capture current instruction artifact: %w", err)
	}
	restore := func(cause error) error {
		if restoreErr := root.Rename(backupRel, target.targetRel); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restore previous instruction artifact: %w", restoreErr))
		}
		return cause
	}

	backupPath := filepath.Join(target.basePath, backupRel)
	var capturedID string
	if tree {
		capturedID, _, err = snapshotSkillTree(backupPath)
	} else {
		capturedID, _, err = snapshotRegularFile(backupPath)
	}
	if err != nil {
		return restore(err)
	}
	if capturedID != expectedID {
		return restore(errors.New("instruction artifact changed before update activation"))
	}
	if err := s.callActivationHook("after-backup"); err != nil {
		return restore(err)
	}
	if err := s.revalidateTarget(target); err != nil {
		return restore(err)
	}
	if err := root.Rename(stageRel, target.targetRel); err != nil {
		return restore(fmt.Errorf("activate instruction update: %w", err))
	}
	if err := root.RemoveAll(backupRel); err != nil {
		return fmt.Errorf("remove previous instruction artifact backup: %w", err)
	}
	return nil
}

func (s *InstructionAuthoringService) callActivationHook(point string) error {
	if s.activationHook == nil {
		return nil
	}
	return s.activationHook(point)
}

func validateAuthoringMode(mode InstructionAuthoringMode) error {
	if mode != InstructionCreate && mode != InstructionUpdate {
		return fmt.Errorf("unsupported instruction authoring mode %q", mode)
	}
	return nil
}

func validateModeAgainstExistence(mode InstructionAuthoringMode, exists bool) error {
	switch {
	case mode == InstructionCreate && exists:
		return errors.New("target already exists")
	case mode == InstructionUpdate && !exists:
		return errors.New("target does not exist")
	default:
		return nil
	}
}

func validateArtifactName(name string) (string, error) {
	if name != strings.TrimSpace(name) || !instructionArtifactNamePattern.MatchString(name) {
		return "", errors.New("instruction artifact name must be 1-64 lowercase letters, digits, or hyphens and start with a letter or digit")
	}
	return name, nil
}

func validateRuleRequest(request RuleAuthoringRequest) (string, []string, error) {
	content := strings.TrimSpace(strings.ReplaceAll(request.Content, "\r\n", "\n"))
	if content == "" {
		return "", nil, errors.New("rule content is required")
	}
	if len([]byte(content)) > maxAuthoredRuleBytes {
		return "", nil, fmt.Errorf("rule content exceeds %d bytes", maxAuthoredRuleBytes)
	}
	if request.AlwaysApply && len(request.Globs) != 0 {
		return "", nil, errors.New("always-on rule cannot define scoped globs")
	}
	if !request.AlwaysApply && len(request.Globs) == 0 {
		return "", nil, errors.New("scoped rule requires at least one glob")
	}
	if len(request.Globs) > maxAuthoredRuleGlobs {
		return "", nil, fmt.Errorf("rule defines more than %d globs", maxAuthoredRuleGlobs)
	}
	globs := make([]string, 0, len(request.Globs))
	seen := map[string]bool{}
	for _, raw := range request.Globs {
		value := strings.TrimSpace(raw)
		if value == "" || strings.ContainsAny(value, "\x00\\") || len([]byte(value)) > maxAuthoredRuleGlobBytes {
			return "", nil, fmt.Errorf("invalid rule glob %q", raw)
		}
		if seen[value] {
			return "", nil, fmt.Errorf("duplicate rule glob %q", value)
		}
		seen[value] = true
		globs = append(globs, value)
	}
	return content, globs, nil
}

func renderRule(content string, alwaysApply bool, globs []string) []byte {
	var builder strings.Builder
	builder.WriteString("---\n")
	if alwaysApply {
		builder.WriteString("always_apply: true\n")
	} else {
		builder.WriteString("globs:\n")
		for _, glob := range globs {
			builder.WriteString("  - ")
			builder.WriteString(strconv.Quote(glob))
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("---\n")
	builder.WriteString(content)
	builder.WriteByte('\n')
	return []byte(builder.String())
}

func validateAndRenderSkill(request SkillAuthoringRequest, name string) ([]authoredFile, string, error) {
	description := strings.TrimSpace(strings.ReplaceAll(request.Description, "\r\n", "\n"))
	if description == "" || strings.Contains(description, "\n") {
		return nil, "", errors.New("skill description must be one non-empty line")
	}
	if len([]byte(description)) > 200 {
		return nil, "", errors.New("skill description exceeds 200 bytes")
	}
	instructions := strings.TrimSpace(strings.ReplaceAll(request.Instructions, "\r\n", "\n"))
	if instructions == "" {
		return nil, "", errors.New("skill instructions are required")
	}
	if len([]byte(instructions)) > maxAuthoredSkillBytes {
		return nil, "", fmt.Errorf("skill instructions exceed %d bytes", maxAuthoredSkillBytes)
	}
	if len(request.SupportingFiles) > maxAuthoredSkillFiles {
		return nil, "", fmt.Errorf("skill defines more than %d supporting files", maxAuthoredSkillFiles)
	}

	main := []byte("---\nname: " + strconv.Quote(name) + "\ndescription: " + strconv.Quote(description) + "\n---\n" + instructions + "\n")
	files := []authoredFile{{path: "SKILL.md", data: main, perm: 0o600}}
	total := len(main)
	seen := map[string]bool{"skill.md": true}
	for _, support := range request.SupportingFiles {
		clean, err := validateSupportingPath(support.Path)
		if err != nil {
			return nil, "", err
		}
		key := strings.ToLower(clean)
		if seen[key] {
			return nil, "", fmt.Errorf("duplicate or reserved skill file path %q", support.Path)
		}
		seen[key] = true
		if len(support.Content) > maxAuthoredSkillFileBytes {
			return nil, "", fmt.Errorf("skill file %q exceeds %d bytes", clean, maxAuthoredSkillFileBytes)
		}
		total += len(support.Content)
		if total > maxAuthoredSkillTotalBytes {
			return nil, "", fmt.Errorf("skill tree exceeds %d bytes", maxAuthoredSkillTotalBytes)
		}
		perm := fs.FileMode(0o600)
		if support.Executable {
			perm = 0o700
		}
		files = append(files, authoredFile{path: filepath.FromSlash(clean), data: append([]byte(nil), support.Content...), perm: perm})
	}
	sort.Slice(files[1:], func(i, j int) bool { return files[1+i].path < files[1+j].path })
	return files, authoredFilesContentID(files), nil
}

func validateSupportingPath(value string) (string, error) {
	if value != strings.TrimSpace(value) || value == "" || strings.ContainsAny(value, "\x00\\") {
		return "", fmt.Errorf("invalid skill supporting path %q", value)
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || isPortableVolumePath(value) {
		return "", fmt.Errorf("skill supporting path must be relative: %q", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("skill supporting path escapes skill root: %q", value)
	}
	return clean, nil
}

func isPortableVolumePath(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

func authoredFilesContentID(files []authoredFile) string {
	hash := sha256.New()
	for _, file := range files {
		_, _ = hash.Write([]byte(filepath.ToSlash(file.path)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(fmt.Sprintf("%04o", file.perm.Perm())))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(file.data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func contentID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func inspectResourceRoot(basePath, resourceDir string) error {
	info, err := os.Lstat(basePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("instruction authoring root is not a stable directory: %s", basePath)
	}
	resourcePath := filepath.Join(basePath, resourceDir)
	info, err = os.Lstat(resourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("instruction %s root must be a real directory", resourceDir)
	}
	return nil
}

func openStableDirectory(path string, create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("instruction authoring root is not a stable directory: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("instruction authoring root changed while opening: %s", path)
	}
	return root, nil
}

func ensureStableSubdirectory(root *os.Root, relative string) error {
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(relative, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err = root.Lstat(relative)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("instruction %s root must be a real directory", relative)
	}
	opened, err := root.OpenRoot(relative)
	if err != nil {
		return err
	}
	defer opened.Close()
	current, err := opened.Stat(".")
	if err != nil || !current.IsDir() || !os.SameFile(info, current) {
		if err != nil {
			return err
		}
		return fmt.Errorf("instruction %s root changed while opening", relative)
	}
	return nil
}

func snapshotRegularFile(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("instruction rule target must be a regular non-symlink file: %s", path)
	}
	if info.Size() > maxExistingArtifactBytes {
		return "", false, fmt.Errorf("instruction rule target exceeds %d bytes", maxExistingArtifactBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(fmt.Sprintf("%04o", info.Mode().Perm())))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil)), true, nil
}

func snapshotSkillTree(root string) (string, bool, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("instruction skill target must be a real directory: %s", root)
	}
	hash := sha256.New()
	files := 0
	total := 0
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("instruction skill tree contains symlink: %s", current)
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("instruction skill tree contains non-regular file: %s", current)
		}
		files++
		if files > maxAuthoredSkillFiles+1 {
			return fmt.Errorf("instruction skill tree contains more than %d files", maxAuthoredSkillFiles+1)
		}
		if info.Size() > maxExistingArtifactBytes {
			return fmt.Errorf("instruction skill file exceeds %d bytes: %s", maxExistingArtifactBytes, current)
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		total += len(data)
		if total > maxExistingArtifactBytes {
			return fmt.Errorf("instruction skill tree exceeds %d bytes", maxExistingArtifactBytes)
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(fmt.Sprintf("%04o", info.Mode().Perm())))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return hex.EncodeToString(hash.Sum(nil)), true, nil
}

func writeRootFileExclusive(root *os.Root, relative string, data []byte, perm fs.FileMode) error {
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = root.Remove(relative)
		}
	}()
	if err := file.Chmod(perm); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func stageRootFile(root *os.Root, parent, name string, data []byte, perm fs.FileMode) (string, error) {
	stage, err := uniqueArtifactPath(parent, name, "stage")
	if err != nil {
		return "", err
	}
	if err := writeRootFileExclusive(root, stage, data, perm); err != nil {
		return "", err
	}
	return stage, nil
}

func stageSkillTree(root *os.Root, parent, name string, files []authoredFile) (string, error) {
	stage, err := uniqueArtifactPath(parent, name, "stage")
	if err != nil {
		return "", err
	}
	if err := root.Mkdir(stage, 0o700); err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = root.RemoveAll(stage)
		}
	}()
	for _, file := range files {
		relative := filepath.Join(stage, file.path)
		dir := filepath.Dir(relative)
		if dir != stage {
			if err := root.MkdirAll(dir, 0o700); err != nil {
				return "", err
			}
		}
		if err := writeRootFileExclusive(root, relative, file.data, file.perm); err != nil {
			return "", err
		}
	}
	ok = true
	return stage, nil
}

func uniqueArtifactPath(parent, base, suffix string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return filepath.Join(parent, "."+base+"-"+suffix+"-"+hex.EncodeToString(random[:])), nil
}
