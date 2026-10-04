package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeBrowserRuntime struct {
	goos       string
	env        map[string]string
	kernel     string
	windowsEnv map[string]string
	look       map[string]string
	exists     func(string) bool
	probe      func(Candidate) ProbeResult
	probed     []Candidate
	mapped     []string
}

func (fake *fakeBrowserRuntime) runtime() Runtime {
	return Runtime{
		GOOS:          fake.goos,
		Env:           func(key string) string { return fake.env[key] },
		KernelRelease: func() string { return fake.kernel },
		LookPath: func(name string) (string, error) {
			if value := fake.look[name]; value != "" {
				return value, nil
			}
			return "", errors.New("not found")
		},
		Exists: func(path string) bool {
			if fake.exists != nil {
				return fake.exists(path)
			}
			return false
		},
		WindowsEnv: func(_ context.Context, name string) (string, error) {
			return fake.windowsEnv[name], nil
		},
		WindowsToLocal: func(_ context.Context, value string) (string, error) {
			fake.mapped = append(fake.mapped, value)
			return fakeWindowsToLocal(value), nil
		},
		Probe: func(_ context.Context, candidate Candidate) ProbeResult {
			fake.probed = append(fake.probed, candidate)
			if fake.probe != nil {
				return fake.probe(candidate)
			}
			return ProbeResult{}
		},
	}
}

func TestDetectExplicitValidExecutableWins(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos:   "linux",
		env:    map[string]string{"DISPLAY": ":0"},
		look:   map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool { return path == "/opt/custom-browser" || path == "/usr/bin/chromium" },
		probe: func(candidate Candidate) ProbeResult {
			return ProbeResult{Usable: true, Graphical: true, Family: FamilyChrome, Version: "154.0.1.2"}
		},
	}
	root := t.TempDir()
	capability := Detect(context.Background(), Options{
		Enabled: true, ConfiguredPath: "/opt/custom-browser", StateRoot: root, Runtime: fake.runtime(),
	})
	if capability.State != StateAvailable || capability.Executable != "/opt/custom-browser" || capability.Family != FamilyChrome || capability.Transport != TransportNative {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) != 1 || fake.probed[0].Source != SourceConfigured {
		t.Fatalf("probe order=%#v", fake.probed)
	}
	if capability.Profile == nil || capability.Profile.Path != filepath.Join(root, "browser", "chatgpt") {
		t.Fatalf("profile=%#v", capability.Profile)
	}
}

func TestDetectInvalidExplicitExecutableDoesNotFallThrough(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos:   "linux",
		env:    map[string]string{"DISPLAY": ":0"},
		look:   map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool { return path == "/usr/bin/chromium" },
		probe: func(Candidate) ProbeResult {
			return ProbeResult{Usable: true, Graphical: true}
		},
	}
	capability := Detect(context.Background(), Options{
		Enabled: true, ConfiguredPath: "/missing/google-chrome", StateRoot: t.TempDir(), Runtime: fake.runtime(),
	})
	if capability.State != StateUnavailable || capability.Usable || !strings.Contains(capability.Reason, "does not exist") {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) != 0 {
		t.Fatalf("invalid explicit path fell through to browser probing: %#v", fake.probed)
	}
}

func TestDetectUnusableExplicitExecutableDoesNotFallThrough(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos:   "linux",
		env:    map[string]string{"DISPLAY": ":0"},
		look:   map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool { return path == "/opt/custom-browser" || path == "/usr/bin/chromium" },
		probe: func(candidate Candidate) ProbeResult {
			if candidate.Source == SourceConfigured {
				return ProbeResult{Graphical: true, Family: FamilyChrome, Reason: "launch probe failed"}
			}
			return ProbeResult{Usable: true, Graphical: true, Family: FamilyChromium}
		},
	}
	capability := Detect(context.Background(), Options{
		Enabled: true, ConfiguredPath: "/opt/custom-browser", StateRoot: t.TempDir(), Runtime: fake.runtime(),
	})
	if capability.State != StateUnavailable || capability.Usable || !strings.Contains(capability.Reason, "launch probe failed") {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) != 1 || fake.probed[0].Source != SourceConfigured {
		t.Fatalf("unusable explicit browser fell through: %#v", fake.probed)
	}
}

