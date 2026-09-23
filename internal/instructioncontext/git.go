package instructioncontext

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	gitutil "go.mewis.me/codemcp/internal/git"
)

const (
	DefaultGitStatusMaxBytes = 12_000
	DefaultRecentCommitCount = 3
)

type GitSnapshotOptions struct {
	WorkspaceRoots []string
	MaxStatusBytes int
	RecentCommits  int
}

func LoadGitSnapshot(ctx context.Context, cwd string, opts GitSnapshotOptions) GitSnapshot {
	cwd, err := canonicalEnvironmentPath(cwd)
	if err != nil {
		return GitSnapshot{Error: err.Error()}
	}
	roots := cleanPaths(opts.WorkspaceRoots)
	if len(roots) == 0 {
		roots = []string{cwd}
	}
	maxStatusBytes := opts.MaxStatusBytes
	if maxStatusBytes <= 0 {
		maxStatusBytes = DefaultGitStatusMaxBytes
	}
	recentCommitCount := opts.RecentCommits
	if recentCommitCount <= 0 {
		recentCommitCount = DefaultRecentCommitCount
	}

	rootResult, err := gitutil.Run(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitSnapshot{Error: err.Error()}
	}
	if rootResult.ExitCode != 0 || strings.TrimSpace(rootResult.Stdout) == "" {
		return GitSnapshot{}
	}
	repoRoot, err := filepath.Abs(strings.TrimSpace(rootResult.Stdout))
	if err != nil {
		return GitSnapshot{Error: err.Error()}
	}
	repoRoot = filepath.Clean(repoRoot)
	if canonical, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = filepath.Clean(canonical)
	}
	if !withinAnyRoot(roots, repoRoot) {
		return GitSnapshot{Error: "git repository root is outside effective workspace roots"}
	}

	snapshot := GitSnapshot{IsRepo: true, Root: repoRoot}
	branch, err := gitutil.Run(ctx, repoRoot, "branch", "--show-current")
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot
	}
	if branch.ExitCode == 0 {
		snapshot.Branch = strings.TrimSpace(branch.Stdout)
	}

	status, err := gitutil.Run(ctx, repoRoot, "status", "--short", "--branch")
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot
	}
	if status.ExitCode != 0 {
		snapshot.Error = gitResultError("status", status)
		return snapshot
	}
	snapshot.StatusShort, snapshot.StatusTruncated = limitGitText(status.Stdout, maxStatusBytes)

	logResult, err := gitutil.Run(ctx, repoRoot, "log", fmt.Sprintf("-%d", recentCommitCount), "--oneline", "--no-decorate")
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot
	}
	if logResult.ExitCode == 0 && strings.TrimSpace(logResult.Stdout) != "" {
		for _, line := range strings.Split(strings.ReplaceAll(logResult.Stdout, "\r\n", "\n"), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				snapshot.RecentCommits = append(snapshot.RecentCommits, line)
			}
		}
	}
	return snapshot
}

func limitGitText(value string, maxBytes int) (string, bool) {
	limited, truncated := limitInstructionText([]byte(strings.TrimSpace(value)), maxBytes, int(^uint(0)>>1))
	return strings.TrimSpace(limited), truncated
}

func gitResultError(operation string, result gitutil.Result) string {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		detail = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return fmt.Sprintf("git %s: %s", operation, detail)
}
