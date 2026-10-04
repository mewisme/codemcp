package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	gitexec "go.mewis.me/codemcp/internal/git"
	"go.mewis.me/codemcp/internal/workspace"
)

const gitTimeout = 60 * time.Second

type GitStatusResult struct {
	Path   string `json:"path"`
	Output string `json:"output"`
}

type GitDiffResult struct {
	Path   string  `json:"path"`
	Staged bool    `json:"staged"`
	File   *string `json:"file"`
	Output string  `json:"output"`
}

type GitLogResult struct {
	Path    string   `json:"path"`
	Count   int      `json:"count"`
	Commits []string `json:"commits"`
}

type GitAddResult struct {
	Path   string   `json:"path"`
	Files  []string `json:"files"`
	Output string   `json:"output"`
}

type GitCommitResult struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Output  string `json:"output"`
}

type GitBranchResult struct {
	Path   string  `json:"path"`
	Action string  `json:"action"`
	Name   *string `json:"name"`
	Output string  `json:"output"`
}

type GitCheckoutResult struct {
	Path               string `json:"path"`
	Branch             string `json:"branch"`
	Output             string `json:"output"`
	RunCommandFallback string `json:"run_command_fallback"`
}

type GitRestoreResult struct {
	Path               string   `json:"path"`
	Files              []string `json:"files"`
	Source             string   `json:"source"`
	Output             string   `json:"output"`
	RunCommandFallback string   `json:"run_command_fallback"`
}

type GitPushResult struct {
	Path               string  `json:"path"`
	Remote             string  `json:"remote"`
	Branch             *string `json:"branch"`
	Force              bool    `json:"force"`
	Output             string  `json:"output"`
	RunCommandFallback string  `json:"run_command_fallback"`
}

type GitPullResult struct {
	Path   string  `json:"path"`
	Remote string  `json:"remote"`
	Branch *string `json:"branch"`
	Output string  `json:"output"`
}

type GitStashResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Output string `json:"output"`
}

type GitResetResult struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	Ref    string `json:"ref"`
	Output string `json:"output"`
}

type gitLocation struct {
	Workspace workspace.Workspace
	CWD       string
	RepoRoot  string
}

