//go:build linux

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/install"
	updatepkg "go.mewis.me/codemcp/internal/update"
)

func TestApplyLinuxPackageUpgradeOrdersVerifiedDownloadBeforeMutation(t *testing.T) {
	cmd := &cobra.Command{}
	events := []string{}
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true, ServiceScope: "user"}}
	ops := linuxPackageUpgradeOps{
		resolve: func(context.Context, string, updatepkg.ArtifactKind) (updatepkg.PackageRelease, error) {
			events = append(events, "resolve")
			return updatepkg.PackageRelease{Version: "v1.2.3", Kind: updatepkg.ArtifactDebian}, nil
		},
		download: func(context.Context, updatepkg.PackageRelease) (updatepkg.PackageArtifact, error) {
			events = append(events, "download-verify")
			return updatepkg.PackageArtifact{Path: "/tmp/package.deb", Warnings: []string{"signature warning"}}, nil
		},
		selectInstall: func(install.Method, string) (linuxPackageInstallCommand, error) {
			events = append(events, "select-installer")
			return linuxPackageInstallCommand{}, nil
		},
		capture: func(context.Context) (updateRuntimeState, error) {
			events = append(events, "capture-runtime")
			return state, nil
		},
		validate: func(context.Context, install.Detection, updateRuntimeState) error {
			events = append(events, "validate-runtime")
			return nil
		},
		stop: func(context.Context, *cobra.Command, install.Detection, updateRuntimeState) error {
			events = append(events, "stop")
			return nil
		},
		install: func(context.Context, *cobra.Command, linuxPackageInstallCommand) error {
			events = append(events, "package-install")
			return nil
		},
		verify: func(context.Context, string) (string, string, error) {
			events = append(events, "version")
			return "/usr/bin/cm", "v1.2.3", nil
		},
		postinstall: func(context.Context, *cobra.Command, string) error {
			events = append(events, "postinstall")
			return nil
		},
		restart: func(context.Context, *cobra.Command, string, updateRuntimeState) error {
			events = append(events, "restart")
			return nil
		},
	}
	installed, warnings, err := applyLinuxPackageUpgrade(cmd, install.Detection{Method: install.MethodDebian, Executable: "/usr/bin/cm"}, "v1.2.3", updatepkg.ArtifactDebian, false, ops)
	if err != nil {
		t.Fatal(err)
	}
	if installed != "v1.2.3" || len(warnings) != 1 {
		t.Fatalf("installed=%q warnings=%#v", installed, warnings)
	}
	want := "resolve|download-verify|select-installer|capture-runtime|validate-runtime|stop|package-install|version|postinstall|restart"
	if got := strings.Join(events, "|"); got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
}

func TestApplyLinuxPackageUpgradeFailureBeforeReplacementDoesNotStopRuntime(t *testing.T) {
	cmd := &cobra.Command{}
	events := []string{}
	ops := linuxPackageUpgradeOps{
		resolve: func(context.Context, string, updatepkg.ArtifactKind) (updatepkg.PackageRelease, error) {
			events = append(events, "resolve")
			return updatepkg.PackageRelease{Version: "v1.2.3"}, nil
		},
		download: func(context.Context, updatepkg.PackageRelease) (updatepkg.PackageArtifact, error) {
			events = append(events, "download")
			return updatepkg.PackageArtifact{}, errors.New("checksum mismatch")
		},
		selectInstall: func(install.Method, string) (linuxPackageInstallCommand, error) {
			t.Fatal("installer selection ran")
			return linuxPackageInstallCommand{}, nil
		},
		capture: func(context.Context) (updateRuntimeState, error) {
			t.Fatal("runtime capture ran")
			return updateRuntimeState{}, nil
		},
		validate: func(context.Context, install.Detection, updateRuntimeState) error {
			t.Fatal("runtime validation ran")
			return nil
		},
		stop: func(context.Context, *cobra.Command, install.Detection, updateRuntimeState) error {
			t.Fatal("runtime stop ran")
			return nil
		},
		install: func(context.Context, *cobra.Command, linuxPackageInstallCommand) error {
			t.Fatal("package install ran")
			return nil
		},
		verify: func(context.Context, string) (string, string, error) {
			t.Fatal("version check ran")
			return "", "", nil
		},
		postinstall: func(context.Context, *cobra.Command, string) error { t.Fatal("postinstall ran"); return nil },
		restart: func(context.Context, *cobra.Command, string, updateRuntimeState) error {
			t.Fatal("restart ran")
			return nil
		},
	}
	if _, _, err := applyLinuxPackageUpgrade(cmd, install.Detection{Method: install.MethodDebian, Executable: "/usr/bin/cm"}, "v1.2.3", updatepkg.ArtifactDebian, false, ops); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error=%v", err)
	}
	if got := strings.Join(events, "|"); got != "resolve|download" {
		t.Fatalf("events=%q", got)
	}
}

