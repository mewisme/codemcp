package shell

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrShellUnavailable = errors.New("shell runtime unavailable")

type ProviderSource string

const (
	ProviderSourceConfigured ProviderSource = "configured"
	ProviderSourceSystem     ProviderSource = "system"
)

type ProviderKind string

const (
	ProviderPOSIX             ProviderKind = "posix"
	ProviderGitBash           ProviderKind = "git_bash"
	ProviderPowerShell7       ProviderKind = "powershell7"
	ProviderWindowsPowerShell ProviderKind = "windows_powershell"
)

type Provider struct {
	Executable string         `json:"executable"`
	Language   string         `json:"language"`
	Source     ProviderSource `json:"source"`
	Kind       ProviderKind   `json:"kind"`
	Path       []string       `json:"path,omitempty"`
}

type ShellUnavailableError struct {
	GOOS string
}

func (e *ShellUnavailableError) Error() string {
	if e != nil && e.GOOS == "windows" {
		return "no supported Windows shell runtime found; install Git for Windows or PowerShell 7, or ensure Windows PowerShell is available"
	}
	return "no supported shell runtime found; configure SHELL or install Bash"
}

func (e *ShellUnavailableError) Unwrap() error {
	return ErrShellUnavailable
}

type ProviderResolver struct {
	goos     string
	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
	getenv   func(string) string
}

func NewProviderResolver() *ProviderResolver {
	return &ProviderResolver{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		stat:     os.Stat,
		getenv:   os.Getenv,
	}
}

func (r *ProviderResolver) Resolve(configuredPaths []string) (Provider, error) {
	if r == nil {
		return Provider{}, errors.New("shell provider resolver is unavailable")
	}
	paths, err := normalizeProviderPaths(configuredPaths, r.goos)
	if err != nil {
		return Provider{}, err
	}
	if r.goos == "windows" {
		if provider, ok := r.resolveConfiguredWindows(paths); ok {
			return provider, nil
		}
	} else if provider, ok := r.resolveConfiguredPOSIX(paths); ok {
		return provider, nil
	}
	if explicit := strings.TrimSpace(r.getenv("SHELL")); explicit != "" {
		if provider, explicitErr := r.resolveExplicit(explicit, paths); explicitErr == nil {
			return provider, nil
		}
	}
	if r.goos == "windows" {
		return r.resolveWindows(paths)
	}
	return r.resolvePOSIX(paths)
}

func (r *ProviderResolver) resolveExplicit(value string, configuredPaths []string) (Provider, error) {
	executable := strings.TrimSpace(value)
	source := ProviderSourceConfigured
	if !filepath.IsAbs(executable) {
		var err error
		executable, _, err = r.lookupExecutable(executable, configuredPaths)
		if err != nil {
			return Provider{}, fmt.Errorf("configured SHELL %q is unavailable: %w", value, err)
		}
	}
	if err := r.validateExecutable(executable); err != nil {
		return Provider{}, fmt.Errorf("configured SHELL %q is invalid: %w", value, err)
	}
	base := strings.ToLower(filepath.Base(executable))
	if r.goos == "windows" {
		switch base {
		case "bash", "bash.exe":
			return providerFor(executable, "bash", ProviderGitBash, source), nil
		case "pwsh", "pwsh.exe":
			return providerFor(executable, "powershell", ProviderPowerShell7, source), nil
		case "powershell", "powershell.exe":
			return providerFor(executable, "powershell", ProviderWindowsPowerShell, source), nil
		default:
			return Provider{}, fmt.Errorf("configured SHELL %q is unsupported on Windows; use Git Bash, pwsh.exe, or powershell.exe", value)
		}
	}
	return providerFor(executable, shellMarkdownLanguage(executable), ProviderPOSIX, source), nil
}

func (r *ProviderResolver) resolvePOSIX(configuredPaths []string) (Provider, error) {
	executable, configured, err := r.lookupExecutable("bash", configuredPaths)
	if err == nil {
		source := ProviderSourceSystem
		if configured {
			source = ProviderSourceConfigured
		}
		return providerFor(executable, "bash", ProviderPOSIX, source), nil
	}
	if err := r.validateExecutable("/bin/sh"); err == nil {
		return providerFor("/bin/sh", "sh", ProviderPOSIX, ProviderSourceSystem), nil
	}
	return Provider{}, &ShellUnavailableError{GOOS: r.goos}
}

func (r *ProviderResolver) resolveConfiguredPOSIX(configuredPaths []string) (Provider, bool) {
	for _, root := range configuredPaths {
		for _, name := range []string{"bash", "zsh", "fish", "sh"} {
			executable := filepath.Join(root, name)
			if r.validateExecutable(executable) == nil {
				return providerFor(executable, shellMarkdownLanguage(executable), ProviderPOSIX, ProviderSourceConfigured), true
			}
		}
	}
	return Provider{}, false
}