func TestDetectDisabledSkipsAllDiscovery(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos:   "linux",
		env:    map[string]string{},
		look:   map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(string) bool { return true },
		probe:  func(Candidate) ProbeResult { return ProbeResult{Usable: true, Family: FamilyChromium} },
	}
	capability := Detect(context.Background(), Options{Enabled: false, StateRoot: t.TempDir(), Runtime: fake.runtime()})
	if capability.State != StateDisabled || capability.Enabled || capability.Available || capability.Usable {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) != 0 {
		t.Fatalf("disabled integration probed browsers: %#v", fake.probed)
	}
}

func TestDetectPassiveDiscoversBrowserWithoutProbing(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos: "linux",
		env:  map[string]string{"DISPLAY": ":0"},
		look: map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool {
			return path == "/usr/bin/chromium"
		},
		probe: func(Candidate) ProbeResult {
			t.Fatal("passive detection must not launch/probe the browser")
			return ProbeResult{}
		},
	}
	root := t.TempDir()
	capability := Detect(context.Background(), Options{
		Enabled: true, StateRoot: root, Runtime: fake.runtime(), Passive: true,
	})
	if capability.State != StateAvailable || !capability.Available || !capability.Launchable || capability.Usable {
		t.Fatalf("capability=%#v", capability)
	}
	if capability.Family != FamilyChromium || capability.Profile == nil {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) != 0 {
		t.Fatalf("passive detection probed browsers: %#v", fake.probed)
	}
}

func TestDetectPassiveRequiresGraphicalSession(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos: "linux",
		env:  map[string]string{},
		look: map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool {
			return path == "/usr/bin/chromium"
		},
		probe: func(Candidate) ProbeResult {
			t.Fatal("passive detection must not probe the browser")
			return ProbeResult{}
		},
	}
	root := t.TempDir()
	capability := Detect(context.Background(), Options{
		Enabled: true, StateRoot: root, Runtime: fake.runtime(), Passive: true,
	})
	if capability.State != StateUnavailable || capability.Graphical || capability.Available || capability.Launchable {
		t.Fatalf("capability without display=%#v", capability)
	}
}

func TestDetectLinuxGraphicalRoutesCoverX11AndWayland(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
	}{
		{name: "x11", env: map[string]string{"DISPLAY": ":0"}},
		{name: "wayland", env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeBrowserRuntime{
				goos: "linux",
				env:  test.env,
				look: map[string]string{"chromium": "/usr/bin/chromium"},
				exists: func(path string) bool {
					return path == "/usr/bin/chromium"
				},
				probe: func(Candidate) ProbeResult {
					t.Fatal("passive graphical detection must not probe the browser")
					return ProbeResult{}
				},
			}
			capability := Detect(context.Background(), Options{
				Enabled: true, StateRoot: t.TempDir(), Runtime: fake.runtime(), Passive: true,
			})
			if capability.State != StateAvailable || !capability.Graphical || !capability.Launchable {
				t.Fatalf("%s capability=%#v", test.name, capability)
			}
		})
	}
}

func TestDetectActiveProbeMarksCapabilityUsableAndLaunchable(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos: "linux",
		env:  map[string]string{"DISPLAY": ":0"},
		look: map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool {
			return path == "/usr/bin/chromium"
		},
		probe: func(Candidate) ProbeResult {
			return ProbeResult{Usable: true, Graphical: true, Family: FamilyChromium, Version: "154.0.0.0"}
		},
	}
	capability := Detect(context.Background(), Options{
		Enabled: true, StateRoot: t.TempDir(), Runtime: fake.runtime(),
	})
	if capability.State != StateAvailable || !capability.Available || !capability.Launchable || !capability.Usable {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) != 1 {
		t.Fatalf("active detection probes=%d want=1", len(fake.probed))
	}
}

func TestPassiveCapabilityCanReachManagedLauncherWithoutProbe(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos: "linux",
		env:  map[string]string{"DISPLAY": ":0"},
		look: map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool {
			return path == "/usr/bin/chromium"
		},
		probe: func(Candidate) ProbeResult {
			t.Fatal("passive detection must not probe before the managed launch")
			return ProbeResult{}
		},
	}
	capability := Detect(context.Background(), Options{
		Enabled: true, StateRoot: t.TempDir(), Runtime: fake.runtime(), Passive: true,
	})
	launcher := &fakeLauncher{}
	connector := &fakeConnector{}
	manager, err := NewManager(ManagerOptions{
		Capability: capability,
		Launcher:   launcher,
		Connector:  connector,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if err := manager.EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if launcher.count() != 1 {
		t.Fatalf("managed launches=%d want=1", launcher.count())
	}
	if len(fake.probed) != 0 {
		t.Fatalf("passive discovery probes=%#v", fake.probed)
	}
}

func TestPrepareProbeProfileBootstrapsManagedDefault(t *testing.T) {
	root := t.TempDir()
	if err := prepareProbeProfile(root, TransportNative); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, managedProfileDirectory))
	if err != nil || !info.IsDir() {
		t.Fatalf("probe Default profile missing: info=%v err=%v", info, err)
	}
}