func TestValidateLinuxPackageRuntimeStateRejectsForegroundRuntime(t *testing.T) {
	err := validateLinuxPackageRuntimeState(t.Context(), install.Detection{Method: install.MethodDebian, Executable: "/usr/bin/cm"}, updateRuntimeState{
		Running: true,
		Status:  runtimeStatusResult{PID: 4242, Managed: false},
	})
	if err == nil || !strings.Contains(err.Error(), "foreground runtime") {
		t.Fatalf("error=%v", err)
	}
}

func TestApplyLinuxPackageUpgradeInstallFailureRestoresManagedRuntime(t *testing.T) {
	cmd := &cobra.Command{}
	events := []string{}
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true, ServiceScope: "system"}}
	ops := successfulLinuxPackageUpgradeOps(&events, state)
	ops.install = func(context.Context, *cobra.Command, linuxPackageInstallCommand) error {
		events = append(events, "package-install")
		return errors.New("package manager failed")
	}
	_, _, err := applyLinuxPackageUpgrade(cmd, install.Detection{Method: install.MethodRPM, Executable: "/usr/bin/cm"}, "v1.2.3", updatepkg.ArtifactRPM, false, ops)
	if err == nil || !strings.Contains(err.Error(), "package manager failed") {
		t.Fatalf("error=%v", err)
	}
	if got := strings.Join(events, "|"); got != "resolve|download-verify|select-installer|capture-runtime|validate-runtime|stop|package-install|restart" {
		t.Fatalf("events=%q", got)
	}
}

func TestApplyLinuxPackageUpgradeNoRestartLeavesManagedRuntimeStoppedAfterSuccess(t *testing.T) {
	cmd := &cobra.Command{}
	events := []string{}
	state := updateRuntimeState{Running: true, Status: runtimeStatusResult{Managed: true}}
	ops := successfulLinuxPackageUpgradeOps(&events, state)
	if _, _, err := applyLinuxPackageUpgrade(cmd, install.Detection{Method: install.MethodDebian, Executable: "/usr/bin/cm"}, "v1.2.3", updatepkg.ArtifactDebian, true, ops); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(events, "|"), "restart") {
		t.Fatalf("events=%#v", events)
	}
}

