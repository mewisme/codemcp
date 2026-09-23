package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	"go.mewis.me/codemcp/internal/controlguard"
)

var mutationWord = regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])(rm|rmdir|unlink|mv|rename|del|erase|move|ren|remove-item|move-item|rename-item)([^a-z0-9_.-]|$)|\bgit\s+(?:mv|rm|clean)\b|\bfind\b[\s\S]*\s-delete\b|\b(?:os\.(?:remove|unlink|rename|replace)|shutil\.(?:move|rmtree)|fs\.(?:unlink|rm|rename))\b`)

var cwdCommands = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "chdir": true, "set-location": true, "sl": true,
}

var mutationCommands = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true, "mv": true, "move": true, "ren": true, "rename": true,
	"del": true, "erase": true, "remove-item": true, "move-item": true, "rename-item": true, "shred": true, "clear-content": true,
}

var destructiveMutationCommands = map[string]string{
	"rm": "filesystem deletion", "rmdir": "directory deletion", "unlink": "filesystem deletion", "del": "filesystem deletion", "erase": "filesystem deletion",
	"remove-item": "filesystem deletion", "shred": "irreversible file overwrite", "truncate": "file truncation", "clear-content": "file content deletion",
}

var longMutationOptions = map[string]bool{
	"--force": true, "--recursive": true, "--verbose": true, "--interactive": true, "--no-clobber": true,
	"--dir": true, "--quiet": true, "--cached": true, "--ignore-unmatch": true,
	"-force": true, "-recurse": true, "-verbose": true, "-confirm:$false": true, "-whatif:$false": true,
	"-path": true, "-literalpath": true, "-destination": true,
}

func (m *Manager) IsMutationCommand(command string) bool {
	return m.isMutationCommand(command, 0)
}

func destructiveMutationReason(command string) (string, bool) {
	return destructiveMutationReasonDepth(command, 0)
}

func destructiveMutationReasonDepth(command string, depth int) (string, bool) {
	if depth >= maxNestedShellDepth {
		return "nested shell depth exceeded", true
	}
	segments, err := splitShellSegments(command)
	if err != nil {
		return "", false
	}
	for _, segment := range segments {
		tokens, err := shellWords(segment)
		if err != nil || len(tokens) == 0 {
			continue
		}
		name, args := commandName(tokens)
		if inner, ok := nestedShellCommand(name, args); ok {
			if reason, destructive := destructiveMutationReasonDepth(inner, depth+1); destructive {
				return reason, true
			}
		}
		if reason := destructiveMutationCommands[name]; reason != "" {
			return reason, true
		}
		if name == "find" && containsToken(args, "-delete") {
			return "recursive filesystem deletion", true
		}
		if name == "git" {
			if reason, ok := destructiveGitReason(args); ok {
				return reason, true
			}
		}
		switch name {
		case "sed":
			if hasSedInPlace(args) {
				return "in-place file overwrite", true
			}
		case "perl":
			if hasPerlInPlace(args) {
				return "in-place file overwrite", true
			}
		case "dd":
			if _, ok := assignmentValue(args, "of"); ok {
				return "raw file overwrite", true
			}
		case "rsync":
			if hasAnyOption(args, "--delete", "--delete-before", "--delete-during", "--delete-delay", "--delete-after", "--delete-excluded") {
				return "rsync destination deletion", true
			}
		case "terraform", "tofu":
			if firstCommandArg(args) == "fmt" && !hasAnyOption(args, "-check") {
				return "Terraform file rewrite", true
			}
		}
	}
	return "", false
}

func isGitMutation(args []string) bool {
	_, ok := destructiveGitReason(args)
	return ok
}

func destructiveGitReason(args []string) (string, bool) {
	command, rest, ok := gitCommand(args)
	if !ok {
		return "", false
	}
	switch command {
	case "push":
		if gitForcePush(rest) {
			return "Git force push", true
		}
	case "rm":
		return "Git tracked-file deletion", true
	case "clean":
		return "Git untracked-file deletion", true
	case "restore":
		return "Git working-tree overwrite", true
	case "reset":
		if containsAnyFold(rest, "--hard", "--merge", "--keep") {
			return "Git working-tree reset", true
		}
	case "checkout":
		if containsAnyFold(rest, "--", "-f", "--force") {
			return "Git working-tree overwrite", true
		}
	case "switch":
		if containsAnyFold(rest, "-f", "--force", "--discard-changes") {
			return "Git working-tree overwrite", true
		}
	case "stash":
		if len(rest) > 0 && (strings.EqualFold(rest[0], "drop") || strings.EqualFold(rest[0], "clear")) {
			return "Git stash deletion", true
		}
	case "branch":
		if containsAnyFold(rest, "-D", "--delete", "--force") {
			return "Git branch deletion", true
		}
	case "tag":
		if containsAnyFold(rest, "-d", "--delete") {
			return "Git tag deletion", true
		}
	}
	return "", false
}

func gitForcePush(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "-f" || lower == "--force" || lower == "--force-with-lease" || lower == "--force-if-includes" || strings.HasPrefix(lower, "--force-with-lease=") || strings.HasPrefix(lower, "--force-if-includes=") {
			return true
		}
	}
	return false
}

func ValidateGitOperationContext(ctx context.Context, args []string) error {
	reason, ok := destructiveGitReason(args)
	if !ok {
		return nil
	}
	code, category := controlguard.CodeDestructiveMutation, "destructive"
	if grant, ok := controlguard.GrantFromContext(ctx); ok && grant.Code == code {
		return nil
	}
	command := "git"
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	return controlguard.New(code, category+" Git mutation requires local approval: "+reason, true, &controlguard.Invocation{Command: command})
}

func hostMutationReason(command string) (string, bool) {
	return shellInvocationReason(command, hostMutationReasonForInvocation)
}

func externalMutationReason(command string) (string, bool) {
	return shellInvocationReason(command, externalMutationReasonForInvocation)
}

func externalAccessReason(command string) (string, bool) {
	return shellInvocationReason(command, externalAccessReasonForInvocation)
}

func ShellCommandUsesExternalNetwork(command string) bool {
	if _, ok := externalAccessReason(command); ok {
		return true
	}
	_, ok := externalMutationReason(command)
	return ok
}

func shellInvocationReason(command string, classify func(string, []string) (string, bool)) (string, bool) {
	return shellInvocationReasonDepth(command, classify, 0)
}

func shellInvocationReasonDepth(command string, classify func(string, []string) (string, bool), depth int) (string, bool) {
	if depth >= maxNestedShellDepth {
		return "", false
	}
	segments, err := splitShellSegments(command)
	if err != nil {
		return "", false
	}
	for _, segment := range segments {
		tokens, err := shellWords(segment)
		if err != nil || len(tokens) == 0 {
			continue
		}
		name, args := commandName(tokens)
		if reason, ok := classify(name, args); ok {
			return reason, true
		}
		if inner, ok := nestedShellCommand(name, args); ok {
			if reason, matched := shellInvocationReasonDepth(inner, classify, depth+1); matched {
				return reason, true
			}
		}
	}
	return "", false
}

func hostMutationReasonForInvocation(name string, args []string) (string, bool) {
	switch name {
	case "kill", "pkill", "killall", "taskkill", "stop-process":
		return "process termination", true
	case "shutdown", "reboot", "poweroff", "halt", "restart-computer", "stop-computer":
		return "host power-state change", true
	case "systemctl":
		if containsAnyFold(args, "start", "stop", "restart", "reload", "reload-or-restart", "try-restart", "enable", "disable", "reenable", "mask", "unmask", "isolate", "set-default", "daemon-reload", "reboot", "poweroff", "halt", "suspend", "hibernate", "hybrid-sleep") {
			return "system service mutation", true
		}
	case "service":
		if len(args) > 1 && containsAnyFold([]string{"start", "stop", "restart", "reload", "force-reload"}, args[len(args)-1]) {
			return "system service mutation", true
		}
	case "docker", "podman":
		return containerHostMutationReason(args)
	case "apt", "apt-get", "dnf", "yum", "zypper", "apk", "brew", "choco", "winget", "scoop":
		if packageManagerMutation(args) {
			return "host package-manager mutation", true
		}
	}
	return "", false
}

func containerHostMutationReason(args []string) (string, bool) {
	command := firstCommandArg(args, "--context", "-h", "--host", "--config", "--log-level")
	if command == "" {
		return "", false
	}
	commandIndex := indexFold(args, command)
	rest := args
	if commandIndex >= 0 && commandIndex+1 < len(args) {
		rest = args[commandIndex+1:]
	}
	if command == "compose" && len(rest) > 0 {
		switch firstCommandArg(rest, "-f", "--file", "--project-name", "--project-directory", "--env-file", "--profile") {
		case "up", "down", "start", "stop", "restart", "rm", "create", "run":
			return "container runtime state mutation", true
		}
		return "", false
	}
	switch command {
	case "run", "create", "start", "stop", "restart", "kill", "rm", "rmi", "rename", "pause", "unpause", "update", "commit", "import", "load":
		return "container runtime state mutation", true
	case "container", "image", "volume", "network", "system", "builder":
		if len(args) > 1 && containsAnyFold([]string{"rm", "prune", "create", "disconnect", "connect"}, args[1]) {
			return "container runtime state mutation", true
		}
	}
	return "", false
}

func packageManagerMutation(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch strings.ToLower(arg) {
		case "install", "remove", "uninstall", "purge", "upgrade", "dist-upgrade", "full-upgrade", "update", "autoremove", "clean", "reinstall", "add", "del", "delete", "link", "unlink", "pin", "unpin":
			return true
		}
	}
	return false
}

func externalMutationReasonForInvocation(name string, args []string) (string, bool) {
	switch name {
	case "git":
		if command, _, ok := gitCommand(args); ok && command == "push" {
			return "remote Git mutation", true
		}
	case "rsync":
		if destination, ok := rsyncDestination(args); ok && looksRemotePath(destination) {
			return "remote rsync mutation", true
		}
	case "ssh", "scp":
		return "remote host mutation capability", true
	case "kubectl":
		if kubectlMutation(args) {
			return "Kubernetes cluster mutation", true
		}
	case "helm":
		if helmMutation(args) {
			return "Helm release mutation", true
		}
	case "terraform", "tofu":
		if terraformMutation(args) {
			return "infrastructure mutation", true
		}
	case "npm", "pnpm", "yarn", "bun", "cargo", "twine":
		if registryPublishMutation(name, args) {
			return "package registry mutation", true
		}
	case "docker", "podman":
		if firstCommandArg(args, "--context", "-h", "--host", "--config", "--log-level") == "push" {
			return "remote container registry mutation", true
		}
	case "curl":
		if externalHTTPMutation(args) {
			return "remote HTTP mutation", true
		}
	}
	return "", false
}

func externalAccessReasonForInvocation(name string, args []string) (string, bool) {
	switch name {
	case "curl", "wget":
		for _, arg := range args {
			value := strings.ToLower(strings.TrimSpace(arg))
			if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "ftp://") || strings.HasPrefix(value, "ftps://") {
				return "HTTP/FTP network access", true
			}
		}
	case "git":
		command, _, ok := gitCommand(args)
		if !ok {
			return "", false
		}
		switch command {
		case "clone", "fetch", "pull", "push", "ls-remote":
			return "remote Git access", true
		case "submodule":
			if containsAnyFold(args, "update", "sync", "foreach") {
				return "Git submodule remote access", true
			}
		}
	case "ssh", "scp", "sftp", "ftp", "telnet":
		return "remote host access", true
	case "rsync":
		if rsyncUsesRemote(args) {
			return "remote rsync access", true
		}
	case "kubectl":
		if command := firstCommandArg(args, "--context", "--namespace", "-n", "--kubeconfig", "--cluster", "--user", "--server", "--token"); command != "" && command != "config" {
			return "Kubernetes API access", true
		}
	case "helm":
		command := firstCommandArg(args, "--namespace", "-n", "--kube-context", "--kubeconfig", "--registry-config", "--repository-cache", "--repository-config")
		switch command {
		case "list", "status", "get", "history", "search", "install", "upgrade", "rollback", "uninstall", "pull", "push", "repo", "registry", "dependency":
			return "Helm network access", true
		}
	case "terraform", "tofu":
		if command := firstCommandArg(args); command == "init" {
			return "Terraform provider/module access", true
		}
	case "npm", "pnpm", "yarn", "bun":
		command := firstCommandArg(args, "--prefix", "--dir", "-c", "--cwd", "--registry", "--config")
		switch command {
		case "install", "add", "update", "upgrade", "outdated", "view", "info", "search", "audit", "publish", "unpublish", "deprecate", "dist-tag", "owner", "access", "login", "logout", "whoami":
			return "package registry access", true
		}
	case "cargo":
		command := firstCommandArg(args, "--manifest-path", "--registry", "--config")
		switch command {
		case "install", "search", "publish", "yank", "login", "logout":
			return "Cargo registry access", true
		}
	case "docker", "podman":
		command := firstCommandArg(args, "--context", "-h", "--host", "--config", "--log-level")
		switch command {
		case "pull", "push", "login", "logout", "search":
			return "container registry access", true
		}
	case "apt", "apt-get", "dnf", "yum", "zypper", "apk", "brew", "choco", "winget", "scoop":
		if packageManagerMutation(args) {
			return "package-manager network access", true
		}
	case "cgm", "cmcp", "chatgpt-mcp":
		if firstCommandArg(args) == "update" {
			return "control-plane update access", true
		}
	}
	return "", false
}

func kubectlMutation(args []string) bool {
	command := firstCommandArg(args, "--context", "--namespace", "-n", "--kubeconfig", "--cluster", "--user", "--server", "--token")
	if command == "" {
		return false
	}
	switch command {
	case "get", "describe", "logs", "top", "api-resources", "api-versions", "cluster-info", "explain", "version":
		return false
	case "config":
		return false
	default:
		return true
	}
}

func helmMutation(args []string) bool {
	command := firstCommandArg(args, "--namespace", "-n", "--kube-context", "--kubeconfig", "--registry-config", "--repository-cache", "--repository-config")
	if command == "" {
		return false
	}
	switch command {
	case "list", "status", "get", "history", "show", "search", "template", "lint", "version", "env", "completion":
		return false
	default:
		return true
	}
}

func terraformMutation(args []string) bool {
	command := firstCommandArg(args)
	if command == "" {
		return false
	}
	switch command {
	case "plan", "show", "output", "validate", "fmt", "graph", "providers", "version":
		return false
	default:
		return true
	}
}

func registryPublishMutation(name string, args []string) bool {
	command := firstCommandArg(args, "--prefix", "--dir", "-c", "--cwd", "--manifest-path", "--registry", "--config")
	switch name {
	case "npm", "pnpm", "yarn", "bun":
		return command == "publish" || command == "unpublish" || command == "deprecate" || command == "dist-tag" || command == "owner" || command == "access"
	case "cargo":
		return command == "publish" || command == "yank"
	case "twine":
		return command == "upload"
	default:
		return false
	}
}

func externalHTTPMutation(args []string) bool {
	method := ""
	hasBody := false
	for i := 0; i < len(args); i++ {
		arg := strings.ToLower(args[i])
		switch {
		case arg == "-x" || arg == "--request":
			if i+1 < len(args) {
				method = strings.ToUpper(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--request="):
			method = strings.ToUpper(args[i][len("--request="):])
		case arg == "-d" || arg == "--data" || arg == "--data-raw" || arg == "--data-binary" || arg == "--data-urlencode" || arg == "-f" || arg == "--form" || arg == "--upload-file" || arg == "-t":
			hasBody = true
		}
	}
	if method == "" && hasBody {
		method = "POST"
	}
	if method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return false
	}
	return true
}

func firstCommandArg(args []string, valueFlags ...string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return strings.ToLower(arg)
		}
		lower := strings.ToLower(arg)
		for _, flag := range valueFlags {
			flag = strings.ToLower(flag)
			if lower == flag {
				i++
				break
			}
		}
	}
	return ""
}

func gitCommand(args []string) (string, []string, bool) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		lower := strings.ToLower(arg)
		if arg == "--" {
			return "", nil, false
		}
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "=") {
				continue
			}
			if containsAnyFold([]string{"-c", "-C", "--git-dir", "--work-tree", "--namespace", "--exec-path"}, arg) {
				if index+1 >= len(args) {
					return "", nil, false
				}
				index++
			}
			continue
		}
		return lower, args[index+1:], true
	}
	return "", nil, false
}

func indexFold(values []string, target string) int {
	for index, value := range values {
		if strings.EqualFold(value, target) {
			return index
		}
	}
	return -1
}

func containsAnyFold(values []string, targets ...string) bool {
	for _, value := range values {
		for _, target := range targets {
			if strings.EqualFold(value, target) {
				return true
			}
		}
	}
	return false
}

func (m *Manager) ValidateMutationCommand(id, baseDirectory, command string) error {
	_, cwd, err := m.ResolveDirectory(id, baseDirectory)
	if err != nil {
		return err
	}
	return m.validateMutationCommandDepth(id, cwd, command, 0)
}

func (m *Manager) validateMutationCommandDepth(id, cwd, command string, depth int) error {
	if depth >= maxNestedShellDepth {
		return errors.New("mutation command denied: nested shell depth exceeded")
	}
	if !m.IsMutationCommand(command) {
		return nil
	}

	segments, err := splitShellSegments(command)
	if err != nil {
		return fmt.Errorf("mutation command denied: %w", err)
	}

	redirections, err := outputRedirectionTargets(command)
	if err != nil {
		return fmt.Errorf("mutation command denied: %w", err)
	}
	recognizedMutation := false
	for _, target := range redirections {
		recognizedMutation = true
		if isNullDevice(target) {
			continue
		}
		if err := m.validateLiteralPath(id, cwd, target, false); err != nil {
			return fmt.Errorf("mutation command denied: output redirection: %w", err)
		}
	}
	for _, segment := range segments {
		tokens, err := shellWords(segment)
		if err != nil {
			return fmt.Errorf("mutation command denied: %w", err)
		}
		if len(tokens) == 0 {
			continue
		}
		name, args := commandName(tokens)
		if inner, ok := nestedShellCommand(name, args); ok && m.isMutationCommand(inner, 1) {
			if err := m.validateMutationCommandDepth(id, cwd, inner, depth+1); err != nil {
				return fmt.Errorf("mutation command denied: nested %s mutation: %w", name, err)
			}
			recognizedMutation = true
			continue
		}
		if code, ok := inlineInterpreterCode(name, args); ok && inlineMutationAPI.MatchString(code) {
			return fmt.Errorf("mutation command denied: inline %s mutation cannot be proven workspace-safe", name)
		}
		if cwdCommands[name] {
			if name == "popd" {
				return errors.New("mutation command denied: popd cannot be proven workspace-safe")
			}
			if name == "pushd" && len(args) == 0 {
				return errors.New("mutation command denied: pushd requires an explicit target")
			}
			target := "."
			if len(args) > 0 {
				target = args[0]
			}
			resolved, err := m.ResolvePath(id, cwd, target, true)
			if err != nil {
				return fmt.Errorf("mutation command denied: cwd change target is invalid: %w", err)
			}
			if resolved != cwd {
				return fmt.Errorf("mutation command denied: cwd change from %s to %s", cwd, resolved)
			}
			continue
		}

		if name == "git" && len(args) > 0 {
			gitCommandName, gitArgs, hasGitCommand := gitCommand(args)
			if hasGitCommand && isGitMutation(args) {
				if err := m.validateGitPaths(id, cwd, args); err != nil {
					return fmt.Errorf("mutation command denied: git: %w", err)
				}
			}
			switch gitCommandName {
			case "mv":
				recognizedMutation = true
				if err := m.validateLiteralOperands(id, cwd, gitArgs, 2); err != nil {
					return fmt.Errorf("mutation command denied: git mv: %w", err)
				}
				continue
			case "rm":
				recognizedMutation = true
				if err := m.validateLiteralOperands(id, cwd, gitArgs, 1); err != nil {
					return fmt.Errorf("mutation command denied: git rm: %w", err)
				}
				continue
			case "clean":
				recognizedMutation = true
				for _, arg := range gitArgs {
					if !strings.HasPrefix(arg, "-") {
						if err := m.validateLiteralPath(id, cwd, arg, false); err != nil {
							return fmt.Errorf("mutation command denied: git clean: %w", err)
						}
					}
				}
				continue
			}
			if isGitMutation(args) {
				recognizedMutation = true
				continue
			}
		}
		if name == "find" && containsToken(args, "-delete") {
			recognizedMutation = true
			roots := findRoots(args)
			if len(roots) == 0 {
				roots = []string{"."}
			}
			for _, root := range roots {
				if err := m.validateLiteralPath(id, cwd, root, true); err != nil {
					return fmt.Errorf("mutation command denied: find -delete: %w", err)
				}
			}
			continue
		}
		if _, ok := hostMutationReasonForInvocation(name, args); ok {
			recognizedMutation = true
			continue
		}
		if _, ok := externalMutationReasonForInvocation(name, args); ok {
			recognizedMutation = true
			if err := m.validateExternalMutation(id, cwd, name, args); err != nil {
				return fmt.Errorf("mutation command denied: %s: %w", name, err)
			}
			continue
		}
		if writeCommands[name] {
			recognizedMutation = true
			if err := m.validateWriteOperands(id, cwd, name, args); err != nil {
				return fmt.Errorf("mutation command denied: %s: %w", name, err)
			}
			continue
		}
		if pathMutationCommands[name] && isKnownPathMutation(name, args) {
			recognizedMutation = true
			if err := m.validateKnownPathMutation(id, cwd, name, args); err != nil {
				return fmt.Errorf("mutation command denied: %s: %w", name, err)
			}
			continue
		}
		if mutationCommands[name] {
			recognizedMutation = true
			minimum := 1
			if name == "mv" || name == "move" || name == "ren" || name == "rename" || name == "move-item" || name == "rename-item" {
				minimum = 2
			}
			if name == "clear-content" {
				if err := m.validatePowerShellWriteOperands(id, cwd, name, args); err != nil {
					return fmt.Errorf("mutation command denied: %s: %w", name, err)
				}
				continue
			}
			if err := m.validateLiteralOperands(id, cwd, args, minimum); err != nil {
				return fmt.Errorf("mutation command denied: %s: %w", name, err)
			}
		}
	}

	if !recognizedMutation {
		return errors.New("mutation command denied: destructive/rename operation cannot be proven workspace-safe")
	}
	return nil
}

func (m *Manager) validateExternalMutation(id, cwd, name string, args []string) error {
	switch name {
	case "ssh":
		return validateSSHInvocation(args)
	case "git":
		return m.validateGitPaths(id, cwd, args)
	case "scp":
		return m.validateSCPInvocation(id, cwd, args)
	case "rsync":
		return m.validateRsyncTransfer(id, cwd, args)
	case "terraform", "tofu":
		return m.validateTerraformChdir(id, cwd, args)
	case "curl":
		return m.validateCurlInputs(id, cwd, args)
	case "kubectl":
		return m.validateFlagPaths(id, cwd, args, []string{"--kubeconfig"})
	case "helm":
		return m.validateFlagPaths(id, cwd, args, []string{"--kubeconfig", "--registry-config", "--repository-cache", "--repository-config"})
	case "cargo":
		return m.validateFlagPaths(id, cwd, args, []string{"--manifest-path"})
	case "npm":
		return m.validateFlagPaths(id, cwd, args, []string{"--prefix"})
	case "pnpm":
		return m.validateFlagPaths(id, cwd, args, []string{"--dir", "-c"})
	case "yarn":
		return m.validateFlagPaths(id, cwd, args, []string{"--cwd"})
	default:
		return nil
	}
}

func (m *Manager) validateGitPaths(id, cwd string, args []string) error {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		lower := strings.ToLower(arg)
		switch {
		case arg == "-C":
			if index+1 >= len(args) {
				return errors.New("-C requires a path")
			}
			if err := m.validateLiteralPath(id, cwd, args[index+1], true); err != nil {
				return err
			}
			index++
		case strings.HasPrefix(arg, "-C") && len(arg) > 2:
			if err := m.validateLiteralPath(id, cwd, arg[2:], true); err != nil {
				return err
			}
		case lower == "--git-dir" || lower == "--work-tree":
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a path", arg)
			}
			if err := m.validateLiteralPath(id, cwd, args[index+1], true); err != nil {
				return err
			}
			index++
		case strings.HasPrefix(lower, "--git-dir="):
			if err := m.validateLiteralPath(id, cwd, arg[len("--git-dir="):], true); err != nil {
				return err
			}
		case strings.HasPrefix(lower, "--work-tree="):
			if err := m.validateLiteralPath(id, cwd, arg[len("--work-tree="):], true); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSSHInvocation(args []string) error {
	positionals, err := commandPositionals(args, map[string]bool{"-b": true, "-c": true, "-d": true, "-e": true, "-f": true, "-i": true, "-j": true, "-l": true, "-o": true, "-p": true, "-q": true, "-r": true, "-s": true, "-w": true})
	if err != nil {
		return err
	}
	if len(positionals) < 2 {
		return errors.New("interactive ssh session cannot be proven bounded; provide an explicit remote command")
	}
	return nil
}

func (m *Manager) validateSCPInvocation(id, cwd string, args []string) error {
	positionals, err := commandPositionals(args, map[string]bool{"-c": true, "-d": true, "-f": true, "-i": true, "-j": true, "-l": true, "-o": true, "-p": true, "-s": true})
	if err != nil {
		return err
	}
	if len(positionals) < 2 {
		return errors.New("scp requires source and destination")
	}
	for index, value := range positionals {
		remote := looksRemotePath(value)
		if remote {
			continue
		}
		mustExist := index < len(positionals)-1
		if err := m.validateLiteralPath(id, cwd, value, mustExist); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) validateRsyncTransfer(id, cwd string, args []string) error {
	positionals, err := commandPositionals(args, map[string]bool{"-e": true, "--rsh": true, "--exclude-from": true, "--include-from": true, "--files-from": true, "--filter": true, "--password-file": true})
	if err != nil {
		return err
	}
	if len(positionals) < 2 {
		return errors.New("rsync requires source and destination")
	}
	for index, value := range positionals {
		if looksRemotePath(value) {
			continue
		}
		mustExist := index < len(positionals)-1
		if err := m.validateLiteralPath(id, cwd, value, mustExist); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) validateTerraformChdir(id, cwd string, args []string) error {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "-chdir=") {
			return m.validateLiteralPath(id, cwd, arg[len("-chdir="):], true)
		}
		if lower == "-chdir" {
			if index+1 >= len(args) {
				return errors.New("-chdir requires a path")
			}
			return m.validateLiteralPath(id, cwd, args[index+1], true)
		}
	}
	return nil
}

func (m *Manager) validateCurlInputs(id, cwd string, args []string) error {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		lower := strings.ToLower(arg)
		switch lower {
		case "-t", "--upload-file":
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a path", arg)
			}
			if err := m.validateLiteralPath(id, cwd, args[index+1], true); err != nil {
				return err
			}
			index++
		case "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode", "-f", "--form":
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			if path, ok := curlFileReference(args[index+1]); ok {
				if err := m.validateLiteralPath(id, cwd, path, true); err != nil {
					return err
				}
			}
			index++
		default:
			if strings.HasPrefix(lower, "--upload-file=") {
				if err := m.validateLiteralPath(id, cwd, arg[len("--upload-file="):], true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func curlFileReference(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@") && len(value) > 1 {
		return strings.TrimPrefix(value, "@"), true
	}
	if index := strings.Index(value, "=@"); index >= 0 && index+2 < len(value) {
		return value[index+2:], true
	}
	return "", false
}

func (m *Manager) validateFlagPaths(id, cwd string, args, flags []string) error {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		lower := strings.ToLower(arg)
		for _, flag := range flags {
			flag = strings.ToLower(flag)
			if lower == flag {
				if index+1 >= len(args) {
					return fmt.Errorf("%s requires a path", arg)
				}
				if err := m.validateLiteralPath(id, cwd, args[index+1], true); err != nil {
					return err
				}
				index++
				break
			}
			if strings.HasPrefix(lower, flag+"=") {
				if err := m.validateLiteralPath(id, cwd, arg[len(flag)+1:], true); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func commandPositionals(args []string, valueOptions map[string]bool) ([]string, error) {
	positionals := make([]string, 0, len(args))
	optionsDone := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		lower := strings.ToLower(arg)
		if !optionsDone && arg == "--" {
			optionsDone = true
			continue
		}
		if !optionsDone && strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "=") {
				continue
			}
			if valueOptions[lower] {
				if index+1 >= len(args) {
					return nil, fmt.Errorf("%s requires a value", arg)
				}
				index++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return positionals, nil
}

func (m *Manager) validateKnownPathMutation(id, cwd, name string, args []string) error {
	switch name {
	case "chmod", "chown", "chgrp":
		return m.validateMetadataMutation(id, cwd, args)
	case "sed":
		return m.validateSedInPlace(id, cwd, args)
	case "perl":
		return m.validatePerlInPlace(id, cwd, args)
	case "dd":
		value, ok := assignmentValue(args, "of")
		if !ok || strings.TrimSpace(value) == "" {
			return errors.New("dd output path is required")
		}
		return m.validateLiteralPath(id, cwd, value, false)
	case "rsync":
		return m.validateRsyncDestination(id, cwd, args)
	case "curl":
		return m.validateOptionPaths(id, cwd, args, map[string]bool{"-o": true, "--output": true, "--output-dir": true})
	case "wget":
		return m.validateOptionPaths(id, cwd, args, map[string]bool{"-o": true, "--output-document": true, "-p": true, "--directory-prefix": true})
	default:
		return nil
	}
}

func (m *Manager) validateMetadataMutation(id, cwd string, args []string) error {
	positionals := make([]string, 0, len(args))
	reference := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(strings.ToLower(arg), "--reference=") {
			if err := m.validateLiteralPath(id, cwd, arg[len("--reference="):], true); err != nil {
				return err
			}
			reference = true
			continue
		}
		if strings.EqualFold(arg, "--reference") {
			if i+1 >= len(args) {
				return errors.New("--reference requires a path")
			}
			if err := m.validateLiteralPath(id, cwd, args[i+1], true); err != nil {
				return err
			}
			reference = true
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positionals = append(positionals, arg)
	}
	minimum := 2
	pathStart := 1
	if reference {
		minimum, pathStart = 1, 0
	}
	if len(positionals) < minimum {
		return errors.New("metadata mutation requires mode/owner and target path")
	}
	for _, path := range positionals[pathStart:] {
		if err := m.validateLiteralPath(id, cwd, path, false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) validateSedInPlace(id, cwd string, args []string) error {
	files := scriptMutationFiles(args, true)
	if len(files) == 0 {
		return errors.New("sed in-place mutation requires a literal target path")
	}
	for _, path := range files {
		if err := m.validateLiteralPath(id, cwd, path, false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) validatePerlInPlace(id, cwd string, args []string) error {
	files := scriptMutationFiles(args, false)
	if len(files) == 0 {
		return errors.New("perl in-place mutation requires a literal target path")
	}
	for _, path := range files {
		if err := m.validateLiteralPath(id, cwd, path, false); err != nil {
			return err
		}
	}
	return nil
}

func scriptMutationFiles(args []string, sed bool) []string {
	var files []string
	expressionProvided := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		lower := strings.ToLower(arg)
		if lower == "-e" || lower == "--expression" {
			expressionProvided = true
			i++
			continue
		}
		if strings.HasPrefix(lower, "--expression=") {
			expressionProvided = true
			continue
		}
		if lower == "-f" || lower == "--file" {
			expressionProvided = true
			i++
			continue
		}
		if strings.HasPrefix(lower, "--file=") || strings.HasPrefix(arg, "-") {
			continue
		}
		if !expressionProvided {
			expressionProvided = true
			continue
		}
		files = append(files, arg)
	}
	if !sed && len(files) == 0 && expressionProvided {
		return files
	}
	return files
}

func (m *Manager) validateRsyncDestination(id, cwd string, args []string) error {
	destination, ok := rsyncDestination(args)
	if !ok {
		return errors.New("rsync requires source and destination")
	}
	if looksRemotePath(destination) {
		return nil
	}
	return m.validateLiteralPath(id, cwd, destination, false)
}

func rsyncDestination(args []string) (string, bool) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if optionConsumesNext(strings.ToLower(arg), "--exclude-from", "--include-from", "--files-from", "--filter", "-e", "--rsh") {
				i++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) < 2 {
		return "", false
	}
	return positionals[len(positionals)-1], true
}

func rsyncUsesRemote(args []string) bool {
	positionals, err := commandPositionals(args, map[string]bool{"-e": true, "--rsh": true, "--exclude-from": true, "--include-from": true, "--files-from": true, "--filter": true, "--password-file": true})
	if err != nil {
		return false
	}
	for _, value := range positionals {
		if looksRemotePath(value) {
			return true
		}
	}
	return false
}

func (m *Manager) validateOptionPaths(id, cwd string, args []string, options map[string]bool) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		lower := strings.ToLower(arg)
		if options[lower] {
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", arg)
			}
			if err := m.validateLiteralPath(id, cwd, args[i+1], false); err != nil {
				return err
			}
			i++
			continue
		}
		for option := range options {
			if strings.HasPrefix(lower, option+"=") {
				if err := m.validateLiteralPath(id, cwd, arg[len(option)+1:], false); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func optionConsumesNext(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func looksRemotePath(value string) bool {
	if strings.Contains(value, "://") {
		return true
	}
	colon := strings.IndexByte(value, ':')
	return colon > 0 && !filepath.IsAbs(value)
}

func (m *Manager) validateLiteralOperands(id, cwd string, args []string, minimum int) error {
	operands := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if !allowedMutationOption(arg) {
				return fmt.Errorf("unsupported option %q", arg)
			}
			continue
		}
		operands = append(operands, arg)
	}
	if len(operands) < minimum {
		return fmt.Errorf("expected at least %d literal path operand(s)", minimum)
	}
	for _, operand := range operands {
		if err := m.validateLiteralPath(id, cwd, operand, false); err != nil {
			return err
		}
	}
	return nil
}

func allowedMutationOption(value string) bool {
	lower := strings.ToLower(value)
	if longMutationOptions[lower] {
		return true
	}
	if !strings.HasPrefix(value, "-") || strings.HasPrefix(value, "--") || len(value) < 2 {
		return false
	}
	for _, flag := range value[1:] {
		if !strings.ContainsRune("frRvinTdq", flag) {
			return false
		}
	}
	return true
}

func (m *Manager) validateLiteralPath(id, cwd, value string, mustExist bool) error {
	if unsafeShellPath(value) {
		return fmt.Errorf("dynamic path %q is not allowed", value)
	}
	path := value
	if index := strings.IndexAny(path, "*?["); index >= 0 {
		path = path[:index]
		path = strings.TrimRight(path, `/\`)
		if path == "" {
			path = "."
		} else if filepath.Base(path) != "." {
			path = filepath.Dir(path)
		}
		mustExist = true
	}
	if _, err := m.ResolvePath(id, cwd, path, mustExist); err != nil {
		return err
	}
	return nil
}

func unsafeShellPath(value string) bool {
	return strings.ContainsAny(value, "$`{}<>|;&\n\r") || strings.HasPrefix(value, "~") || strings.Contains(value, "$(")
}

