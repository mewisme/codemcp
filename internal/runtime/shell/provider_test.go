package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/workspace"
)

func TestProviderExplicitShellWinsOnEveryPlatform(t *testing.T) {
	t.Run("posix", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX executable-bit fixture is not representable on Windows")
		}
		executable := writeProviderFixture(t, "custom-shell", 0755, "#!/bin/sh\nexit 0\n")
		resolver := NewProviderResolver()
		resolver.goos = "linux"
		resolver.getenv = func(name string) string {
			if name == "SHELL" {
				return executable
			}
			return ""
		}
		resolver.lookPath = func(string) (string, error) {
			t.Fatal("automatic discovery ran despite explicit SHELL")
			return "", exec.ErrNotFound
		}
		provider, err := resolver.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		if provider.Executable != executable || provider.Source != ProviderSourceConfigured || provider.Kind != ProviderPOSIX {
			t.Fatalf("provider=%#v", provider)
		}
	})

	t.Run("windows", func(t *testing.T) {
		executable := writeProviderFixture(t, "pwsh.exe", 0644, "fixture")
		resolver := NewProviderResolver()
		resolver.goos = "windows"
		resolver.getenv = func(name string) string {
			if name == "SHELL" {
				return executable
			}
			return ""
		}
		resolver.lookPath = func(string) (string, error) {
			t.Fatal("automatic discovery ran despite explicit SHELL")
			return "", exec.ErrNotFound
		}
		provider, err := resolver.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		if provider.Executable != executable || provider.Source != ProviderSourceConfigured || provider.Kind != ProviderPowerShell7 {
			t.Fatalf("provider=%#v", provider)
		}
	})
}

func TestWindowsConfiguredShellPathWinsBeforeAutomaticDiscovery(t *testing.T) {
	configuredDir := t.TempDir()
	configuredPowerShell := filepath.Join(configuredDir, "powershell.exe")
	if err := os.WriteFile(configuredPowerShell, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRoot := filepath.Join(t.TempDir(), "Git")
	gitExecutable := writeProviderFixtureAt(t, filepath.Join(gitRoot, "cmd", "git.exe"), 0644, "fixture")
	writeProviderFixtureAt(t, filepath.Join(gitRoot, "bin", "bash.exe"), 0644, "fixture")

	resolver := NewProviderResolver()
	resolver.goos = "windows"
	resolver.getenv = func(name string) string {
		if name == "SHELL" {
			return filepath.Join(gitRoot, "bin", "bash.exe")
		}
		return ""
	}
	resolver.lookPath = func(name string) (string, error) {
		if name == "git.exe" {
			return gitExecutable, nil
		}
		return "", exec.ErrNotFound
	}
	provider, err := resolver.Resolve([]string{configuredDir})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Executable != configuredPowerShell || provider.Source != ProviderSourceConfigured || provider.Kind != ProviderWindowsPowerShell {
		t.Fatalf("provider=%#v", provider)
	}
}

func TestConfiguredShellPathWinsOverInheritedShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable-bit fixture is not representable on Windows")
	}
	configuredDir := t.TempDir()
	configured := filepath.Join(configuredDir, "bash")
	if err := os.WriteFile(configured, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	inherited := writeProviderFixture(t, "zsh", 0755, "#!/bin/sh\nexit 0\n")
	resolver := NewProviderResolver()
	resolver.goos = "linux"
	resolver.getenv = func(name string) string {
		if name == "SHELL" {
			return inherited
		}
		return ""
	}
	provider, err := resolver.Resolve([]string{configuredDir})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Executable != configured || provider.Source != ProviderSourceConfigured || provider.Language != "bash" {
		t.Fatalf("provider=%#v", provider)
	}
}