func successfulLinuxPackageUpgradeOps(events *[]string, state updateRuntimeState) linuxPackageUpgradeOps {
	return linuxPackageUpgradeOps{
		resolve: func(context.Context, string, updatepkg.ArtifactKind) (updatepkg.PackageRelease, error) {
			*events = append(*events, "resolve")
			return updatepkg.PackageRelease{Version: "v1.2.3"}, nil
		},
		download: func(context.Context, updatepkg.PackageRelease) (updatepkg.PackageArtifact, error) {
			*events = append(*events, "download-verify")
			return updatepkg.PackageArtifact{Path: "/tmp/package"}, nil
		},
		selectInstall: func(install.Method, string) (linuxPackageInstallCommand, error) {
			*events = append(*events, "select-installer")
			return linuxPackageInstallCommand{}, nil
		},
		capture: func(context.Context) (updateRuntimeState, error) {
			*events = append(*events, "capture-runtime")
			return state, nil
		},
		validate: func(context.Context, install.Detection, updateRuntimeState) error {
			*events = append(*events, "validate-runtime")
			return nil
		},
		stop: func(context.Context, *cobra.Command, install.Detection, updateRuntimeState) error {
			*events = append(*events, "stop")
			return nil
		},
		install: func(context.Context, *cobra.Command, linuxPackageInstallCommand) error {
			*events = append(*events, "package-install")
			return nil
		},
		verify: func(context.Context, string) (string, string, error) {
			*events = append(*events, "version")
			return "/usr/bin/cm", "v1.2.3", nil
		},
		postinstall: func(context.Context, *cobra.Command, string) error {
			*events = append(*events, "postinstall")
			return nil
		},
		restart: func(context.Context, *cobra.Command, string, updateRuntimeState) error {
			*events = append(*events, "restart")
			return nil
		},
	}
}