func TestDetectBrowserAbsenceIsNormalUnavailableCapability(t *testing.T) {
	fake := &fakeBrowserRuntime{goos: "linux", env: map[string]string{}, look: map[string]string{}}
	capability := Detect(context.Background(), Options{Enabled: true, StateRoot: t.TempDir(), Runtime: fake.runtime()})
	if capability.State != StateUnavailable || !capability.Enabled || capability.Available || capability.Usable {
		t.Fatalf("capability=%#v", capability)
	}
	if capability.Reason == "" || len(capability.Reason) > ReasonLimit {
		t.Fatalf("reason=%q", capability.Reason)
	}
}

func TestDetectWSLgNativeRouteWinsBeforeWindowsHostFallback(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos: "linux",
		env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "DISPLAY": ":0"},
		windowsEnv: map[string]string{
			"PROGRAMFILES": `C:\Program Files`,
			"LOCALAPPDATA": `C:\Users\Mew\AppData\Local`,
		},
		look: map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool {
			return path == "/usr/bin/chromium" || strings.HasSuffix(strings.ToLower(path), "msedge.exe")
		},
		probe: func(candidate Candidate) ProbeResult {
			return ProbeResult{Usable: true, Graphical: true, Version: "154.0.0.0"}
		},
	}
	capability := Detect(context.Background(), Options{Enabled: true, StateRoot: t.TempDir(), Runtime: fake.runtime()})
	if capability.Transport != TransportNative || capability.HostPlatform != "linux" || capability.Executable != "/usr/bin/chromium" {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) != 1 || fake.probed[0].Transport != TransportNative {
		t.Fatalf("WSLg native route did not win: %#v", fake.probed)
	}
	if len(fake.mapped) != 0 {
		t.Fatalf("Windows fallback was inspected before usable WSLg route: %#v", fake.mapped)
	}
}

