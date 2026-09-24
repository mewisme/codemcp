package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gitcmd "go.mewis.me/codemcp/internal/git"
	"go.mewis.me/codemcp/internal/state"
)

const (
	gitExcludeEntry        = LocalDirName + "/"
	localGitIgnoreFileName = ".gitignore"
	localGitIgnoreRule     = "*"
	GitTrackedGuidance     = "git rm -r --cached .cm"
)

type GitHygieneState string

const (
	GitHygieneHealthy  GitHygieneState = "healthy"
	GitHygieneRepaired GitHygieneState = "repaired"
	GitHygieneSkipped  GitHygieneState = "skipped"
	GitHygieneFailed   GitHygieneState = "failed"
)

type GitHygieneLayer struct {
	State GitHygieneState `json:"state"`
	Path  string          `json:"path,omitempty"`
	Error string          `json:"error,omitempty"`
}

type GitHygieneResult struct {
	NestedIgnore GitHygieneLayer `json:"nested_ignore"`
	GitExclude   GitHygieneLayer `json:"git_exclude"`
}

type GitIndexState string

const (
	GitIndexClean   GitIndexState = "clean"
	GitIndexTracked GitIndexState = "tracked"
	GitIndexSkipped GitIndexState = "skipped"
	GitIndexFailed  GitIndexState = "failed"
)

type GitHygieneDiagnostic struct {
	NestedIgnore GitHygieneState `json:"nested_ignore"`
	GitExclude   GitHygieneState `json:"git_exclude"`
	Index        GitIndexState   `json:"index"`
	Protected    bool            `json:"protected"`
	Degraded     bool            `json:"degraded"`
	Tracked      bool            `json:"tracked"`
	Guidance     string          `json:"guidance,omitempty"`
	Error        string          `json:"error,omitempty"`
}

func (r GitHygieneResult) Protected() bool {
	return hygieneLayerProtects(r.NestedIgnore) || hygieneLayerProtects(r.GitExclude)
}

func (r GitHygieneResult) Error() error {
	if r.Protected() {
		return nil
	}
	var errs []error
	if r.NestedIgnore.State == GitHygieneFailed && strings.TrimSpace(r.NestedIgnore.Error) != "" {
		errs = append(errs, fmt.Errorf("nested .cm/.gitignore: %s", r.NestedIgnore.Error))
	}
	if r.GitExclude.State == GitHygieneFailed && strings.TrimSpace(r.GitExclude.Error) != "" {
		errs = append(errs, fmt.Errorf("git info/exclude: %s", r.GitExclude.Error))
	}
	if len(errs) == 0 {
		return errors.New("workspace git hygiene has no active protection layer")
	}
	return errors.Join(errs...)
}

func hygieneLayerProtects(layer GitHygieneLayer) bool {
	return layer.State == GitHygieneHealthy || layer.State == GitHygieneRepaired
}

func InspectLocalStateGitHygiene(ctx context.Context, workspaceRoot string) GitHygieneDiagnostic {
	root, err := canonicalExistingDirectory(workspaceRoot)
	if err != nil {
		return GitHygieneDiagnostic{NestedIgnore: GitHygieneFailed, GitExclude: GitHygieneFailed, Index: GitIndexSkipped, Degraded: true, Error: "workspace root inspection failed"}
	}
	nested := inspectNestedGitIgnore(root)
	exclude, repository := inspectGitInfoExclude(root)
	result := GitHygieneDiagnostic{NestedIgnore: nested.State, GitExclude: exclude.State}
	result.Protected = hygieneLayerProtects(nested) || hygieneLayerProtects(exclude)
	result.Degraded = nested.State == GitHygieneFailed || exclude.State == GitHygieneFailed
	if nested.Error != "" {
		result.Error = "workspace local .gitignore inspection failed"
	}
	if exclude.Error != "" {
		result.Error = appendGitHygieneDiagnosticError(result.Error, "repository local exclude inspection failed")
	}
	if !repository {
		result.Index = GitIndexSkipped
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tracked, queryErr := trackedLocalState(queryCtx, root)
	if queryErr != nil {
		if strings.Contains(strings.ToLower(queryErr.Error()), "git not found") {
			result.Index = GitIndexSkipped
			return result
		}
		result.Index = GitIndexFailed
		result.Degraded = true
		result.Error = appendGitHygieneDiagnosticError(result.Error, "Git index inspection failed")
		return result
	}
	if tracked {
		result.Index, result.Tracked, result.Guidance = GitIndexTracked, true, GitTrackedGuidance
		return result
	}
	result.Index = GitIndexClean
	return result
}

func inspectNestedGitIgnore(workspaceRoot string) GitHygieneLayer {
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Error: err.Error()}
	}
	defer root.Close()
	content, exists, err := readRootText(root, filepath.Join(LocalDirName, localGitIgnoreFileName))
	if err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Error: err.Error()}
	}
	if !exists {
		return GitHygieneLayer{State: GitHygieneFailed, Error: "workspace local .gitignore is missing"}
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == localGitIgnoreRule {
			return GitHygieneLayer{State: GitHygieneHealthy}
		}
	}
	return GitHygieneLayer{State: GitHygieneFailed, Error: "workspace local .gitignore does not exclude local state"}
}

