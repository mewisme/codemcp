package codegraph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	executablepath "go.mewis.me/codemcp/internal/integrations/executable"
	"go.mewis.me/codemcp/internal/integrations/managedasset"
)

type Resolution struct {
	Source     ExecutableState `json:"source"`
	Path       string          `json:"path,omitempty"`
	PrefixArgs []string        `json:"prefix_args,omitempty"`
	Verified   bool            `json:"verified"`
}

type Status struct {
	Enabled          bool          `json:"enabled"`
	Resolution       Resolution    `json:"resolution"`
	ManagedSupported bool          `json:"managed_supported"`
	ManagedInstalled bool          `json:"managed_installed"`
	Platform         string        `json:"platform"`
	PinnedVersion    string        `json:"pinned_version"`
	InstallHints     []InstallHint `json:"install_hints,omitempty"`
}

type ProbeResult struct {
	Status  Status `json:"status"`
	Path    string `json:"path"`
	Version string `json:"version_output"`
}

type InstallResult struct {
	Status           Status `json:"status"`
	Path             string `json:"path"`
	Installed        bool   `json:"installed"`
	AlreadyInstalled bool   `json:"already_installed"`
}

type CommandResult struct {
	Resolution Resolution `json:"resolution"`
	ExitCode   int        `json:"exit_code"`
	Stdout     string     `json:"stdout"`
	Stderr     string     `json:"stderr"`
}

type commandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type commandRunner func(context.Context, string, []string, int) (commandResult, error)
type specResolver func(string, string) (managedasset.TreeSpec, Asset, bool)

type Options struct {
	Enabled        bool
	ConfiguredPath string
	ManagedRoot    string
	HTTPClient     *http.Client
}

type Runtime struct {
	enabled        bool
	configuredPath string
	goos           string
	goarch         string
	lookPath       func(string) (string, error)
	run            commandRunner
	managedRoot    string
	httpClient     *http.Client
	specFor        specResolver
}

func New(options Options) *Runtime {
	root := strings.TrimSpace(options.ManagedRoot)
	if root == "" {
		root = DefaultManagedRoot()
	}
	return &Runtime{
		enabled: options.Enabled, configuredPath: strings.TrimSpace(options.ConfiguredPath),
		goos: runtime.GOOS, goarch: runtime.GOARCH, lookPath: exec.LookPath, run: runCommand,
		managedRoot: root, httpClient: options.HTTPClient, specFor: ManagedSpecFor,
	}
}

func DefaultManagedRoot() string {
	root, _ := ManagedRoot(configformat.RootPath())
	return root
}

func ManagedSpecFor(goos, goarch string) (managedasset.TreeSpec, Asset, bool) {
	asset, ok := AssetFor(goos, goarch)
	if !ok {
		return managedasset.TreeSpec{}, Asset{}, false
	}
	return managedasset.TreeSpec{
		Name: "codegraph", Version: Version, Platform: strings.TrimSpace(goos) + "-" + strings.TrimSpace(goarch),
		URL: asset.URL, SHA256: asset.SHA256, Archive: asset.Archive, Entrypoint: asset.Entrypoint,
		Required: append([]string(nil), asset.Required...),
	}, asset, true
}

func (r *Runtime) Resolve() (Resolution, error) {
	if r == nil || !r.enabled {
		return Resolution{Source: ExecutableDisabled}, nil
	}
	if r.configuredPath != "" {
		path, err := executablepath.ResolveExternal(r.configuredPath, r.goos)
		if err != nil {
			return Resolution{Source: ExecutableUnavailable}, fmt.Errorf("configured CodeGraph executable: %w", err)
		}
		return Resolution{Source: ExecutableConfigured, Path: path, Verified: true}, nil
	}
	if path, err := r.lookPath(SystemExecutable()); err == nil && strings.TrimSpace(path) != "" {
		if resolved, resolveErr := executablepath.ResolveExternal(path, r.goos); resolveErr == nil {
			return Resolution{Source: ExecutableSystem, Path: resolved, Verified: true}, nil
		}
	}
	spec, asset, ok := r.specFor(r.goos, r.goarch)
	if !ok || strings.TrimSpace(r.managedRoot) == "" {
		return Resolution{Source: ExecutableUnavailable}, nil
	}
	path, err := (managedasset.Manager{Root: r.managedRoot}).ValidateTree(spec)
	if err != nil {
		return Resolution{Source: ExecutableUnavailable}, nil
	}
	return Resolution{Source: ExecutableManaged, Path: path, PrefixArgs: append([]string(nil), asset.PrefixArgs...), Verified: true}, nil
}

func (r *Runtime) Status() (Status, error) {
	status := Status{Enabled: r != nil && r.enabled, PinnedVersion: Version}
	if r == nil {
		status.Resolution = Resolution{Source: ExecutableDisabled}
		return status, nil
	}
	status.Platform = r.goos + "/" + r.goarch
	spec, _, supported := r.specFor(r.goos, r.goarch)
	status.ManagedSupported = supported
	status.InstallHints = SystemInstallHints(r.goos)
	if supported && strings.TrimSpace(r.managedRoot) != "" {
		if _, err := (managedasset.Manager{Root: r.managedRoot}).ValidateTree(spec); err == nil {
			status.ManagedInstalled = true
		}
	}
	resolution, err := r.Resolve()
	if err != nil {
		status.Resolution = Resolution{Source: ExecutableUnavailable}
		return status, err
	}
	status.Resolution = resolution
	return status, nil
}