func RegisterGitTools(registry *Registry, workspaces *workspace.Manager) {
	registerAnnotatedWithApproval := func(name, title, description, input, output string, annotations map[string]any, approval bool, handler Handler) {
		schema := Schema{
			Name: name, Title: title, Description: description,
			InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: annotations, Capability: toolCapability(CapabilityDomainGit),
		}
		if approval {
			schema.Approval = inlineApprovalMetadata()
		}
		registry.MustRegister(name, schema, handler)
	}
	registerAnnotated := func(name, title, description, input, output string, annotations map[string]any, handler Handler) {
		registerAnnotatedWithApproval(name, title, description, input, output, annotations, false, handler)
	}
	registerAnnotatedApproval := func(name, title, description, input, output string, annotations map[string]any, handler Handler) {
		registerAnnotatedWithApproval(name, title, description, input, output, annotations, true, handler)
	}
	register := func(name, title, description, input, output string, risk Risk, handler Handler) {
		registerAnnotated(name, title, description, input, output, ToolAnnotations(risk), handler)
	}
	registerApproval := func(name, title, description, input, output string, risk Risk, handler Handler) {
		registerAnnotatedApproval(name, title, description, input, output, ToolAnnotations(risk), handler)
	}

	register("git_status", "Git Status", "Show git working tree status.", gitLocationSchema(``), `{"type":"object","properties":{"path":{"type":"string"},"output":{"type":"string"}},"required":["path","output"],"additionalProperties":false}`, RiskRead, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		result, err := runGit(ctx, location.CWD, "status", "--short", "--branch")
		if err != nil {
			return Result{}, err
		}
		output := result.Stdout
		if output == "" {
			output = "Clean working tree"
		}
		return JSONResult(GitStatusResult{Path: location.CWD, Output: output}), nil
	})

	register("git_diff", "Git Diff", "Show unstaged or staged changes.", gitLocationSchema(`"staged":{"type":"boolean","default":false},"file":{"type":"string"},`), `{"type":"object","properties":{"path":{"type":"string"},"staged":{"type":"boolean"},"file":{"type":["string","null"]},"output":{"type":"string"}},"required":["path","staged","file","output"],"additionalProperties":false}`, RiskRead, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		staged, err := optionalBool(args, "staged", false)
		if err != nil {
			return Result{}, err
		}
		file, err := optionalString(args, "file")
		if err != nil {
			return Result{}, err
		}
		gitArgs := []string{"diff"}
		if staged {
			gitArgs = append(gitArgs, "--staged")
		}
		var filePtr *string
		if strings.TrimSpace(file) != "" {
			pathspec, err := gitPathspec(workspaces, location, file, false)
			if err != nil {
				return Result{}, err
			}
			gitArgs = append(gitArgs, "--", pathspec)
			filePtr = &file
		}
		result, err := runGit(ctx, location.CWD, gitArgs...)
		if err != nil {
			return Result{}, err
		}
		output := result.Stdout
		if output == "" {
			output = "No changes"
		}
		return JSONResult(GitDiffResult{Path: location.CWD, Staged: staged, File: filePtr, Output: output}), nil
	})

	register("git_log", "Git Log", "Show recent commit history.", gitLocationSchema(`"count":{"type":"integer","minimum":1,"maximum":10000,"default":10},`), `{"type":"object","properties":{"path":{"type":"string"},"count":{"type":"integer"},"commits":{"type":"array","items":{"type":"string"}}},"required":["path","count","commits"],"additionalProperties":false}`, RiskRead, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		count, err := optionalInt(args, "count", 10, 1, 10000)
		if err != nil {
			return Result{}, err
		}
		result, err := runGit(ctx, location.CWD, "log", "--oneline", "-n", fmt.Sprintf("%d", count))
		if err != nil {
			return Result{}, err
		}
		commits := []string{}
		if result.Stdout != "" {
			for _, line := range strings.Split(result.Stdout, "\n") {
				if strings.TrimSpace(line) != "" {
					commits = append(commits, line)
				}
			}
		}
		return JSONResult(GitLogResult{Path: location.CWD, Count: count, Commits: commits}), nil
	})

	register("git_add", "Git Add", "Stage files for commit.", gitLocationSchema(`"files":{"type":"array","items":{"type":"string"}},"all":{"type":"boolean","default":true},`), `{"type":"object","properties":{"path":{"type":"string"},"files":{"type":"array","items":{"type":"string"}},"output":{"type":"string"}},"required":["path","files","output"],"additionalProperties":false}`, RiskEdit, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		files, err := optionalStrings(args, "files")
		if err != nil {
			return Result{}, err
		}
		all, err := optionalBool(args, "all", true)
		if err != nil {
			return Result{}, err
		}
		gitArgs := []string{"add"}
		outputFiles := []string{"-A"}
		if all && len(files) == 0 {
			gitArgs = append(gitArgs, "-A")
		} else if len(files) > 0 {
			pathspecs, err := gitPathspecs(workspaces, location, files, false)
			if err != nil {
				return Result{}, err
			}
			gitArgs = append(gitArgs, "--")
			gitArgs = append(gitArgs, pathspecs...)
			outputFiles = files
		} else {
			return Result{}, errors.New("files is required when all=false")
		}
		result, err := runGit(ctx, location.CWD, gitArgs...)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(GitAddResult{Path: location.CWD, Files: outputFiles, Output: result.Stdout}), nil
	})

	register("git_commit", "Git Commit", "Create a commit; stages all first unless stage_all=false.", gitLocationSchema(`"message":{"type":"string"},"stage_all":{"type":"boolean","default":true},`), `{"type":"object","properties":{"path":{"type":"string"},"message":{"type":"string"},"output":{"type":"string"}},"required":["path","message","output"],"additionalProperties":false}`, RiskEdit, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		message, err := requiredString(args, "message")
		if err != nil {
			return Result{}, err
		}
		stageAll, err := optionalBool(args, "stage_all", true)
		if err != nil {
			return Result{}, err
		}
		if stageAll {
			if _, err := runGit(ctx, location.CWD, "add", "-A"); err != nil {
				return Result{}, err
			}
		}
		result, err := runGit(ctx, location.CWD, "commit", "-m", message)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(GitCommitResult{Path: location.CWD, Message: message, Output: result.Stdout}), nil
	})

	register("git_branch", "Git Branch", "List/create/switch branches.", gitLocationSchema(`"action":{"type":"string","enum":["list","create","switch","create-and-switch"],"default":"list"},"name":{"type":"string"},`), `{"type":"object","properties":{"path":{"type":"string"},"action":{"type":"string"},"name":{"type":["string","null"]},"output":{"type":"string"}},"required":["path","action","name","output"],"additionalProperties":false}`, RiskEdit, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		action, err := optionalEnum(args, "action", "list", "list", "create", "switch", "create-and-switch")
		if err != nil {
			return Result{}, err
		}
		name, err := optionalString(args, "name")
		if err != nil {
			return Result{}, err
		}
		var namePtr *string
		var gitArgs []string
		if action == "list" {
			gitArgs = []string{"branch", "--all"}
		} else {
			if strings.TrimSpace(name) == "" {
				return Result{}, errors.New("name is required")
			}
			if err := validateBranchName(ctx, location.CWD, name); err != nil {
				return Result{}, err
			}
			namePtr = &name
			switch action {
			case "create":
				gitArgs = []string{"branch", name}
			case "switch":
				gitArgs = []string{"switch", name}
			case "create-and-switch":
				gitArgs = []string{"switch", "-c", name}
			}
		}
		result, err := runGit(ctx, location.CWD, gitArgs...)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(GitBranchResult{Path: location.CWD, Action: action, Name: namePtr, Output: result.Stdout}), nil
	})

	register("git_checkout", "Switch Git Branch", "Switch the current local repository to an existing branch.", gitLocationSchema(`"branch":{"type":"string"},`), `{"type":"object","properties":{"path":{"type":"string"},"branch":{"type":"string"},"output":{"type":"string"},"run_command_fallback":{"type":"string"}},"required":["path","branch","output","run_command_fallback"],"additionalProperties":false}`, RiskEdit, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		branch, err := requiredString(args, "branch")
		if err != nil {
			return Result{}, err
		}
		if err := validateBranchName(ctx, location.CWD, branch); err != nil {
			return Result{}, err
		}
		result, err := runGit(ctx, location.CWD, "switch", branch)
		if err != nil {
			return Result{}, err
		}
		output := result.Stdout
		if output == "" {
			output = result.Stderr
		}
		return JSONResult(GitCheckoutResult{
			Path: location.CWD, Branch: branch, Output: output,
			RunCommandFallback: "git switch " + quoteFallback(branch),
		}), nil
	})

	registerApproval("git_restore", "Restore Tracked Files", "Restore tracked files from a revision. Local workspace only.", gitLocationSchema(`"files":{"type":"array","items":{"type":"string"},"minItems":1},"source":{"type":"string","default":"HEAD"},`), `{"type":"object","properties":{"path":{"type":"string"},"files":{"type":"array","items":{"type":"string"}},"source":{"type":"string"},"output":{"type":"string"},"run_command_fallback":{"type":"string"}},"required":["path","files","source","output","run_command_fallback"],"additionalProperties":false}`, RiskDestructive, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		files, err := requiredStrings(args, "files")
		if err != nil {
			return Result{}, err
		}
		source, err := optionalStringDefault(args, "source", "HEAD")
		if err != nil {
			return Result{}, err
		}
		if err := validateGitScalar("source", source); err != nil {
			return Result{}, err
		}
		pathspecs, err := gitPathspecs(workspaces, location, files, false)
		if err != nil {
			return Result{}, err
		}
		restoreArgs := []string{"restore", "--source", source, "--"}
		restoreArgs = append(restoreArgs, pathspecs...)
		result, runErr := runGitRaw(ctx, location.CWD, restoreArgs...)
		if runErr != nil {
			return Result{}, runErr
		}
		if result.ExitCode != 0 {
			checkoutArgs := []string{"checkout", source, "--"}
			checkoutArgs = append(checkoutArgs, pathspecs...)
			result, err = runGit(ctx, location.CWD, checkoutArgs...)
			if err != nil {
				return Result{}, err
			}
		}
		output := result.Stdout
		if output == "" {
			output = result.Stderr
		}
		if output == "" {
			output = "Restored"
		}
		return JSONResult(GitRestoreResult{
			Path: location.CWD, Files: files, Source: source, Output: output,
			RunCommandFallback: "git restore --source " + quoteFallback(source) + " -- " + quoteFallbackList(files),
		}), nil
	})

	registerAnnotatedApproval("git_push", "Sync Commits to Remote", "Upload local commits to the configured remote. Normal pushes run without local approval; force push requires destructive approval.", gitLocationSchema(`"remote":{"type":"string","default":"origin"},"branch":{"type":"string"},"set_upstream":{"type":"boolean","default":false},"force":{"type":"boolean","default":false},`), `{"type":"object","properties":{"path":{"type":"string"},"remote":{"type":"string"},"branch":{"type":["string","null"]},"force":{"type":"boolean"},"output":{"type":"string"},"run_command_fallback":{"type":"string"}},"required":["path","remote","branch","force","output","run_command_fallback"],"additionalProperties":false}`, ToolAnnotationsOpenWorld(RiskDestructive), func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		remote, err := optionalStringDefault(args, "remote", "origin")
		if err != nil {
			return Result{}, err
		}
		if err := validateGitScalar("remote", remote); err != nil {
			return Result{}, err
		}
		branch, err := optionalString(args, "branch")
		if err != nil {
			return Result{}, err
		}
		var branchPtr *string
		if strings.TrimSpace(branch) != "" {
			if err := validateGitScalar("branch", branch); err != nil {
				return Result{}, err
			}
			branchPtr = &branch
		}
		setUpstream, err := optionalBool(args, "set_upstream", false)
		if err != nil {
			return Result{}, err
		}
		force, err := optionalBool(args, "force", false)
		if err != nil {
			return Result{}, err
		}
		gitArgs := []string{"push"}
		if force {
			gitArgs = append(gitArgs, "--force")
		}
		if setUpstream {
			gitArgs = append(gitArgs, "-u")
		}
		gitArgs = append(gitArgs, remote)
		if branchPtr != nil {
			gitArgs = append(gitArgs, branch)
		}
		result, err := runGit(ctx, location.CWD, gitArgs...)
		if err != nil {
			return Result{}, err
		}
		output := result.Stdout
		if output == "" {
			output = result.Stderr
		}
		fallback := []string{"git push"}
		if force {
			fallback = append(fallback, "--force")
		}
		if setUpstream {
			fallback = append(fallback, "-u")
		}
		fallback = append(fallback, quoteFallback(remote))
		if branchPtr != nil {
			fallback = append(fallback, quoteFallback(branch))
		}
		return JSONResult(GitPushResult{
			Path: location.CWD, Remote: remote, Branch: branchPtr, Force: force, Output: output,
			RunCommandFallback: strings.Join(fallback, " "),
		}), nil
	})

	registerAnnotated("git_pull", "Sync from Remote", "Download updates from the configured remote into the local working copy.", gitLocationSchema(`"remote":{"type":"string","default":"origin"},"branch":{"type":"string"},`), `{"type":"object","properties":{"path":{"type":"string"},"remote":{"type":"string"},"branch":{"type":["string","null"]},"output":{"type":"string"}},"required":["path","remote","branch","output"],"additionalProperties":false}`, ToolAnnotationsOpenWorld(RiskEdit), func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		remote, err := optionalStringDefault(args, "remote", "origin")
		if err != nil {
			return Result{}, err
		}
		if err := validateGitScalar("remote", remote); err != nil {
			return Result{}, err
		}
		branch, err := optionalString(args, "branch")
		if err != nil {
			return Result{}, err
		}
		var branchPtr *string
		gitArgs := []string{"pull", remote}
		if strings.TrimSpace(branch) != "" {
			if err := validateGitScalar("branch", branch); err != nil {
				return Result{}, err
			}
			branchPtr = &branch
			gitArgs = append(gitArgs, branch)
		}
		result, err := runGit(ctx, location.CWD, gitArgs...)
		if err != nil {
			return Result{}, err
		}
		output := result.Stdout
		if output == "" {
			output = result.Stderr
		}
		return JSONResult(GitPullResult{Path: location.CWD, Remote: remote, Branch: branchPtr, Output: output}), nil
	})

	register("git_stash", "Git Stash", "Stash list/push/pop/apply.", gitLocationSchema(`"action":{"type":"string","enum":["list","push","pop","apply"],"default":"list"},"message":{"type":"string"},`), `{"type":"object","properties":{"path":{"type":"string"},"action":{"type":"string"},"output":{"type":"string"}},"required":["path","action","output"],"additionalProperties":false}`, RiskEdit, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		action, err := optionalEnum(args, "action", "list", "list", "push", "pop", "apply")
		if err != nil {
			return Result{}, err
		}
		message, err := optionalString(args, "message")
		if err != nil {
			return Result{}, err
		}
		gitArgs := []string{"stash"}
		if action == "list" {
			gitArgs = append(gitArgs, "list")
		} else if action == "push" {
			gitArgs = append(gitArgs, "push")
			if strings.TrimSpace(message) != "" {
				gitArgs = append(gitArgs, "-m", message)
			}
		} else {
			gitArgs = append(gitArgs, action)
		}
		result, err := runGit(ctx, location.CWD, gitArgs...)
		if err != nil {
			return Result{}, err
		}
		output := result.Stdout
		if output == "" {
			output = result.Stderr
		}
		return JSONResult(GitStashResult{Path: location.CWD, Action: action, Output: output}), nil
	})

	registerApproval("git_reset", "Git Reset", "Move HEAD to a ref. mixed=unstage, soft=keep staged, hard=discard working changes.", gitLocationSchema(`"mode":{"type":"string","enum":["soft","mixed","hard"],"default":"mixed"},"ref":{"type":"string","default":"HEAD"},`), `{"type":"object","properties":{"path":{"type":"string"},"mode":{"type":"string"},"ref":{"type":"string"},"output":{"type":"string"}},"required":["path","mode","ref","output"],"additionalProperties":false}`, RiskDestructive, func(ctx context.Context, args map[string]any) (Result, error) {
		location, err := resolveGitLocation(ctx, workspaces, args)
		if err != nil {
			return Result{}, err
		}
		mode, err := optionalEnum(args, "mode", "mixed", "soft", "mixed", "hard")
		if err != nil {
			return Result{}, err
		}
		ref, err := optionalStringDefault(args, "ref", "HEAD")
		if err != nil {
			return Result{}, err
		}
		if err := validateGitScalar("ref", ref); err != nil {
			return Result{}, err
		}
		result, err := runGit(ctx, location.CWD, "reset", "--"+mode, ref)
		if err != nil {
			return Result{}, err
		}
		output := result.Stdout
		if output == "" {
			output = result.Stderr
		}
		return JSONResult(GitResetResult{Path: location.CWD, Mode: mode, Ref: ref, Output: output}), nil
	})
}