func TestWindowsAutomaticResolutionOrder(t *testing.T) {
	gitRoot := filepath.Join(t.TempDir(), "Git")
	gitExecutable := writeProviderFixtureAt(t, filepath.Join(gitRoot, "cmd", "git.exe"), 0644, "fixture")
	gitBash := writeProviderFixtureAt(t, filepath.Join(gitRoot, "bin", "bash.exe"), 0644, "fixture")
	pwsh := writeProviderFixture(t, "pwsh.exe", 0644, "fixture")
	powershell := writeProviderFixture(t, "powershell.exe", 0644, "fixture")

	tests := []struct {
		name      string
		available map[string]string
		want      string
		kind      ProviderKind
	}{
		{
			name: "git bash before powershell families",
			available: map[string]string{
				"git.exe": gitExecutable, "pwsh.exe": pwsh, "powershell.exe": powershell,
			},
			want: gitBash, kind: ProviderGitBash,
		},
		{
			name:      "powershell 7 before windows powershell",
			available: map[string]string{"pwsh.exe": pwsh, "powershell.exe": powershell},
			want:      pwsh, kind: ProviderPowerShell7,
		},
		{
			name:      "windows powershell last",
			available: map[string]string{"powershell.exe": powershell},
			want:      powershell, kind: ProviderWindowsPowerShell,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := NewProviderResolver()
			resolver.goos = "windows"
			resolver.getenv = func(string) string { return "" }
			resolver.lookPath = func(name string) (string, error) {
				if value := tc.available[name]; value != "" {
					return value, nil
				}
				return "", exec.ErrNotFound
			}
			provider, err := resolver.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			if provider.Executable != tc.want || provider.Kind != tc.kind || provider.Source != ProviderSourceSystem {
				t.Fatalf("provider=%#v", provider)
			}
		})
	}
}

func TestWindowsGitBashResolvesDirectlyFromPathAndIgnoresNonGitBash(t *testing.T) {
	t.Run("git bash path", func(t *testing.T) {
		bash := writeProviderFixtureAt(t, filepath.Join(t.TempDir(), "Git", "usr", "bin", "bash.exe"), 0644, "fixture")
		resolver := NewProviderResolver()
		resolver.goos = "windows"
		resolver.getenv = func(string) string { return "" }
		resolver.lookPath = func(name string) (string, error) {
			if name == "bash.exe" {
				return bash, nil
			}
			return "", exec.ErrNotFound
		}
		provider, err := resolver.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		if provider.Executable != bash || provider.Kind != ProviderGitBash {
			t.Fatalf("provider=%#v", provider)
		}
	})

	t.Run("non git bash", func(t *testing.T) {
		bash := writeProviderFixture(t, "bash.exe", 0644, "fixture")
		pwsh := writeProviderFixture(t, "pwsh.exe", 0644, "fixture")
		resolver := NewProviderResolver()
		resolver.goos = "windows"
		resolver.getenv = func(string) string { return "" }
		resolver.lookPath = func(name string) (string, error) {
			switch name {
			case "bash.exe":
				return bash, nil
			case "pwsh.exe":
				return pwsh, nil
			default:
				return "", exec.ErrNotFound
			}
		}
		provider, err := resolver.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		if provider.Executable != pwsh || provider.Kind != ProviderPowerShell7 {
			t.Fatalf("provider=%#v", provider)
		}
	})
}

func TestWindowsGitBashResolvesFromStandardInstallLocation(t *testing.T) {
	programFiles := t.TempDir()
	bash := writeProviderFixtureAt(t, filepath.Join(programFiles, "Git", "usr", "bin", "bash.exe"), 0644, "fixture")
	resolver := NewProviderResolver()
	resolver.goos = "windows"
	resolver.getenv = func(name string) string {
		if name == "ProgramFiles" {
			return programFiles
		}
		return ""
	}
	resolver.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	provider, err := resolver.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if provider.Executable != bash || provider.Kind != ProviderGitBash || provider.Source != ProviderSourceSystem {
		t.Fatalf("provider=%#v", provider)
	}
}

func TestWindowsUnavailableShellReturnsTypedActionableErrorWithoutManagedFallback(t *testing.T) {
	resolver := NewProviderResolver()
	resolver.goos = "windows"
	resolver.getenv = func(string) string { return "" }
	resolver.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	_, err := resolver.Resolve(nil)
	if !errors.Is(err, ErrShellUnavailable) {
		t.Fatalf("err=%v", err)
	}
	var typed *ShellUnavailableError
	if !errors.As(err, &typed) || typed.GOOS != "windows" {
		t.Fatalf("typed error=%#v err=%v", typed, err)
	}
	message := strings.ToLower(err.Error())
	for _, want := range []string{"git for windows", "powershell 7", "windows powershell"} {
		if !strings.Contains(message, want) {
			t.Fatalf("missing repair guidance %q: %v", want, err)
		}
	}
	for _, forbidden := range []string{"managed bash", "download", "install asset"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("managed fallback leaked into error: %v", err)
		}
	}
}