func TestDetectWSLWindowsHostFallbackUsesWindowsProfile(t *testing.T) {
	configRoot := t.TempDir()
	fake := &fakeBrowserRuntime{
		goos: "linux",
		env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "DISPLAY": ":0"},
		windowsEnv: map[string]string{
			"PROGRAMFILES":      `C:\Program Files`,
			"PROGRAMFILES(X86)": `C:\Program Files (x86)`,
			"LOCALAPPDATA":      `C:\Users\Mew\AppData\Local`,
		},
		look: map[string]string{"chromium": "/usr/bin/chromium"},
		exists: func(path string) bool {
			return path == "/usr/bin/chromium" || strings.HasSuffix(strings.ToLower(strings.ReplaceAll(path, `\`, "/")), "/microsoft/edge/application/msedge.exe")
		},
		probe: func(candidate Candidate) ProbeResult {
			if candidate.Transport == TransportNative {
				return ProbeResult{Graphical: true, Reason: "native launch failed"}
			}
			if candidate.Family == FamilyEdge {
				return ProbeResult{Usable: true, Graphical: true, Version: "154.0.0.0"}
			}
			return ProbeResult{Graphical: true, Reason: "not installed"}
		},
	}
	capability := Detect(context.Background(), Options{Enabled: true, StateRoot: configRoot, Runtime: fake.runtime()})
	if capability.State != StateAvailable || capability.Transport != TransportWSLHost || capability.HostPlatform != "windows" || capability.Family != FamilyEdge {
		t.Fatalf("capability=%#v", capability)
	}
	if capability.Profile == nil || capability.Profile.HostPlatform != "windows" || !strings.HasPrefix(capability.Profile.Path, `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`) {
		t.Fatalf("host profile=%#v", capability.Profile)
	}
	if !strings.HasPrefix(capability.Profile.LocalPath, "/mnt/c/Users/Mew/AppData/Local/CodeMCP/Browser/ChatGPT") {
		t.Fatalf("local profile=%#v", capability.Profile)
	}
	if strings.HasPrefix(filepath.Clean(capability.Profile.LocalPath), filepath.Clean(configRoot)) {
		t.Fatalf("Windows browser was assigned a WSL/Linux profile: %#v", capability.Profile)
	}
}

func TestDetectWSLWindowsHostFallbackWhenServiceEnvironmentDropsWSLVariables(t *testing.T) {
	configRoot := t.TempDir()
	fake := &fakeBrowserRuntime{
		goos:   "linux",
		env:    map[string]string{},
		kernel: "6.18.33.2-microsoft-standard-WSL2",
		windowsEnv: map[string]string{
			"PROGRAMFILES":      `C:\\Program Files`,
			"PROGRAMFILES(X86)": `C:\\Program Files (x86)`,
			"LOCALAPPDATA":      `C:\\Users\\Mew\\AppData\\Local`,
		},
		look: map[string]string{},
		exists: func(path string) bool {
			return strings.HasSuffix(strings.ToLower(strings.ReplaceAll(path, `\\`, "/")), "/microsoft/edge/application/msedge.exe")
		},
		probe: func(candidate Candidate) ProbeResult {
			if candidate.Transport != TransportWSLHost || candidate.Family != FamilyEdge {
				return ProbeResult{Reason: "unexpected candidate"}
			}
			return ProbeResult{Usable: true, Graphical: true, Family: FamilyEdge, Version: "154.0.0.0"}
		},
	}

	capability := Detect(context.Background(), Options{Enabled: true, StateRoot: configRoot, Runtime: fake.runtime()})
	if capability.State != StateAvailable || capability.Transport != TransportWSLHost || capability.HostPlatform != "windows" || !capability.Graphical {
		t.Fatalf("capability=%#v", capability)
	}
	if capability.Profile == nil || capability.Profile.HostPlatform != "windows" {
		t.Fatalf("profile=%#v", capability.Profile)
	}
}

func TestIsWSLUsesKernelFallbackOnlyOnLinux(t *testing.T) {
	for _, test := range []struct {
		name   string
		goos   string
		kernel string
		want   bool
	}{
		{name: "wsl2 kernel", goos: "linux", kernel: "6.18.33.2-microsoft-standard-WSL2", want: true},
		{name: "legacy microsoft kernel", goos: "linux", kernel: "4.4.0-19041-Microsoft", want: true},
		{name: "ordinary linux", goos: "linux", kernel: "6.12.0-generic", want: false},
		{name: "non linux", goos: "darwin", kernel: "microsoft", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeBrowserRuntime{goos: test.goos, env: map[string]string{}, kernel: test.kernel}
			if got := isWSL(fake.runtime()); got != test.want {
				t.Fatalf("isWSL=%t want=%t", got, test.want)
			}
		})
	}
}

func TestDetectWSLWindowsHostRequiresUsablePrivateCDPRoute(t *testing.T) {
	fake := &fakeBrowserRuntime{
		goos: "linux",
		env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
		windowsEnv: map[string]string{
			"PROGRAMFILES": `C:\Program Files`,
			"LOCALAPPDATA": `C:\Users\Mew\AppData\Local`,
		},
		look: map[string]string{},
		exists: func(path string) bool {
			return strings.HasSuffix(strings.ToLower(strings.ReplaceAll(path, `\`, "/")), "/microsoft/edge/application/msedge.exe")
		},
		probe: func(Candidate) ProbeResult {
			return ProbeResult{Graphical: true, Reason: "loopback CDP route is unreachable"}
		},
	}
	capability := Detect(context.Background(), Options{Enabled: true, StateRoot: t.TempDir(), Runtime: fake.runtime()})
	if capability.State != StateUnavailable || capability.Usable || capability.Available {
		t.Fatalf("capability=%#v", capability)
	}
	if len(fake.probed) == 0 {
		t.Fatal("Windows host browser was not probed")
	}
}

func TestNativeDiscoveryCoversChromiumFamiliesAcrossPlatforms(t *testing.T) {
	tests := []struct {
		name string
		fake *fakeBrowserRuntime
		want map[Family]bool
	}{
		{
			name: "linux",
			fake: &fakeBrowserRuntime{goos: "linux", env: map[string]string{}, look: map[string]string{
				"google-chrome": "/bin/google-chrome", "chromium": "/bin/chromium", "microsoft-edge": "/bin/microsoft-edge",
			}},
			want: map[Family]bool{FamilyChrome: true, FamilyChromium: true, FamilyEdge: true},
		},
		{
			name: "darwin",
			fake: &fakeBrowserRuntime{goos: "darwin", env: map[string]string{"HOME": "/Users/mew"}, look: map[string]string{}},
			want: map[Family]bool{FamilyChrome: true, FamilyChromium: true, FamilyEdge: true},
		},
		{
			name: "windows",
			fake: &fakeBrowserRuntime{goos: "windows", env: map[string]string{
				"PROGRAMFILES": `C:\Program Files`, "LOCALAPPDATA": `C:\Users\Mew\AppData\Local`,
			}, look: map[string]string{}},
			want: map[Family]bool{FamilyChrome: true, FamilyChromium: true, FamilyEdge: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			families := map[Family]bool{}
			for _, candidate := range nativeCandidates(test.fake.runtime()) {
				families[candidate.Family] = true
			}
			for family := range test.want {
				if !families[family] {
					t.Fatalf("%s discovery missing %s: %#v", test.name, family, nativeCandidates(test.fake.runtime()))
				}
			}
		})
	}
}

func TestNativeDiscoveryUsesExpectedMacOSAndWindowsBrowserPaths(t *testing.T) {
	tests := []struct {
		name string
		fake *fakeBrowserRuntime
		want map[Family]string
	}{
		{
			name: "macos",
			fake: &fakeBrowserRuntime{goos: "darwin", env: map[string]string{"HOME": "/Users/mew"}, look: map[string]string{}},
			want: map[Family]string{
				FamilyChrome:   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
				FamilyChromium: "/Applications/Chromium.app/Contents/MacOS/Chromium",
				FamilyEdge:     "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			},
		},
		{
			name: "windows",
			fake: &fakeBrowserRuntime{goos: "windows", env: map[string]string{
				"PROGRAMFILES": `C:\Program Files`,
				"LOCALAPPDATA": `C:\Users\Mew\AppData\Local`,
			}, look: map[string]string{}},
			want: map[Family]string{
				FamilyChrome:   `C:\Program Files\Google\Chrome\Application\chrome.exe`,
				FamilyChromium: `C:\Program Files\Chromium\Application\chrome.exe`,
				FamilyEdge:     `C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := nativeCandidates(test.fake.runtime())
			for family, expected := range test.want {
				found := false
				for _, candidate := range candidates {
					if candidate.Family == family && candidate.Executable == expected && candidate.Transport == TransportNative {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%s discovery missing %s path %q: %#v", test.name, family, expected, candidates)
				}
			}
		})
	}
}

func TestBrowserProbeArgumentsBindCDPOnlyToLoopback(t *testing.T) {
	args := browserProbeArgs("/tmp/profile")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--remote-debugging-address=127.0.0.1") || !strings.Contains(joined, "--remote-debugging-port=0") {
		t.Fatalf("probe args do not use private ephemeral CDP endpoint: %v", args)
	}
	if !strings.Contains(joined, "--profile-directory=Default") {
		t.Fatalf("probe args do not select the managed Default profile: %v", args)
	}
	for _, forbidden := range []string{"0.0.0.0", "::", "--remote-allow-origins=*"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("probe args expose CDP broadly: %v", args)
		}
	}
}