func inspectGitInfoExclude(workspaceRoot string) (GitHygieneLayer, bool) {
	path, exists, err := gitExcludePath(workspaceRoot)
	if err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Error: err.Error()}, true
	}
	if !exists {
		return GitHygieneLayer{State: GitHygieneSkipped}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Error: err.Error()}, true
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == gitExcludeEntry || line == "/"+gitExcludeEntry {
			return GitHygieneLayer{State: GitHygieneHealthy}, true
		}
	}
	return GitHygieneLayer{State: GitHygieneFailed, Error: "repository local exclude does not contain .cm/"}, true
}

func trackedLocalState(ctx context.Context, workspaceRoot string) (bool, error) {
	result, err := gitcmd.Run(ctx, workspaceRoot, "ls-files", "--", LocalDirName)
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail == "" {
			detail = fmt.Sprintf("git ls-files exited with code %d", result.ExitCode)
		}
		return false, errors.New(detail)
	}
	return result.Stdout != "", nil
}

func appendGitHygieneDiagnosticError(current, next string) string {
	if current == "" {
		return next
	}
	if current == next {
		return current
	}
	return current + "; " + next
}

var gitHygienePathLocks sync.Map

func EnsureLocalStateGitHygiene(workspaceRoot string) GitHygieneResult {
	root, err := canonicalExistingDirectory(workspaceRoot)
	if err != nil {
		message := err.Error()
		return GitHygieneResult{NestedIgnore: GitHygieneLayer{State: GitHygieneFailed, Error: message}, GitExclude: GitHygieneLayer{State: GitHygieneFailed, Error: message}}
	}
	result := GitHygieneResult{}
	result.NestedIgnore = ensureNestedGitIgnore(root)
	result.GitExclude = ensureGitInfoExclude(root)
	return result
}

func EnsureLocalDirExcluded(workspaceRoot string) error {
	result := ensureGitInfoExclude(workspaceRoot)
	if result.State == GitHygieneFailed {
		return errors.New(result.Error)
	}
	return nil
}

func ensureNestedGitIgnore(workspaceRoot string) GitHygieneLayer {
	path := filepath.Join(workspaceRoot, LocalDirName, localGitIgnoreFileName)
	unlock := lockGitHygienePath(path)
	defer unlock()
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: err.Error()}
	}
	defer root.Close()
	rel := filepath.Join(LocalDirName, localGitIgnoreFileName)
	content, exists, err := readRootText(root, rel)
	if err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: err.Error()}
	}
	if exists {
		for _, line := range strings.Split(content, "\n") {
			if strings.TrimSpace(line) == localGitIgnoreRule {
				return GitHygieneLayer{State: GitHygieneHealthy, Path: path}
			}
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += localGitIgnoreRule + "\n"
	if err := state.WriteFileAtomicRoot(root, rel, []byte(content), 0600); err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: err.Error()}
	}
	return GitHygieneLayer{State: GitHygieneRepaired, Path: path}
}

func readRootText(root *os.Root, path string) (string, bool, error) {
	file, err := root.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

func ensureGitInfoExclude(workspaceRoot string) GitHygieneLayer {
	path, exists, err := gitExcludePath(workspaceRoot)
	if err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Error: err.Error()}
	}
	if !exists {
		return GitHygieneLayer{State: GitHygieneSkipped}
	}
	return ensureRuleFile(path, gitExcludeEntry, func(line string) bool {
		line = strings.TrimSpace(line)
		return line == gitExcludeEntry || line == "/"+gitExcludeEntry
	}, 0644)
}

