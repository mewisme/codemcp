package browser

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	Enabled        bool
	ConfiguredPath string
	StateRoot      string
	Runtime        Runtime
	// Passive is retained for source compatibility. Detection is always passive;
	// use Verify when an explicit launch/CDP diagnostic is required.
	Passive bool
}

type Runtime struct {
	GOOS           string
	Env            func(string) string
	KernelRelease  func() string
	LookPath       func(string) (string, error)
	Exists         func(string) bool
	WindowsEnv     func(context.Context, string) (string, error)
	WindowsToLocal func(context.Context, string) (string, error)
	Probe          func(context.Context, Candidate) ProbeResult
}

func Detect(ctx context.Context, options Options) Capability {
	if ctx == nil {
		ctx = context.Background()
	}
	if !options.Enabled {
		return Capability{State: StateDisabled, Enabled: false, Reason: "browser integration disabled by configuration"}
	}
	runtime := normalizedRuntime(options.Runtime)
	configured := strings.TrimSpace(options.ConfiguredPath)
	if configured != "" {
		candidate, err := configuredCandidate(ctx, runtime, configured)
		if err != nil {
			return unavailable(true, err.Error())
		}
		return discoverCapability(options.StateRoot, runtime, candidate)
	}

	var lastFailure Capability
	for _, candidate := range nativeCandidates(runtime) {
		if !runtime.Exists(candidate.LocalExecutable) {
			continue
		}
		capability := discoverCapability(options.StateRoot, runtime, candidate)
		if capability.State == StateAvailable {
			return capability
		}
		if strings.TrimSpace(capability.Reason) != "" {
			lastFailure = capability
		}
	}
	if isWSL(runtime) {
		for _, candidate := range windowsHostCandidates(ctx, runtime) {
			if candidate.LocalExecutable == "" || !runtime.Exists(candidate.LocalExecutable) {
				continue
			}
			capability := discoverCapability(options.StateRoot, runtime, candidate)
			if capability.State == StateAvailable {
				return capability
			}
			if strings.TrimSpace(capability.Reason) != "" {
				lastFailure = capability
			}
		}
	}
	if strings.TrimSpace(lastFailure.Reason) != "" {
		return lastFailure
	}
	return unavailable(true, "no launchable Chrome, Chromium, or Edge browser was detected")
}

func discoverCapability(root string, runtime Runtime, candidate Candidate) Capability {
	graphical := graphicalAvailable(runtime, candidate)
	if !graphical {
		return Capability{
			State: StateUnavailable, Enabled: true, Family: candidate.Family,
			Executable: candidate.Executable, HostPlatform: candidate.HostPlatform,
			Transport: candidate.Transport, Graphical: false,
			Reason: "no graphical browser session is available",
		}
	}
	profile, err := ResolveProfile(ProfileOptions{StateRoot: root, Candidate: candidate})
	if err != nil {
		return unavailable(true, err.Error())
	}
	return Capability{
		State: StateAvailable, Enabled: true, Available: true, Launchable: true, Usable: false,
		Family: candidate.Family, Executable: candidate.Executable,
		HostPlatform: candidate.HostPlatform, Transport: candidate.Transport, Graphical: graphical,
		ProfileHostPlatform: profile.HostPlatform, Profile: &profile, Candidate: &candidate,
	}
}

// Verify performs an explicit isolated browser launch/CDP diagnostic for an
// already-discovered capability. Normal detection, status, and readiness paths
// must not call this function.
func Verify(ctx context.Context, capability Capability) ProbeResult {
	return verifyCapability(ctx, capability, Runtime{})
}

func verifyCapability(ctx context.Context, capability Capability, runtime Runtime) ProbeResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if capability.State != StateAvailable || !capability.Available || !capability.Launchable {
		reason := strings.TrimSpace(capability.Reason)
		if reason == "" {
			reason = "browser capability is not launchable"
		}
		return ProbeResult{Graphical: capability.Graphical, Family: capability.Family, Reason: boundedReason(reason)}
	}
	if capability.Candidate == nil {
		return ProbeResult{Graphical: capability.Graphical, Family: capability.Family, Reason: "browser capability has no selected candidate"}
	}
	runtime = normalizedRuntime(runtime)
	result := runtime.Probe(ctx, *capability.Candidate)
	if result.Family == "" {
		result.Family = capability.Family
	}
	result.Reason = boundedReason(result.Reason)
	return result
}