func TestBoundedReasonAndVersionParsing(t *testing.T) {
	if got := parseVersion("Google Chrome 154.0.8012.42"); got != "154.0.8012.42" {
		t.Fatalf("version=%q", got)
	}
	for input, want := range map[string]Family{
		"Google Chrome 154.0.8012.42": FamilyChrome,
		"Chromium 154.0.8012.42":      FamilyChromium,
		"Microsoft Edge 154.0.0.0":    FamilyEdge,
	} {
		if got := parseFamily(input); got != want {
			t.Fatalf("family %q=%q want=%q", input, got, want)
		}
	}
	value := strings.Repeat("x", ReasonLimit+100)
	if got := boundedReason(value); len(got) != ReasonLimit {
		t.Fatalf("bounded reason len=%d", len(got))
	}
}

func TestLastNonEmptyLineIgnoresCmdUNCWorkingDirectoryWarnings(t *testing.T) {
	output := "'\\\\wsl.localhost\\Ubuntu\\home\\mew'\r\nCMD.EXE was started with the above path as the current directory.\r\nUNC paths are not supported. Defaulting to Windows directory.\r\nC:\\Users\\Mew\\AppData\\Local\r\n"
	if got := lastNonEmptyLine(output); got != `C:\Users\Mew\AppData\Local` {
		t.Fatalf("last line=%q", got)
	}
}

func fakeWindowsToLocal(value string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	if len(value) >= 3 && value[1] == ':' {
		drive := strings.ToLower(value[:1])
		return "/mnt/" + drive + "/" + strings.TrimLeft(value[2:], "/")
	}
	return value
}