func ensureRuleFile(path, rule string, present func(string) bool, perm os.FileMode) GitHygieneLayer {
	unlock := lockGitHygienePath(path)
	defer unlock()
	parent := filepath.Dir(path)
	if info, err := os.Lstat(parent); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: "Git metadata parent is not a real directory"}
		}
	} else if !os.IsNotExist(err) {
		return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: err.Error()}
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: "Git exclude is not a regular file"}
		}
	} else if !os.IsNotExist(err) {
		return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: err.Error()}
	}
	content := string(data)
	for _, line := range strings.Split(content, "\n") {
		if present(line) {
			return GitHygieneLayer{State: GitHygieneHealthy, Path: path}
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += rule + "\n"
	if err := state.WriteFileAtomic(path, []byte(content), perm); err != nil {
		return GitHygieneLayer{State: GitHygieneFailed, Path: path, Error: err.Error()}
	}
	return GitHygieneLayer{State: GitHygieneRepaired, Path: path}
}

func lockGitHygienePath(path string) func() {
	key := filepath.Clean(path)
	value, _ := gitHygienePathLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func gitExcludePath(workspaceRoot string) (string, bool, error) {
	gitDir, exists, err := resolveGitDir(workspaceRoot)
	if err != nil || !exists {
		return "", exists, err
	}
	metadataRoot, err := resolveCommonGitDir(gitDir)
	if err != nil {
		return "", true, err
	}
	return filepath.Join(metadataRoot, "info", "exclude"), true, nil
}

func resolveGitDir(workspaceRoot string) (string, bool, error) {
	gitPath := filepath.Join(workspaceRoot, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("inspect Git metadata: %w", err)
	}
	if info.IsDir() {
		resolved, err := filepath.EvalSymlinks(gitPath)
		if err != nil {
			return "", true, fmt.Errorf("resolve Git metadata: %w", err)
		}
		return resolved, true, nil
	}
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return "", true, fmt.Errorf("read Git metadata pointer: %w", err)
		}
		target, err := parseGitDirPointer(gitPath, data)
		if err != nil {
			return "", true, err
		}
		if err := validateGitMetadataDir(target); err != nil {
			return "", true, err
		}
		return target, true, nil
	}
	return "", true, errors.New("invalid .git metadata type")
}

func parseGitDirPointer(pointerPath string, data []byte) (string, error) {
	if len(data) == 0 || len(data) > 4096 || strings.ContainsRune(string(data), '\x00') {
		return "", errors.New("invalid .git metadata pointer")
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 2 || (len(lines) == 2 && strings.TrimSpace(lines[1]) != "") {
		return "", errors.New("invalid .git metadata pointer")
	}
	line := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(line, "gitdir:") {
		return "", errors.New("invalid .git metadata pointer")
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if value == "" {
		return "", errors.New("invalid .git metadata pointer")
	}
	target := value
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(pointerPath), target)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", fmt.Errorf("resolve Git metadata pointer: %w", err)
	}
	return resolved, nil
}

func validateGitMetadataDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect Git metadata target: %w", err)
	}
	if !info.IsDir() {
		return errors.New("git metadata target is not a directory")
	}
	for _, marker := range []string{"HEAD", "config", "commondir"} {
		if markerInfo, markerErr := os.Lstat(filepath.Join(path, marker)); markerErr == nil && !markerInfo.IsDir() {
			return nil
		}
	}
	return errors.New("git metadata target is missing expected markers")
}

func resolveCommonGitDir(gitDir string) (string, error) {
	path := filepath.Join(gitDir, "commondir")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return gitDir, nil
		}
		return "", fmt.Errorf("read Git common metadata pointer: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\n") || strings.Contains(value, "\r") {
		return "", errors.New("invalid Git common metadata pointer")
	}
	target := value
	if !filepath.IsAbs(target) {
		target = filepath.Join(gitDir, target)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", fmt.Errorf("resolve Git common metadata pointer: %w", err)
	}
	if err := validateGitMetadataDir(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}
