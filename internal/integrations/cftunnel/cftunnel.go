package cftunnel

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
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/configformat"
	executablepath "go.mewis.me/codemcp/internal/integrations/executable"
	"go.mewis.me/codemcp/internal/integrations/managedasset"
)

const (
	Repository    = "mewisme/cf-tunnel"
	ChecksumAsset = "checksums.txt"
	probeTimeout  = 5 * time.Second
	maxOutput     = 64 << 10
	assetName     = "cf-tunnel"
)

type Source string

const (
	SourceSystem      Source = "system"
	SourceManaged     Source = "managed"
	SourceUnavailable Source = "unavailable"
)

type Platform struct {
	Target     string
	Archive    string
	Entrypoint string
}

type Resolution struct {
	Source   Source `json:"source"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Verified bool   `json:"verified"`
}

type Status struct {
	Platform         string `json:"platform"`
	Source           Source `json:"source"`
	Path             string `json:"path,omitempty"`
	Version          string `json:"version,omitempty"`
	Verified         bool   `json:"verified"`
	ManagedSupported bool   `json:"managed_supported"`
	ManagedInstalled bool   `json:"managed_installed"`
	Consumer         string `json:"consumer"`
}

type ProbeResult struct {
	Status  Status `json:"status"`
	Version string `json:"version,omitempty"`
}

type InstallResult struct {
	Status           Status `json:"status"`
	Path             string `json:"path,omitempty"`
	Version          string `json:"version,omitempty"`
	Installed        bool   `json:"installed"`
	AlreadyInstalled bool   `json:"already_installed"`
}

type RemoveResult struct {
	Status  Status `json:"status"`
	Removed bool   `json:"removed"`
}

type Options struct {
	ManagedRoot string
	HTTPClient  *http.Client
}

type Manager struct {
	managedRoot string
	httpClient  *http.Client
	goos        string
	goarch      string
	lookPath    func(string) (string, error)
	run         func(context.Context, string, ...string) (string, error)
}

var platforms = map[string]Platform{
	"darwin/amd64":  {Target: "darwin_amd64", Archive: "tar.gz", Entrypoint: "cf-tunnel"},
	"darwin/arm64":  {Target: "darwin_arm64", Archive: "tar.gz", Entrypoint: "cf-tunnel"},
	"linux/amd64":   {Target: "linux_amd64", Archive: "tar.gz", Entrypoint: "cf-tunnel"},
	"linux/arm64":   {Target: "linux_arm64", Archive: "tar.gz", Entrypoint: "cf-tunnel"},
	"windows/amd64": {Target: "windows_amd64", Archive: "zip", Entrypoint: "cf-tunnel.exe"},
	"windows/arm64": {Target: "windows_arm64", Archive: "zip", Entrypoint: "cf-tunnel.exe"},
}

func New(options Options) *Manager {
	root := strings.TrimSpace(options.ManagedRoot)
	if root == "" {
		root = DefaultManagedRoot()
	}
	return &Manager{
		managedRoot: root,
		httpClient:  options.HTTPClient,
		goos:        runtime.GOOS,
		goarch:      runtime.GOARCH,
		lookPath:    exec.LookPath,
		run:         runVersion,
	}
}

func DefaultManagedRoot() string {
	return filepath.Join(configformat.RootPath(), "managed-assets")
}

func PlatformFor(goos, goarch string) (Platform, bool) {
	value, ok := platforms[strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch)]
	return value, ok
}

func (m *Manager) Resolve() (Resolution, error) {
	if m == nil {
		return Resolution{Source: SourceUnavailable}, nil
	}
	if path, err := m.lookPath(systemExecutable(m.goos)); err == nil && strings.TrimSpace(path) != "" {
		resolved, resolveErr := executablepath.ResolveExternal(path, m.goos)
		if resolveErr == nil {
			return Resolution{Source: SourceSystem, Path: resolved, Verified: true}, nil
		}
	}
	if _, supported := PlatformFor(m.goos, m.goarch); !supported {
		return Resolution{Source: SourceUnavailable}, nil
	}
	installed, err := (managedasset.Manager{Root: m.managedRoot}).LatestInstalled(assetName, m.managedPlatform())
	if err == nil {
		return Resolution{Source: SourceManaged, Path: installed.Path, Version: installed.Version, Verified: true}, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Resolution{Source: SourceUnavailable}, nil
	}
	return Resolution{Source: SourceUnavailable}, err
}

func (m *Manager) Status() (Status, error) {
	status := Status{Platform: m.platform(), Consumer: "Telegram Logs Mini App"}
	if m == nil {
		status.Source = SourceUnavailable
		return status, nil
	}
	_, status.ManagedSupported = PlatformFor(m.goos, m.goarch)
	if status.ManagedSupported {
		if _, err := (managedasset.Manager{Root: m.managedRoot}).LatestInstalled(assetName, m.managedPlatform()); err == nil {
			status.ManagedInstalled = true
		}
	}
	resolution, err := m.Resolve()
	if err != nil {
		status.Source = SourceUnavailable
		return status, err
	}
	status.Source, status.Path, status.Version, status.Verified = resolution.Source, resolution.Path, resolution.Version, resolution.Verified
	return status, nil
}

func (m *Manager) Probe(ctx context.Context) (ProbeResult, error) {
	status, err := m.Status()
	if err != nil {
		return ProbeResult{Status: status}, err
	}
	if status.Source == SourceUnavailable || strings.TrimSpace(status.Path) == "" {
		return ProbeResult{Status: status}, fmt.Errorf("cf-tunnel executable is unavailable on %s", status.Platform)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	version, err := m.run(runCtx, status.Path, "--version")
	if err != nil {
		return ProbeResult{Status: status}, err
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return ProbeResult{Status: status}, errors.New("cf-tunnel version probe returned empty output")
	}
	return ProbeResult{Status: status, Version: version}, nil
}

func (m *Manager) Install(ctx context.Context) (InstallResult, error) {
	return m.installLatest(ctx, false)
}

func (m *Manager) Update(ctx context.Context) (InstallResult, error) {
	return m.installLatest(ctx, true)
}

func (m *Manager) installLatest(ctx context.Context, update bool) (InstallResult, error) {
	if m == nil {
		return InstallResult{}, errors.New("cf-tunnel integration manager is unavailable")
	}
	platform, ok := PlatformFor(m.goos, m.goarch)
	if !ok {
		return InstallResult{}, fmt.Errorf("cf-tunnel managed asset is unsupported on %s", m.platform())
	}
	if ctx == nil {
		ctx = context.Background()
	}
	spec, err := m.latestSpec(ctx, platform)
	if err != nil {
		return InstallResult{}, err
	}
	manager := managedasset.Manager{Root: m.managedRoot, HTTPClient: m.httpClient}
	if path, validateErr := manager.Validate(spec); validateErr == nil {
		status, statusErr := m.Status()
		return InstallResult{Status: status, Path: path, Version: spec.Version, AlreadyInstalled: true}, statusErr
	}
	path, err := manager.Install(ctx, spec)
	if err != nil {
		return InstallResult{}, err
	}
	if update {
		if err := manager.RemoveOtherVersions(assetName, spec.Version); err != nil {
			return InstallResult{}, err
		}
	}
	status, err := m.Status()
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Status: status, Path: path, Version: spec.Version, Installed: true}, nil
}

func (m *Manager) Remove() (RemoveResult, error) {
	if m == nil {
		return RemoveResult{}, errors.New("cf-tunnel integration manager is unavailable")
	}
	removed, err := (managedasset.Manager{Root: m.managedRoot}).RemoveAll(assetName)
	if err != nil {
		return RemoveResult{}, err
	}
	status, statusErr := m.Status()
	return RemoveResult{Status: status, Removed: removed}, statusErr
}

func (m *Manager) ResolvePath() (string, error) {
	resolution, err := m.Resolve()
	if err != nil {
		return "", err
	}
	if resolution.Source == SourceUnavailable || strings.TrimSpace(resolution.Path) == "" {
		return "", fmt.Errorf("cf-tunnel executable is unavailable on %s: %w", m.platform(), exec.ErrNotFound)
	}
	return resolution.Path, nil
}

func (m *Manager) latestSpec(ctx context.Context, platform Platform) (managedasset.Spec, error) {
	release, err := managedasset.LatestGitHubRelease(ctx, m.httpClient, Repository, ChecksumAsset)
	if err != nil {
		return managedasset.Spec{}, err
	}
	version := strings.TrimSpace(release.Version)
	filename := "cf-tunnel_" + strings.TrimPrefix(version, "v") + "_" + platform.Target + "." + platform.Archive
	assetURL := strings.TrimSpace(release.Assets[filename])
	checksum := strings.TrimSpace(release.Checksums[filename])
	if assetURL == "" {
		return managedasset.Spec{}, fmt.Errorf("latest cf-tunnel release %s is missing asset %q", version, filename)
	}
	if checksum == "" {
		return managedasset.Spec{}, fmt.Errorf("latest cf-tunnel release %s checksum file is missing %q", version, filename)
	}
	return managedasset.Spec{
		Name: assetName, Version: version, Platform: m.managedPlatform(),
		URL: assetURL, SHA256: checksum, Archive: platform.Archive, Entrypoint: platform.Entrypoint,
	}, nil
}

func (m *Manager) platform() string {
	if m == nil {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return m.goos + "/" + m.goarch
}

func (m *Manager) managedPlatform() string {
	if m == nil {
		return runtime.GOOS + "-" + runtime.GOARCH
	}
	return m.goos + "-" + m.goarch
}

func systemExecutable(goos string) string {
	if goos == "windows" {
		return "cf-tunnel.exe"
	}
	return "cf-tunnel"
}

func runVersion(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return "", fmt.Errorf("cf-tunnel probe output exceeds %d-byte limit", maxOutput)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("probe cf-tunnel: %s", detail)
	}
	return stdout.String(), nil
}

type boundedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := maxOutput + 1 - b.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.Buffer.Write(data)
	}
	if b.Len() > maxOutput || len(data) < original {
		b.exceeded = true
	}
	return original, nil
}