func (r *Runtime) Probe(ctx context.Context) (ProbeResult, error) {
	status, err := r.Status()
	if err != nil {
		return ProbeResult{Status: status}, err
	}
	if status.Resolution.Source == ExecutableDisabled {
		return ProbeResult{Status: status}, errors.New("codegraph integration is disabled")
	}
	if status.Resolution.Source == ExecutableUnavailable || status.Resolution.Path == "" {
		return ProbeResult{Status: status}, fmt.Errorf("codegraph executable is unavailable on %s", status.Platform)
	}
	runCtx, cancel := context.WithTimeout(nonNilContext(ctx), ProbeTimeout)
	defer cancel()
	args := append(append([]string(nil), status.Resolution.PrefixArgs...), VersionProbeArgs()...)
	result, runErr := r.run(runCtx, status.Resolution.Path, args, ProbeOutputLimit)
	if runCtx.Err() != nil {
		return ProbeResult{Status: status}, runCtx.Err()
	}
	if runErr != nil {
		return ProbeResult{Status: status}, runErr
	}
	if result.ExitCode != 0 {
		return ProbeResult{Status: status}, commandFailure(result)
	}
	version := strings.TrimSpace(result.Stdout)
	if version == "" {
		version = strings.TrimSpace(result.Stderr)
	}
	if version == "" {
		return ProbeResult{Status: status}, errors.New("codegraph version probe returned no output")
	}
	return ProbeResult{Status: status, Path: status.Resolution.Path, Version: version}, nil
}

func (r *Runtime) Install(ctx context.Context) (InstallResult, error) {
	if r == nil {
		return InstallResult{}, errors.New("codegraph runtime is unavailable")
	}
	spec, _, ok := r.specFor(r.goos, r.goarch)
	if !ok {
		return InstallResult{}, fmt.Errorf("codegraph managed asset is unsupported on %s/%s", r.goos, r.goarch)
	}
	manager := managedasset.Manager{Root: r.managedRoot, HTTPClient: r.httpClient}
	if path, err := manager.ValidateTree(spec); err == nil {
		status, statusErr := r.Status()
		return InstallResult{Status: status, Path: path, AlreadyInstalled: true}, statusErr
	}
	path, err := manager.InstallTree(nonNilContext(ctx), spec)
	if err != nil {
		return InstallResult{}, err
	}
	status, err := r.Status()
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Status: status, Path: path, Installed: true}, nil
}

func (r *Runtime) ExecuteInDir(ctx context.Context, directory string, args []string, timeout time.Duration, limit int) (CommandResult, error) {
	if r == nil || !r.enabled {
		return CommandResult{}, errors.New("codegraph integration is disabled")
	}
	directory = strings.TrimSpace(directory)
	if directory == "" || !filepath.IsAbs(directory) {
		return CommandResult{}, errors.New("codegraph command directory must be an absolute workspace path")
	}
	info, err := os.Stat(directory)
	if err != nil {
		return CommandResult{}, fmt.Errorf("codegraph command directory: %w", err)
	}
	if !info.IsDir() {
		return CommandResult{}, errors.New("codegraph command directory must be a directory")
	}
	if timeout <= 0 {
		return CommandResult{}, errors.New("codegraph command timeout must be positive")
	}
	if limit <= 0 || limit > MaxOutputBytes {
		return CommandResult{}, fmt.Errorf("codegraph output limit must be between 1 and %d bytes", MaxOutputBytes)
	}
	resolution, err := r.Resolve()
	if err != nil {
		return CommandResult{}, err
	}
	if resolution.Source == ExecutableUnavailable {
		return CommandResult{Resolution: resolution}, fmt.Errorf("codegraph executable is unavailable on %s/%s", r.goos, r.goarch)
	}
	runCtx, cancel := context.WithTimeout(nonNilContext(ctx), timeout)
	defer cancel()
	commandArgs := append(append([]string(nil), resolution.PrefixArgs...), args...)
	result, runErr := runCommandInDir(runCtx, resolution.Path, commandArgs, directory, limit)
	command := CommandResult{Resolution: resolution, ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr}
	if runCtx.Err() != nil {
		return command, runCtx.Err()
	}
	if runErr != nil {
		return command, runErr
	}
	if result.ExitCode != 0 {
		return command, commandFailure(result)
	}
	return command, nil
}

func runCommand(ctx context.Context, path string, args []string, limit int) (commandResult, error) {
	return runCommandInDir(ctx, path, args, "", limit)
}

func runCommandInDir(ctx context.Context, path string, args []string, directory string, limit int) (commandResult, error) {
	if limit <= 0 {
		return commandResult{}, errors.New("codegraph output limit must be positive")
	}
	cmd := exec.CommandContext(nonNilContext(ctx), path, args...)
	cmd.Env = safeEnvironment()
	if strings.TrimSpace(directory) != "" {
		cmd.Dir = directory
	}
	configureCommandCancellation(cmd)
	stdout, stderr := &limitedBuffer{limit: limit}, &limitedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	result := commandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if stdout.exceeded || stderr.exceeded {
		return result, fmt.Errorf("codegraph output exceeds %d-byte limit", limit)
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (w *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := w.limit + 1 - w.buf.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = w.buf.Write(data)
	}
	if w.buf.Len() > w.limit || len(data) < original {
		w.exceeded = true
	}
	return original, nil
}

func (w *limitedBuffer) String() string { return w.buf.String() }

func commandFailure(result commandResult) error {
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = strings.TrimSpace(result.Stdout)
	}
	if message == "" {
		message = "no diagnostic output"
	}
	return fmt.Errorf("codegraph exited with code %d: %s", result.ExitCode, message)
}

func safeEnvironment() []string {
	allowed := map[string]struct{}{"home": {}, "lang": {}, "path": {}, "systemroot": {}, "temp": {}, "tmp": {}, "userprofile": {}, "windir": {}}
	result := make([]string, 0, len(allowed))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, keep := allowed[strings.ToLower(key)]; keep {
			result = append(result, entry)
		}
	}
	sort.Strings(result)
	return result
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
