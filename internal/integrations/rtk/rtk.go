package rtk

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
)

const (
	Version        = "v0.49.0"
	probeTimeout   = 2 * time.Second
	maxOutputBytes = 16 << 10
)

type Source string

const (
	SourceDisabled    Source = "disabled"
	SourceConfigured  Source = "configured"
	SourceSystem      Source = "system"
	SourceManaged     Source = "managed"
	SourceUnavailable Source = "unavailable"
)

type InstallHint struct {
	Label      string   `json:"label"`
	Command    string   `json:"command"`
	Executable string   `json:"executable,omitempty"`
	Args       []string `json:"args,omitempty"`
}

type Signature struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Identity string `json:"identity,omitempty"`
}

type Portable struct {
	URL        string     `json:"url"`
	SHA256     string     `json:"sha256"`
	Archive    string     `json:"archive"`
	Entrypoint string     `json:"entrypoint"`
	Signature  *Signature `json:"signature,omitempty"`
}

type Platform struct {
	Executable string        `json:"executable"`
	Install    []InstallHint `json:"install"`
	Portable   Portable      `json:"portable"`
}

type Resolution struct {
	Source   Source `json:"source"`
	Path     string `json:"path,omitempty"`
	Verified bool   `json:"verified"`
}

type RewriteResult struct {
	Requested  string `json:"requested"`
	Effective  string `json:"effective"`
	Executable string `json:"executable,omitempty"`
	Rewritten  bool   `json:"rewritten"`
}

type Status struct {
	Enabled          bool          `json:"enabled"`
	Source           Source        `json:"source"`
	Path             string        `json:"path,omitempty"`
	Version          string        `json:"version"`
	Platform         string        `json:"platform"`
	ManagedSupported bool          `json:"managed_supported"`
	ManagedInstalled bool          `json:"managed_installed"`
	Verified         bool          `json:"verified"`
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

type runResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type runner func(context.Context, string, ...string) (runResult, error)

type SignatureVerifier func(context.Context, string, Signature, *http.Client) error

type Options struct {
	Enabled           bool
	ConfiguredPath    string
	ManagedRoot       string
	HTTPClient        *http.Client
	SignatureVerifier SignatureVerifier
}

type Manager struct {
	enabled           bool
	configuredPath    string
	managedRoot       string
	httpClient        *http.Client
	signatureVerifier SignatureVerifier
	goos              string
	goarch            string
	lookPath          func(string) (string, error)
	run               runner
}

var platforms = map[string]Platform{
	"linux/amd64": {
		Executable: "rtk",
		Install:    unixInstallHints(),
		Portable:   Portable{URL: "https://github.com/rtk-ai/rtk/releases/download/v0.49.0/rtk-x86_64-unknown-linux-musl.tar.gz", SHA256: "7278231dfd7e6a730a4ab7f847b195bcf02289c2d57622b0dab75a6411100c8f", Archive: "tar.gz", Entrypoint: "rtk"},
	},
	"linux/arm64": {
		Executable: "rtk",
		Install:    unixInstallHints(),
		Portable:   Portable{URL: "https://github.com/rtk-ai/rtk/releases/download/v0.49.0/rtk-aarch64-unknown-linux-gnu.tar.gz", SHA256: "c8ea4b6560841e73157c134fd4a3293914c6ede42e786ee985cf491fde691ba7", Archive: "tar.gz", Entrypoint: "rtk"},
	},
	"darwin/amd64": {
		Executable: "rtk",
		Install:    unixInstallHints(),
		Portable:   Portable{URL: "https://github.com/rtk-ai/rtk/releases/download/v0.49.0/rtk-x86_64-apple-darwin.tar.gz", SHA256: "d297388f4a8a786e79abe5f55b80451725bfe8c5835b4736c05d7cff4d68f627", Archive: "tar.gz", Entrypoint: "rtk"},
	},
	"darwin/arm64": {
		Executable: "rtk",
		Install:    unixInstallHints(),
		Portable:   Portable{URL: "https://github.com/rtk-ai/rtk/releases/download/v0.49.0/rtk-aarch64-apple-darwin.tar.gz", SHA256: "bbbfebabb22686993a80da731aa4d5d35116fb8ae24abb00608efa028e13ae01", Archive: "tar.gz", Entrypoint: "rtk"},
	},
	"windows/amd64": {
		Executable: "rtk.exe",
		Install: []InstallHint{
			{Label: "winget", Command: "winget install rtk-ai.rtk", Executable: "winget", Args: []string{"install", "rtk-ai.rtk"}},
			cargoInstallHint(),
		},
		Portable: Portable{URL: "https://github.com/rtk-ai/rtk/releases/download/v0.49.0/rtk-x86_64-pc-windows-msvc.zip", SHA256: "cb971046598f0e8bd51f6c27780fcdd2c39a4c459a811bd95b0d77ba8c0d7c9f", Archive: "zip", Entrypoint: "rtk.exe"},
	},
}

func New(options Options) *Manager {
	root := strings.TrimSpace(options.ManagedRoot)
	if root == "" {
		root = DefaultManagedRoot()
	}
	return &Manager{
		enabled: options.Enabled, configuredPath: strings.TrimSpace(options.ConfiguredPath), managedRoot: root,
		httpClient: options.HTTPClient, signatureVerifier: options.SignatureVerifier,
		goos: runtime.GOOS, goarch: runtime.GOARCH, lookPath: exec.LookPath, run: runCommand,
	}
}

func DefaultManagedRoot() string {
	return filepath.Join(configformat.RootPath(), "managed-assets")
}

func PlatformFor(goos, goarch string) (Platform, bool) {
	value, ok := platforms[strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch)]
	if !ok {
		return Platform{}, false
	}
	value.Install = cloneInstallHints(value.Install)
	if value.Portable.Signature != nil {
		signature := *value.Portable.Signature
		value.Portable.Signature = &signature
	}
	return value, true
}