func TestResolveLinuxPackageInstallCommandUsesSupportedToolingAndSudo(t *testing.T) {
	kind, _, ok := linuxPackageKind(install.MethodDebian)
	if !ok {
		t.Fatal("debian kind unavailable")
	}
	name, err := updatepkg.ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	lookup := func(name string) (string, error) {
		switch name {
		case "apt-get":
			return "/usr/bin/apt-get", nil
		case "sudo":
			return "/usr/bin/sudo", nil
		default:
			return "", errors.New("not found")
		}
	}
	command, err := resolveLinuxPackageInstallCommandWith(install.MethodDebian, path, lookup, func(string) bool { return true }, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !command.Elevated || command.Executable != "/usr/bin/sudo" || strings.Join(command.Args, "|") != "/usr/bin/apt-get|install|"+path {
		t.Fatalf("command=%#v", command)
	}
	if err := validateLinuxPackageInstallCommand(command); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLinuxPackageInstallCommandFallsBackWithinFamily(t *testing.T) {
	tests := []struct {
		name   string
		method install.Method
		tool   string
		args   []string
	}{
		{name: "apt", method: install.MethodDebian, tool: "apt", args: []string{"install"}},
		{name: "dpkg", method: install.MethodDebian, tool: "dpkg", args: []string{"-i"}},
		{name: "dnf", method: install.MethodRPM, tool: "dnf", args: []string{"install"}},
		{name: "yum", method: install.MethodRPM, tool: "yum", args: []string{"localinstall"}},
		{name: "rpm", method: install.MethodRPM, tool: "rpm", args: []string{"-U"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, _, _ := linuxPackageKind(test.method)
			name, err := updatepkg.ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), name)
			lookup := func(name string) (string, error) {
				if name == test.tool {
					return "/usr/bin/" + test.tool, nil
				}
				return "", errors.New("not found")
			}
			command, err := resolveLinuxPackageInstallCommandWith(test.method, path, lookup, func(string) bool { return true }, 0)
			if err != nil {
				t.Fatal(err)
			}
			wantArgs := append(append([]string(nil), test.args...), path)
			if command.Elevated || command.Executable != "/usr/bin/"+test.tool || strings.Join(command.Args, "|") != strings.Join(wantArgs, "|") {
				t.Fatalf("command=%#v want args=%#v", command, wantArgs)
			}
			if err := validateLinuxPackageInstallCommand(command); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResolveLinuxPackageInstallCommandRejectsUntrustedPackageTool(t *testing.T) {
	kind, _, _ := linuxPackageKind(install.MethodDebian)
	name, err := updatepkg.ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	lookup := func(name string) (string, error) {
		if name == "apt-get" {
			return "/tmp/fake-apt-get", nil
		}
		return "", errors.New("not found")
	}
	if _, err := resolveLinuxPackageInstallCommandWith(install.MethodDebian, path, lookup, func(string) bool { return false }, 1000); err == nil || !strings.Contains(err.Error(), "no supported local package installer") {
		t.Fatalf("error=%v", err)
	}
}

func TestLinuxPackageRuntimeArgsPreserveSystemScope(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	user := linuxPackageRuntimeArgs("down", updateRuntimeState{Running: true, Status: runtimeStatusResult{ServiceScope: "user"}})
	system := linuxPackageRuntimeArgs("up", updateRuntimeState{Running: true, Status: runtimeStatusResult{ServiceScope: "system"}})
	if strings.Contains(strings.Join(user, "|"), "--system") {
		t.Fatalf("user args=%#v", user)
	}
	if len(system) == 0 || system[len(system)-1] != "--system" {
		t.Fatalf("system args=%#v", system)
	}
}

func TestLinuxPackagePostinstallPreservesInstallIntegrationEnv(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	t.Setenv(application.InstallIntegrationsEnv, "0")
	root := t.TempDir()
	marker := filepath.Join(root, "env.txt")
	t.Setenv("TEST_INSTALL_ENV_MARKER", marker)
	binary := filepath.Join(root, "cm")
	script := "#!/bin/sh\nprintf '%s' \"${CM_INSTALL_INTEGRATIONS-<unset>}\" >\"$TEST_INSTALL_ENV_MARKER\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := postinstallLinuxPackageRuntime(t.Context(), cmd, binary); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "0" {
		t.Fatalf("child postinstall env=%q", value)
	}
}

func TestRunLinuxPackageInstallCommandBoundsFailureOutput(t *testing.T) {
	kind, _, _ := linuxPackageKind(install.MethodDebian)
	name, err := updatepkg.ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	packagePath := filepath.Join(root, name)
	if err := os.WriteFile(packagePath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "apt-get")
	script := "#!/bin/sh\nhead -c 100000 /dev/zero | tr '\\0' x >&2\nexit 1\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	command := linuxPackageInstallCommand{Method: install.MethodDebian, Tool: "apt-get", Executable: tool, Args: []string{"install", packagePath}, PackagePath: packagePath}
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err = runLinuxPackageInstallCommand(t.Context(), cmd, command)
	if err == nil || len(err.Error()) > maxLinuxPackageCommandOutput+1024 {
		t.Fatalf("bounded error length=%d error=%v", len(err.Error()), err)
	}
}

func TestLinuxPackageExactVersionGuidanceIsTagPinned(t *testing.T) {
	err := linuxPackageExactVersionGuidance(install.MethodDebian, "1.2.3")
	if err == nil {
		t.Fatal("expected manual exact-version guidance")
	}
	message := err.Error()
	if !strings.Contains(message, "/releases/download/v1.2.3/codemcp_linux_"+runtime.GOARCH+".deb") ||
		!strings.Contains(message, "/releases/download/v1.2.3/"+updatepkg.ChecksumName) {
		t.Fatalf("guidance=%q", message)
	}
}

func TestEnsureLinuxPackageUpgradeUserContextRejectsWholeCommandSudo(t *testing.T) {
	if err := ensureLinuxPackageUpgradeUserContext(0, "mew"); err == nil || !strings.Contains(err.Error(), "request sudo only for the package replacement") {
		t.Fatalf("sudo invocation error=%v", err)
	}
	for _, test := range []struct {
		euid     int
		sudoUser string
	}{
		{euid: 1000, sudoUser: ""},
		{euid: 1000, sudoUser: "mew"},
		{euid: 0, sudoUser: ""},
		{euid: 0, sudoUser: "root"},
	} {
		if err := ensureLinuxPackageUpgradeUserContext(test.euid, test.sudoUser); err != nil {
			t.Fatalf("euid=%d sudo_user=%q error=%v", test.euid, test.sudoUser, err)
		}
	}
}