func (r *ProviderResolver) resolveWindows(configuredPaths []string) (Provider, error) {
	if provider, ok := r.resolveConfiguredWindows(configuredPaths); ok {
		return provider, nil
	}
	if executable, source, err := r.windowsGitBash(configuredPaths); err == nil {
		return providerFor(executable, "bash", ProviderGitBash, source), nil
	}
	if executable, configured, err := r.lookupExecutable("pwsh.exe", configuredPaths); err == nil {
		source := ProviderSourceSystem
		if configured {
			source = ProviderSourceConfigured
		}
		return providerFor(executable, "powershell", ProviderPowerShell7, source), nil
	}
	if executable, configured, err := r.lookupExecutable("powershell.exe", configuredPaths); err == nil {
		source := ProviderSourceSystem
		if configured {
			source = ProviderSourceConfigured
		}
		return providerFor(executable, "powershell", ProviderWindowsPowerShell, source), nil
	}
	return Provider{}, &ShellUnavailableError{GOOS: "windows"}
}

func (r *ProviderResolver) resolveConfiguredWindows(configuredPaths []string) (Provider, bool) {
	for _, root := range configuredPaths {
		for _, candidate := range []struct {
			name     string
			language string
			kind     ProviderKind
		}{
			{name: "bash.exe", language: "bash", kind: ProviderGitBash},
			{name: "pwsh.exe", language: "powershell", kind: ProviderPowerShell7},
			{name: "powershell.exe", language: "powershell", kind: ProviderWindowsPowerShell},
		} {
			executable := filepath.Join(root, candidate.name)
			if r.validateExecutable(executable) == nil {
				return providerFor(executable, candidate.language, candidate.kind, ProviderSourceConfigured), true
			}
		}
		gitExecutable := filepath.Join(root, "git.exe")
		if r.validateExecutable(gitExecutable) == nil {
			gitRoot := filepath.Dir(filepath.Dir(filepath.Clean(gitExecutable)))
			for _, executable := range gitBashCandidates(gitRoot) {
				if r.validateExecutable(executable) == nil {
					return providerFor(executable, "bash", ProviderGitBash, ProviderSourceConfigured), true
				}
			}
		}
	}
	return Provider{}, false
}

func (r *ProviderResolver) windowsGitBash(configuredPaths []string) (string, ProviderSource, error) {
	if gitExecutable, configured, err := r.lookupExecutable("git.exe", configuredPaths); err == nil {
		gitRoot := filepath.Dir(filepath.Dir(filepath.Clean(gitExecutable)))
		for _, candidate := range gitBashCandidates(gitRoot) {
			if r.validateExecutable(candidate) == nil {
				source := ProviderSourceSystem
				if configured {
					source = ProviderSourceConfigured
				}
				return candidate, source, nil
			}
		}
	}
	if executable, configured, err := r.lookupExecutable("bash.exe", configuredPaths); err == nil {
		if configured || isGitBashPath(executable) {
			source := ProviderSourceSystem
			if configured {
				source = ProviderSourceConfigured
			}
			return executable, source, nil
		}
	}
	for _, root := range r.standardGitRoots() {
		for _, candidate := range gitBashCandidates(root) {
			if r.validateExecutable(candidate) == nil {
				return candidate, ProviderSourceSystem, nil
			}
		}
	}
	return "", ProviderSourceSystem, exec.ErrNotFound
}

func (r *ProviderResolver) lookupExecutable(name string, configuredPaths []string) (string, bool, error) {
	for _, root := range configuredPaths {
		candidate := filepath.Join(root, name)
		if r.validateExecutable(candidate) == nil {
			return candidate, true, nil
		}
	}
	if r.lookPath == nil {
		return "", false, exec.ErrNotFound
	}
	executable, err := r.lookPath(name)
	if err != nil {
		return "", false, err
	}
	if err := r.validateExecutable(executable); err != nil {
		return "", false, err
	}
	return executable, false, nil
}

func (r *ProviderResolver) validateExecutable(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return exec.ErrNotFound
	}
	stat := r.stat
	if stat == nil {
		stat = os.Stat
	}
	info, err := stat(value)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("shell executable is not a regular file: %s", value)
	}
	if r.goos != "windows" && info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("shell executable is not executable: %s", value)
	}
	return nil
}

func (r *ProviderResolver) standardGitRoots() []string {
	getenv := r.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	values := []string{}
	for _, name := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if root := strings.TrimSpace(getenv(name)); root != "" {
			values = append(values, filepath.Join(root, "Git"))
		}
	}
	if root := strings.TrimSpace(getenv("LOCALAPPDATA")); root != "" {
		values = append(values, filepath.Join(root, "Programs", "Git"))
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "." || value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeProviderPaths(values []string, goos string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("shell path must be absolute: %q", value)
		}
		value = filepath.Clean(value)
		key := value
		if goos == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func gitBashCandidates(root string) []string {
	return []string{
		filepath.Join(root, "bin", "bash.exe"),
		filepath.Join(root, "usr", "bin", "bash.exe"),
	}
}

func isGitBashPath(value string) bool {
	normalized := strings.ReplaceAll(filepath.Clean(strings.TrimSpace(value)), "\\", "/")
	normalized = strings.ToLower(filepath.ToSlash(normalized))
	return strings.Contains(normalized, "/git/bin/bash.exe") || strings.Contains(normalized, "/git/usr/bin/bash.exe")
}

func providerFor(executable, language string, kind ProviderKind, source ProviderSource) Provider {
	executable = filepath.Clean(strings.TrimSpace(executable))
	path := []string{}
	if filepath.IsAbs(executable) {
		path = []string{filepath.Dir(executable)}
	}
	return Provider{Executable: executable, Language: language, Source: source, Kind: kind, Path: path}
}