func CurrentPlatform() (Platform, bool) {
	return PlatformFor(runtime.GOOS, runtime.GOARCH)
}

func (m *Manager) Resolve() (Resolution, error) {
	if m == nil || !m.enabled {
		return Resolution{Source: SourceDisabled}, nil
	}
	platform, ok := PlatformFor(m.goos, m.goarch)
	if !ok {
		return Resolution{Source: SourceUnavailable}, nil
	}
	if m.configuredPath != "" {
		path, err := filepath.Abs(m.configuredPath)
		if err != nil {
			return Resolution{Source: SourceUnavailable}, fmt.Errorf("resolve configured RTK path: %w", err)
		}
		path = filepath.Clean(path)
		if err := validateExecutable(path); err != nil {
			return Resolution{Source: SourceUnavailable}, fmt.Errorf("configured RTK executable: %w", err)
		}
		return Resolution{Source: SourceConfigured, Path: path, Verified: true}, nil
	}
	if path, err := m.lookPath(platform.Executable); err == nil && strings.TrimSpace(path) != "" {
		if absolute, absErr := filepath.Abs(path); absErr == nil {
			path = filepath.Clean(absolute)
		}
		if err := validateExecutable(path); err == nil {
			return Resolution{Source: SourceSystem, Path: path, Verified: true}, nil
		}
	}
	path, err := m.validateManaged(platform)
	if err == nil {
		return Resolution{Source: SourceManaged, Path: path, Verified: true}, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Resolution{Source: SourceUnavailable}, nil
	}
	return Resolution{Source: SourceUnavailable}, err
}

func (m *Manager) Status() (Status, error) {
	status := Status{Enabled: m != nil && m.enabled, Version: Version}
	if m == nil {
		status.Source = SourceDisabled
		return status, nil
	}
	status.Platform = m.goos + "/" + m.goarch
	platform, supported := PlatformFor(m.goos, m.goarch)
	status.ManagedSupported = supported
	if supported {
		status.InstallHints = cloneInstallHints(platform.Install)
		if _, err := m.validateManaged(platform); err == nil {
			status.ManagedInstalled = true
		}
	}
	resolution, err := m.Resolve()
	if err != nil {
		status.Source = SourceUnavailable
		return status, err
	}
	status.Source, status.Path, status.Verified = resolution.Source, resolution.Path, resolution.Verified
	return status, nil
}