func resolveGitLocation(ctx context.Context, workspaces *workspace.Manager, args map[string]any) (gitLocation, error) {
	workspaceID, err := requiredString(args, "workspace_id")
	if err != nil {
		return gitLocation{}, err
	}
	item, err := workspaces.Get(workspaceID)
	if err != nil {
		return gitLocation{}, err
	}
	pathValue, err := optionalString(args, "path")
	if err != nil {
		return gitLocation{}, err
	}

	cwd := item.Path
	if strings.TrimSpace(pathValue) != "" {
		_, resolved, err := workspaces.ResolveDirectory(workspaceID, pathValue)
		if err != nil {
			return gitLocation{}, fmt.Errorf("path: %w", err)
		}
		cwd = resolved
	}

	result, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitLocation{}, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot, err := workspaces.ResolvePath(workspaceID, item.Path, result.Stdout, true)
	if err != nil {
		return gitLocation{}, fmt.Errorf("git repository root escapes registered workspace: %w", err)
	}
	infoLocation := gitLocation{Workspace: item, CWD: cwd, RepoRoot: repoRoot}
	return infoLocation, nil
}

func gitPathspec(workspaces *workspace.Manager, location gitLocation, input string, mustExist bool) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("git path must not be empty")
	}
	if strings.HasPrefix(input, ":") {
		return "", errors.New("git pathspec magic is not allowed for workspace-bound tools")
	}
	if strings.ContainsRune(input, '\x00') {
		return "", errors.New("git path contains NUL")
	}
	resolved, err := workspaces.ResolvePath(location.Workspace.ID, location.CWD, input, mustExist)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(location.CWD, resolved)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relative), nil
}