func TestProviderRejectsRelativeConfiguredSearchPath(t *testing.T) {
	resolver := NewProviderResolver()
	resolver.getenv = func(string) string { return "" }
	if _, err := resolver.Resolve([]string{"relative/bin"}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderDiagnosticIsBoundedAndOmitsExecutablePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable-bit fixture is not representable on Windows")
	}
	bin := t.TempDir()
	executable := writeProviderFixtureAt(t, filepath.Join(bin, "bash"), 0755, "#!/bin/sh\n")
	resolver := NewProviderResolver()
	resolver.goos = "linux"
	resolver.getenv = func(string) string { return "" }
	resolver.lookPath = func(string) (string, error) { return executable, nil }

	diagnostic := resolver.Diagnose([]string{bin})
	if !diagnostic.Available || diagnostic.Source != ProviderSourceConfigured || diagnostic.Kind != ProviderPOSIX ||
		diagnostic.Language != "bash" || diagnostic.ConfiguredPaths != 1 || diagnostic.ErrorCode != "" {
		t.Fatalf("diagnostic=%#v", diagnostic)
	}
	if strings.Contains(fmt.Sprintf("%#v", diagnostic), executable) {
		t.Fatalf("diagnostic leaked executable path: %#v", diagnostic)
	}
}

func TestProviderDiagnosticClassifiesUnavailableAndInvalidConfiguration(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		resolver := NewProviderResolver()
		resolver.goos = "windows"
		resolver.getenv = func(string) string { return "" }
		resolver.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
		diagnostic := resolver.Diagnose(nil)
		if diagnostic.Available || diagnostic.ErrorCode != "unavailable" {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
	})

	t.Run("invalid configuration", func(t *testing.T) {
		resolver := NewProviderResolver()
		resolver.goos = "linux"
		resolver.getenv = func(string) string { return "" }
		diagnostic := resolver.Diagnose([]string{"relative/bin"})
		if diagnostic.Available || diagnostic.ErrorCode != "invalid_configuration" || diagnostic.ConfiguredPaths != 1 {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
	})
}

func TestSessionExecUsesConfiguredShellResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	t.Setenv("SHELL", "")
	bin := t.TempDir()
	bash := filepath.Join(bin, "bash")
	if err := os.WriteFile(bash, []byte("#!/bin/sh\nif [ \"$1\" = \"-c\" ]; then printf 'configured:%s' \"$2\"; else exit 2; fi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	workspaces.SetShellPath([]string{bin})
	item, err := workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(workspaces, t.TempDir())
	manager.resolver.getenv = func(string) string { return "" }
	manager.resolver.lookPath = func(name string) (string, error) {
		if name == "bash" {
			return "/bin/bash", nil
		}
		return "", exec.ErrNotFound
	}
	result, err := manager.Exec(context.Background(), item.ID, "printf session")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != "configured:printf session" {
		t.Fatalf("result=%#v", result)
	}
	items := manager.Executions().List(item.ID, 1)
	if len(items) != 1 {
		t.Fatalf("execution items=%#v", items)
	}
	if items[0].Shell != "bash" {
		t.Fatalf("execution shell=%q", items[0].Shell)
	}
}

func TestForegroundAndBackgroundShareResolvedProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	bin := t.TempDir()
	bash := filepath.Join(bin, "bash")
	if err := os.WriteFile(bash, []byte("#!/bin/sh\nif [ \"$1\" = \"-c\" ]; then printf 'resolved:%s' \"$2\"; else exit 2; fi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	workspaces.SetShellPath([]string{bin})
	item, err := workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(workspaces, t.TempDir())
	manager.resolver.getenv = func(name string) string {
		if name == "SHELL" {
			return "/bin/sh"
		}
		return ""
	}
	manager.resolver.lookPath = func(name string) (string, error) {
		if name == "bash" {
			return "/bin/bash", nil
		}
		return "", exec.ErrNotFound
	}

	foreground, err := manager.Exec(context.Background(), item.ID, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if foreground.Stdout != "resolved:foreground" {
		t.Fatalf("foreground=%#v", foreground)
	}

	processes := NewProcessManagerWithExecutions(workspaces, manager, manager.Executions())
	started, err := processes.Start(context.Background(), item.ID, "background")
	if err != nil {
		t.Fatal(err)
	}
	processes.mu.RLock()
	process := processes.processes[started.ID]
	processes.mu.RUnlock()
	if process == nil {
		t.Fatal("background process missing")
	}
	if filepath.Clean(process.cmd.Path) != filepath.Clean(bash) {
		t.Fatalf("background executable=%q want %q", process.cmd.Path, bash)
	}
	select {
	case <-process.done:
	case <-time.After(2 * time.Second):
		t.Fatal("background process did not finish")
	}
	output, err := processes.Output(item.ID, started.ID, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.Stdout) != "resolved:background" {
		t.Fatalf("background output=%#v", output)
	}
	history := manager.Executions().List(item.ID, 10)
	if len(history) != 2 {
		t.Fatalf("execution history=%#v", history)
	}
	for _, execution := range history {
		if execution.Shell != "bash" {
			t.Fatalf("execution shell mismatch: %#v", history)
		}
	}
}

func TestCommandForProviderPreservesProviderSpecificSemantics(t *testing.T) {
	command := `printf "quoted && value" && printf tail`
	tests := []struct {
		name     string
		provider Provider
		wantArgs []string
		wantTail string
	}{
		{
			name:     "git bash",
			provider: Provider{Executable: "bash.exe", Language: "bash", Kind: ProviderGitBash},
			wantArgs: []string{"bash.exe", "--noprofile", "--norc", "-c", command},
		},
		{
			name:     "powershell 7",
			provider: Provider{Executable: "pwsh.exe", Language: "powershell", Kind: ProviderPowerShell7},
			wantArgs: []string{"pwsh.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command},
		},
		{
			name:     "windows powershell",
			provider: Provider{Executable: "powershell.exe", Language: "powershell", Kind: ProviderWindowsPowerShell},
			wantTail: "if ($__chatgptMcpSuccess)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := commandForProvider(context.Background(), command, tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.wantArgs) > 0 {
				if len(cmd.Args) != len(tc.wantArgs) {
					t.Fatalf("args=%#v want=%#v", cmd.Args, tc.wantArgs)
				}
				for index := range tc.wantArgs {
					if cmd.Args[index] != tc.wantArgs[index] {
						t.Fatalf("args=%#v want=%#v", cmd.Args, tc.wantArgs)
					}
				}
				return
			}
			if len(cmd.Args) != 7 || cmd.Args[0] != "powershell.exe" || cmd.Args[5] != "-Command" {
				t.Fatalf("windows powershell args=%#v", cmd.Args)
			}
			effective := cmd.Args[6]
			if !strings.Contains(effective, tc.wantTail) || !strings.Contains(effective, `"quoted && value"`) {
				t.Fatalf("transpiled command=%q", effective)
			}
		})
	}
}

func TestPreviewUsesResolvedProviderForCWDOnlyCommand(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	writeProviderFixtureAt(t, filepath.Join(bin, "bash.exe"), 0644, "fixture")
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	workspaces.SetShellPath([]string{bin})
	item, err := workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(workspaces, t.TempDir())
	manager.resolver.goos = "windows"
	manager.resolver.getenv = func(string) string { return "" }
	manager.resolver.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	preview, err := manager.PreviewCommand(context.Background(), item.ID, "cd child", false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Effective != "pwd" || filepath.Clean(preview.CWD) != filepath.Clean(child) {
		t.Fatalf("preview=%#v", preview)
	}
}

func TestSessionSecurityGuardRunsBeforeAutomaticShellResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX command to exercise the deterministic shell guard")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(workspaces, t.TempDir())
	manager.resolver.goos = "windows"
	manager.resolver.getenv = func(string) string { return "" }
	manager.resolver.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	_, err = manager.Exec(context.Background(), item.ID, "unset CM_TOOL_CONTEXT")
	if err == nil {
		t.Fatal("deterministically denied command was accepted")
	}
	if errors.Is(err, ErrShellUnavailable) {
		t.Fatalf("shell resolution ran before deterministic guard: %v", err)
	}
}

func TestBackgroundSecurityGuardRunsBeforeAutomaticShellResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX command to exercise the deterministic shell guard")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	workspaces := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(workspaces, t.TempDir())
	manager.resolver.goos = "windows"
	manager.resolver.getenv = func(string) string { return "" }
	manager.resolver.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	processes := NewProcessManager(workspaces, manager)
	_, err = processes.Start(context.Background(), item.ID, "unset CM_TOOL_CONTEXT")
	if err == nil {
		t.Fatal("deterministically denied background command was accepted")
	}
	if errors.Is(err, ErrShellUnavailable) {
		t.Fatalf("background shell resolution ran before deterministic guard: %v", err)
	}
}

func writeProviderFixture(t *testing.T, name string, mode os.FileMode, content string) string {
	t.Helper()
	return writeProviderFixtureAt(t, filepath.Join(t.TempDir(), name), mode, content)
}

func writeProviderFixtureAt(t *testing.T, path string, mode os.FileMode, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}