func (m *Manager) Rewrite(ctx context.Context, command string) (RewriteResult, error) {
	requested := strings.TrimSpace(command)
	result := RewriteResult{Requested: requested, Effective: requested}
	if requested == "" || m == nil || !m.enabled {
		return result, nil
	}
	resolution, err := m.Resolve()
	if err != nil {
		return result, err
	}
	if resolution.Source == SourceUnavailable || resolution.Source == SourceDisabled || resolution.Path == "" {
		return result, nil
	}
	runCtx, cancel := context.WithTimeout(nonNilContext(ctx), probeTimeout)
	run, runErr := m.run(runCtx, resolution.Path, "rewrite", requested)
	cancel()
	if runErr != nil {
		return result, fmt.Errorf("rewrite command with RTK: %w", runErr)
	}
	switch run.ExitCode {
	case 1, 2:
		return result, nil
	case 0, 3:
	default:
		return result, fmt.Errorf("rewrite command with RTK: %w", commandError(run))
	}
	effective := strings.TrimSpace(run.Stdout)
	if effective == requested {
		return result, nil
	}
	name := executableName(resolution.Path)
	if effective == "" || !strings.HasPrefix(strings.ToLower(effective), strings.ToLower(name)+" ") {
		return result, fmt.Errorf("rtk rewrite is not routed through %s: %q", name, effective)
	}
	result.Effective = effective
	result.Executable = resolution.Path
	result.Rewritten = true
	return result, nil
}

func (m *Manager) Probe(ctx context.Context) (ProbeResult, error) {
	status, err := m.Status()
	if err != nil {
		return ProbeResult{Status: status}, err
	}
	if status.Source == SourceDisabled {
		return ProbeResult{Status: status}, errors.New("rtk integration is disabled")
	}
	if status.Source == SourceUnavailable || status.Path == "" {
		return ProbeResult{Status: status}, fmt.Errorf("rtk executable is unavailable on %s", status.Platform)
	}
	runCtx, cancel := context.WithTimeout(nonNilContext(ctx), probeTimeout)
	result, runErr := m.run(runCtx, status.Path, "--version")
	cancel()
	if runErr != nil {
		return ProbeResult{Status: status}, runErr
	}
	if result.ExitCode != 0 {
		return ProbeResult{Status: status}, commandError(result)
	}
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return ProbeResult{Status: status}, errors.New("rtk version probe returned empty output")
	}
	return ProbeResult{Status: status, Path: status.Path, Version: output}, nil
}

func (m *Manager) Install(ctx context.Context) (InstallResult, error) {
	if m == nil {
		return InstallResult{}, errors.New("rtk integration manager is unavailable")
	}
	platform, ok := PlatformFor(m.goos, m.goarch)
	if !ok {
		return InstallResult{}, fmt.Errorf("rtk managed asset is unsupported on %s/%s", m.goos, m.goarch)
	}
	if path, err := m.validateManaged(platform); err == nil {
		status, statusErr := m.Status()
		return InstallResult{Status: status, Path: path, AlreadyInstalled: true}, statusErr
	}
	path, err := m.installManaged(nonNilContext(ctx), platform)
	if err != nil {
		return InstallResult{}, err
	}
	status, err := m.Status()
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Status: status, Path: path, Installed: true}, nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func runCommand(ctx context.Context, path string, args ...string) (runResult, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = safeEnvironment()
	stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return runResult{}, ctx.Err()
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return runResult{}, err
		}
		exitCode = exitErr.ExitCode()
	}
	if stdout.exceeded || stderr.exceeded {
		return runResult{}, fmt.Errorf("rtk output exceeds %d-byte limit", maxOutputBytes)
	}
	return runResult{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := maxOutputBytes + 1 - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	}
	if b.buffer.Len() > maxOutputBytes || len(data) < original {
		b.exceeded = true
	}
	return original, nil
}

func (b *boundedBuffer) String() string { return b.buffer.String() }

func commandError(result runResult) error {
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return errors.New(message)
}

func executableName(path string) string {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
	return strings.TrimSuffix(name, ".exe")
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

func unixInstallHints() []InstallHint {
	return []InstallHint{
		{Label: "Homebrew", Command: "brew install rtk-ai/tap/rtk", Executable: "brew", Args: []string{"install", "rtk-ai/tap/rtk"}},
		{Label: "Quick install", Command: "curl -fsSL https://raw.githubusercontent.com/rtk-ai/rtk/master/install.sh | sh"},
		cargoInstallHint(),
	}
}

func cargoInstallHint() InstallHint {
	return InstallHint{Label: "Cargo", Command: "cargo install --git https://github.com/rtk-ai/rtk --branch master rtk", Executable: "cargo", Args: []string{"install", "--git", "https://github.com/rtk-ai/rtk", "--branch", "master", "rtk"}}
}

func cloneInstallHints(values []InstallHint) []InstallHint {
	result := make([]InstallHint, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Args = append([]string(nil), value.Args...)
	}
	return result
}