func configuredCandidate(ctx context.Context, runtime Runtime, configured string) (Candidate, error) {
	if runtime.GOOS == "linux" && isWSL(runtime) && looksWindowsPath(configured) {
		local, err := runtime.WindowsToLocal(ctx, configured)
		if err != nil {
			return Candidate{}, fmt.Errorf("configured Windows browser executable cannot be mapped into WSL: %w", err)
		}
		family := familyForExecutable(configured)
		localAppDataHost, localAppDataLocal, err := windowsLocalAppData(ctx, runtime)
		if err != nil {
			return Candidate{}, err
		}
		if !runtime.Exists(local) {
			return Candidate{}, fmt.Errorf("configured browser executable does not exist: %q", configured)
		}
		return Candidate{Family: family, Executable: configured, LocalExecutable: local, HostPlatform: "windows", Transport: TransportWSLHost, Source: SourceConfigured, HostLocalAppData: localAppDataHost, LocalAppData: localAppDataLocal}, nil
	}
	if !filepath.IsAbs(configured) {
		return Candidate{}, fmt.Errorf("configured browser executable must be absolute: %q", configured)
	}
	if !runtime.Exists(configured) {
		return Candidate{}, fmt.Errorf("configured browser executable does not exist: %q", configured)
	}
	family := familyForExecutable(configured)
	return Candidate{Family: family, Executable: configured, LocalExecutable: configured, HostPlatform: runtime.GOOS, Transport: TransportNative, Source: SourceConfigured}, nil
}