func commandName(tokens []string) (string, []string) {
	index := 0
	for index < len(tokens) {
		token := strings.ToLower(filepath.Base(tokens[index]))
		if token == "sudo" || token == "command" || token == "exec" || token == "nohup" {
			index++
			continue
		}
		if token == "env" {
			index++
			for index < len(tokens) {
				current := strings.ToLower(tokens[index])
				switch {
				case strings.Contains(tokens[index], "="):
					index++
				case current == "-i" || current == "--ignore-environment":
					index++
				case current == "-u" || current == "--unset":
					if index+1 >= len(tokens) {
						return "", nil
					}
					index += 2
				case strings.HasPrefix(current, "--unset="):
					index++
				default:
					goto envDone
				}
			}
		envDone:
			continue
		}
		if strings.Contains(tokens[index], "=") && !strings.ContainsAny(tokens[index], `/\`) {
			index++
			continue
		}
		if index >= len(tokens) {
			return "", nil
		}
		return strings.ToLower(filepath.Base(tokens[index])), tokens[index+1:]
	}
	return "", nil
}

func splitShellSegments(command string) ([]string, error) {
	var segments []string
	var current strings.Builder
	var quote rune
	escaped := false
	for i, r := range command {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			current.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			current.WriteRune(r)
			continue
		}
		if r == ';' || r == '\n' || r == '\r' || r == '&' || r == '|' {
			if text := strings.TrimSpace(current.String()); text != "" {
				segments = append(segments, text)
			}
			current.Reset()
			if (r == '&' || r == '|') && i+1 < len(command) {
				continue
			}
			continue
		}
		current.WriteRune(r)
	}
	if quote != 0 || escaped {
		return nil, errors.New("unbalanced shell quoting")
	}
	if text := strings.TrimSpace(current.String()); text != "" {
		segments = append(segments, text)
	}
	return segments, nil
}

func shellWords(segment string) ([]string, error) {
	var words []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for _, r := range segment {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' && runtime.GOOS != "windows" {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		current.WriteRune(r)
	}
	if quote != 0 || escaped {
		return nil, errors.New("unbalanced shell quoting")
	}
	flush()
	return words, nil
}

func containsToken(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func findRoots(args []string) []string {
	var roots []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || arg == "!" || arg == "(" {
			break
		}
		roots = append(roots, arg)
	}
	return roots
}