func gitPathspecs(workspaces *workspace.Manager, location gitLocation, values []string, mustExist bool) ([]string, error) {
	result := make([]string, len(values))
	for index, value := range values {
		pathspec, err := gitPathspec(workspaces, location, value, mustExist)
		if err != nil {
			return nil, fmt.Errorf("files[%d]: %w", index, err)
		}
		result[index] = pathspec
	}
	return result, nil
}

func validateBranchName(ctx context.Context, cwd, name string) error {
	if err := validateGitScalar("branch", name); err != nil {
		return err
	}
	if _, err := runGit(ctx, cwd, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("invalid branch name %q: %w", name, err)
	}
	return nil
}

func validateGitScalar(key, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", key)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s must not start with '-'", key)
	}
	if strings.ContainsRune(value, '\x00') || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s contains invalid characters", key)
	}
	return nil
}

func runGit(ctx context.Context, cwd string, args ...string) (gitexec.Result, error) {
	if err := workspace.ValidateGitOperationContext(ctx, args); err != nil {
		return gitexec.Result{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	result, err := gitexec.OrThrow(runCtx, cwd, args...)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return gitexec.Result{}, fmt.Errorf("git command timed out after %s", gitTimeout)
	}
	return result, err
}

func runGitRaw(ctx context.Context, cwd string, args ...string) (gitexec.Result, error) {
	if err := workspace.ValidateGitOperationContext(ctx, args); err != nil {
		return gitexec.Result{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	result, err := gitexec.Run(runCtx, cwd, args...)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return gitexec.Result{}, fmt.Errorf("git command timed out after %s", gitTimeout)
	}
	return result, err
}

func gitLocationSchema(extra string) string {
	extra = strings.TrimSpace(extra)
	if extra != "" {
		extra = "," + strings.TrimSuffix(extra, ",")
	}
	return `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"}` + extra + `},"required":["workspace_id"],"additionalProperties":false}`
}

func optionalEnum(args map[string]any, key, fallback string, values ...string) (string, error) {
	value, err := optionalStringDefault(args, key, fallback)
	if err != nil {
		return "", err
	}
	for _, allowed := range values {
		if value == allowed {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s must be one of: %s", key, strings.Join(values, ", "))
}

func quoteFallback(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func quoteFallbackList(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quoteFallback(value)
	}
	return strings.Join(quoted, " ")
}