func nativeCandidates(runtime Runtime) []Candidate {
	goos := runtime.GOOS
	result := []Candidate{}
	add := func(family Family, path string, source Source) {
		if strings.TrimSpace(path) == "" {
			return
		}
		result = append(result, Candidate{Family: family, Executable: path, LocalExecutable: path, HostPlatform: goos, Transport: TransportNative, Source: source})
	}
	switch goos {
	case "linux":
		for _, item := range []struct {
			name   string
			family Family
		}{
			{"google-chrome", FamilyChrome}, {"google-chrome-stable", FamilyChrome},
			{"chromium", FamilyChromium}, {"chromium-browser", FamilyChromium},
			{"microsoft-edge", FamilyEdge}, {"microsoft-edge-stable", FamilyEdge},
		} {
			if path, err := runtime.LookPath(item.name); err == nil {
				add(item.family, path, SourcePath)
			}
		}
	case "darwin":
		home := strings.TrimSpace(runtime.Env("HOME"))
		for _, item := range []struct {
			family Family
			path   string
		}{
			{FamilyChrome, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
			{FamilyChromium, "/Applications/Chromium.app/Contents/MacOS/Chromium"},
			{FamilyEdge, "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
			{FamilyChrome, filepath.Join(home, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome")},
			{FamilyChromium, filepath.Join(home, "Applications", "Chromium.app", "Contents", "MacOS", "Chromium")},
			{FamilyEdge, filepath.Join(home, "Applications", "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge")},
		} {
			add(item.family, item.path, SourceStandard)
		}
	case "windows":
		for _, candidate := range standardWindowsCandidates(runtime, TransportNative) {
			add(candidate.Family, candidate.Executable, candidate.Source)
		}
	}
	return dedupeCandidates(result)
}

func windowsHostCandidates(ctx context.Context, runtime Runtime) []Candidate {
	localAppDataHost, localAppDataLocal, err := windowsLocalAppData(ctx, runtime)
	if err != nil {
		return nil
	}
	roots := make([]string, 0, 3)
	for _, name := range []string{"PROGRAMFILES", "PROGRAMFILES(X86)", "LOCALAPPDATA"} {
		value, envErr := runtime.WindowsEnv(ctx, name)
		if envErr == nil && strings.TrimSpace(value) != "" {
			roots = append(roots, strings.TrimSpace(value))
		}
	}
	result := make([]Candidate, 0)
	for _, candidate := range windowsCandidatesForRoots(roots, TransportWSLHost) {
		local, err := runtime.WindowsToLocal(ctx, candidate.Executable)
		if err != nil {
			continue
		}
		candidate.LocalExecutable = local
		candidate.LocalAppData = localAppDataLocal
		candidate.HostLocalAppData = localAppDataHost
		result = append(result, candidate)
	}
	return dedupeCandidates(result)
}

func standardWindowsCandidates(runtime Runtime, transport Transport) []Candidate {
	roots := []string{runtime.Env("PROGRAMFILES"), runtime.Env("PROGRAMFILES(X86)"), runtime.Env("LOCALAPPDATA")}
	return windowsCandidatesForRoots(roots, transport)
}

func windowsCandidatesForRoots(roots []string, transport Transport) []Candidate {
	result := make([]Candidate, 0, 6)
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		for _, item := range []struct {
			family Family
			parts  []string
		}{
			{FamilyChrome, []string{"Google", "Chrome", "Application", "chrome.exe"}},
			{FamilyChromium, []string{"Chromium", "Application", "chrome.exe"}},
			{FamilyEdge, []string{"Microsoft", "Edge", "Application", "msedge.exe"}},
		} {
			path := joinHostPath("windows", append([]string{root}, item.parts...)...)
			result = append(result, Candidate{Family: item.family, Executable: path, LocalExecutable: path, HostPlatform: "windows", Transport: transport, Source: SourceStandard})
		}
	}
	return result
}

func normalizedRuntime(value Runtime) Runtime {
	if strings.TrimSpace(value.GOOS) == "" {
		value.GOOS = currentGOOS()
	}
	if value.Env == nil {
		value.Env = os.Getenv
	}
	if value.KernelRelease == nil {
		value.KernelRelease = currentKernelRelease
	}
	if value.LookPath == nil {
		value.LookPath = exec.LookPath
	}
	if value.Exists == nil {
		value.Exists = func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.Mode().IsRegular() && info.Size() > 0
		}
	}
	if value.WindowsToLocal == nil {
		value.WindowsToLocal = defaultWindowsToLocal
	}
	if value.WindowsEnv == nil {
		value.WindowsEnv = defaultWindowsEnv
	}
	if value.Probe == nil {
		value.Probe = func(ctx context.Context, candidate Candidate) ProbeResult {
			return probeExecutable(ctx, value, candidate)
		}
	}
	return value
}

func probeExecutable(ctx context.Context, runtime Runtime, candidate Candidate) ProbeResult {
	graphical := graphicalAvailable(runtime, candidate)
	versionCtx, cancel := context.WithTimeout(ctx, VersionProbeTimeout)
	defer cancel()
	versionOutput, err := runOutput(versionCtx, candidate.LocalExecutable, "--version")
	if err != nil {
		return ProbeResult{Graphical: graphical, Reason: "browser version probe failed: " + err.Error()}
	}
	family := parseFamily(versionOutput)
	if family == "" {
		family = candidate.Family
	}
	if family == "" {
		return ProbeResult{Graphical: graphical, Reason: "browser version probe did not identify Chrome, Chromium, or Edge"}
	}
	version := parseVersion(versionOutput)
	launchCtx, launchCancel := context.WithTimeout(ctx, LaunchProbeTimeout)
	defer launchCancel()
	if err := probeCDPLoopback(launchCtx, runtime, candidate); err != nil {
		return ProbeResult{Graphical: graphical, Family: family, Version: version, Reason: "browser launch/CDP probe failed: " + err.Error()}
	}
	return ProbeResult{Usable: true, Graphical: graphical, Family: family, Version: version}
}

func probeCDPLoopback(ctx context.Context, runtime Runtime, candidate Candidate) error {
	localParent, hostParent, legacyParent, err := transientProbeRoots(ctx, runtime, candidate)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(localParent, 0700); err != nil {
		return err
	}
	if legacyParent != "" && filepath.Clean(legacyParent) != filepath.Clean(localParent) {
		pruneStaleProbeProfiles(legacyParent, time.Now())
	}
	pruneStaleProbeProfiles(localParent, time.Now())
	localProfile, err := os.MkdirTemp(localParent, ".probe-")
	if err != nil {
		return err
	}
	defer removeProbeProfile(localProfile)
	if err := prepareProbeProfile(localProfile, candidate.Transport); err != nil {
		return err
	}
	hostProfile := localProfile
	if candidate.Transport == TransportWSLHost {
		hostProfile = joinHostPath("windows", hostParent, filepath.Base(localProfile))
	}
	args := browserProbeArgs(hostProfile)
	command := exec.CommandContext(ctx, candidate.LocalExecutable, args...)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		if candidate.Transport == TransportWSLHost {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = stopWindowsHostBrowser(cleanupCtx, candidate.Executable, hostProfile)
			cancel()
		}
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()
	portFile := filepath.Join(localProfile, "DevToolsActivePort")
	deadline := time.NewTicker(50 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			port, err := readDevToolsPort(portFile)
			if err != nil {
				continue
			}
			endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
			var relay *loopbackRelay
			if candidate.Transport == TransportWSLHost {
				relay, err = startWindowsLoopbackRelay(port)
				if err != nil {
					return err
				}
				endpoint = relay.URL()
			}
			if err := verifyBrowserEndpoint(ctx, endpoint); err == nil {
				if relay != nil {
					_ = relay.Close()
				}
				return nil
			}
			if relay != nil {
				_ = relay.Close()
			}
		}
	}
}

func transientProbeRoots(ctx context.Context, runtime Runtime, candidate Candidate) (localParent, hostParent, legacyParent string, err error) {
	if candidate.Transport != TransportWSLHost {
		parent := os.TempDir()
		return parent, parent, "", nil
	}
	hostTemp, localTemp, err := windowsTempDir(ctx, runtime)
	if err != nil {
		return "", "", "", err
	}
	localParent = filepath.Join(strings.TrimSpace(localTemp), "CodeMCP", "Browser")
	hostParent = joinHostPath("windows", hostTemp, "CodeMCP", "Browser")
	if strings.TrimSpace(candidate.LocalAppData) != "" {
		legacyParent = filepath.Join(candidate.LocalAppData, "CodeMCP", "Browser")
	}
	return localParent, hostParent, legacyParent, nil
}

func windowsTempDir(ctx context.Context, runtime Runtime) (string, string, error) {
	var lastErr error
	for _, name := range []string{"TEMP", "TMP"} {
		host, err := runtime.WindowsEnv(ctx, name)
		if err != nil {
			lastErr = err
			continue
		}
		host = strings.TrimSpace(host)
		if !looksWindowsPath(host) {
			continue
		}
		local, err := runtime.WindowsToLocal(ctx, host)
		if err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(local) != "" {
			return host, strings.TrimSpace(local), nil
		}
	}
	if lastErr != nil {
		return "", "", fmt.Errorf("resolve Windows temp directory: %w", lastErr)
	}
	return "", "", errors.New("windows temp directory is unavailable")
}

func removeProbeProfile(path string) {
	for attempt := 0; attempt < 4; attempt++ {
		if err := os.RemoveAll(path); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func pruneStaleProbeProfiles(parent string, now time.Time) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	staleBefore := now.Add(-time.Minute)
	for _, entry := range entries {
		if !entry.IsDir() || !probeArtifactName(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.ModTime().Before(staleBefore) {
			continue
		}
		removeProbeProfile(filepath.Join(parent, entry.Name()))
	}
}

func probeArtifactName(name string) bool {
	return strings.HasPrefix(name, ".probe-") || strings.HasPrefix(name, ".diag-")
}

func prepareProbeProfile(localProfile string, transport Transport) error {
	return PrepareProfile(ProfileRef{Transport: transport, LocalPath: localProfile})
}

func readDevToolsPort(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, errors.New("DevToolsActivePort is empty")
	}
	port, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("DevToolsActivePort contains invalid port")
	}
	return port, nil
}

func runOutput(ctx context.Context, executable string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, executable, args...)
	data, err := command.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

var versionPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+){1,3}`)

func parseVersion(value string) string {
	return versionPattern.FindString(value)
}

func parseFamily(value string) Family {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "microsoft edge"), strings.Contains(value, "msedge"):
		return FamilyEdge
	case strings.Contains(value, "chromium"):
		return FamilyChromium
	case strings.Contains(value, "google chrome"), strings.Contains(value, "chrome"):
		return FamilyChrome
	default:
		return ""
	}
}

func browserProbeArgs(profile string) []string {
	return []string{
		"--headless=new", "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
		"--disable-component-update", "--disable-sync", "--disable-extensions",
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0",
		"--user-data-dir=" + profile, "--profile-directory=" + managedProfileDirectory, "about:blank",
	}
}

func graphicalAvailable(runtime Runtime, candidate Candidate) bool {
	if candidate.Transport == TransportWSLHost {
		return true
	}
	switch candidate.HostPlatform {
	case "linux":
		return runtime.Env("WAYLAND_DISPLAY") != "" || runtime.Env("DISPLAY") != ""
	case "windows":
		return !strings.EqualFold(strings.TrimSpace(runtime.Env("SESSIONNAME")), "Services")
	case "darwin":
		return true
	default:
		return false
	}
}

func isWSL(runtime Runtime) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if strings.TrimSpace(runtime.Env("WSL_INTEROP")) != "" || strings.TrimSpace(runtime.Env("WSL_DISTRO_NAME")) != "" {
		return true
	}
	release := strings.ToLower(strings.TrimSpace(runtime.KernelRelease()))
	return strings.Contains(release, "microsoft") || strings.Contains(release, "wsl")
}

func currentKernelRelease() string {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func windowsLocalAppData(ctx context.Context, runtime Runtime) (string, string, error) {
	host, err := runtime.WindowsEnv(ctx, "LOCALAPPDATA")
	if err != nil {
		return "", "", fmt.Errorf("resolve Windows LocalAppData: %w", err)
	}
	host = strings.TrimSpace(host)
	if !looksWindowsPath(host) {
		return "", "", errors.New("windows LocalAppData is unavailable")
	}
	local, err := runtime.WindowsToLocal(ctx, host)
	if err != nil {
		return "", "", fmt.Errorf("map Windows LocalAppData into WSL: %w", err)
	}
	return host, local, nil
}

func defaultWindowsEnv(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "%&|<>^ \t\r\n") {
		return "", errors.New("invalid Windows environment variable name")
	}
	output, err := runOutput(ctx, "cmd.exe", "/d", "/s", "/c", "echo %"+name+"%")
	if err != nil {
		return "", err
	}
	value := lastNonEmptyLine(output)
	if value == "%"+name+"%" {
		return "", nil
	}
	return value, nil
}

func lastNonEmptyLine(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

func defaultWindowsToLocal(ctx context.Context, value string) (string, error) {
	output, err := runOutput(ctx, "wslpath", "-u", value)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(output) == "" {
		return "", errors.New("wslpath returned an empty path")
	}
	return strings.TrimSpace(output), nil
}

func familyForExecutable(path string) Family {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(path, `\`, "/")))
	switch {
	case strings.Contains(name, "msedge"):
		return FamilyEdge
	case strings.Contains(name, "chromium"):
		return FamilyChromium
	case strings.Contains(name, "chrome"):
		return FamilyChrome
	default:
		return ""
	}
}

func looksWindowsPath(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func dedupeCandidates(values []Candidate) []Candidate {
	result := make([]Candidate, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value.Executable)) + "|" + string(value.Transport)
		if key == "|"+string(value.Transport) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func boundedReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= ReasonLimit {
		return value
	}
	return value[:ReasonLimit]
}

func unavailable(enabled bool, reason string) Capability {
	return Capability{State: StateUnavailable, Enabled: enabled, Reason: boundedReason(reason)}
}
