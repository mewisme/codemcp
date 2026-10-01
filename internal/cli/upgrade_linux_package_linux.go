//go:build linux

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/install"
	managed "go.mewis.me/codemcp/internal/service"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/version"
)

const maxLinuxPackageCommandOutput = 16 << 10

type linuxPackageInstallCommand struct {
	Method      install.Method
	Tool        string
	Executable  string
	Args        []string
	PackagePath string
	Elevated    bool
}

type linuxPackageUpgradeOps struct {
	resolve       func(context.Context, string, updatepkg.ArtifactKind) (updatepkg.PackageRelease, error)
	download      func(context.Context, updatepkg.PackageRelease) (updatepkg.PackageArtifact, error)
	selectInstall func(install.Method, string) (linuxPackageInstallCommand, error)
	capture       func(context.Context) (updateRuntimeState, error)
	validate      func(context.Context, install.Detection, updateRuntimeState) error
	stop          func(context.Context, *cobra.Command, install.Detection, updateRuntimeState) error
	install       func(context.Context, *cobra.Command, linuxPackageInstallCommand) error
	verify        func(context.Context, string) (string, string, error)
	postinstall   func(context.Context, *cobra.Command, string) error
	restart       func(context.Context, *cobra.Command, string, updateRuntimeState) error
}

type boundedLinuxPackageOutput struct {
	mu   sync.Mutex
	data []byte
}

func (buffer *boundedLinuxPackageOutput) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := maxLinuxPackageCommandOutput - len(buffer.data)
	if remaining > 0 {
		if len(value) < remaining {
			remaining = len(value)
		}
		buffer.data = append(buffer.data, value[:remaining]...)
	}
	return len(value), nil
}

func (buffer *boundedLinuxPackageOutput) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}

func runLinuxPackageManagedUpgrade(cmd *cobra.Command, detection install.Detection, targetVersion string, noRestart bool) error {
	kind, managerName, ok := linuxPackageKind(detection.Method)
	if !ok {
		return fmt.Errorf("unsupported Linux package installation method %q", detection.Method)
	}
	if strings.TrimSpace(targetVersion) != "" {
		return linuxPackageExactVersionGuidance(detection.Method, targetVersion)
	}
	if err := ensureLinuxPackageUpgradeUserContext(os.Geteuid(), os.Getenv("SUDO_USER")); err != nil {
		return err
	}

	progress := newCommandProgress(cmd, "UPDATE")
	progress.Start("update.checking", "Checking for updates", "Checked for updates")
	checker := updatepkg.Checker{Source: updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version}}
	check, err := checker.Check(cmd.Context(), version.Version)
	if err != nil {
		progress.Stop()
		return fmt.Errorf("check update: %w", err)
	}
	progress.Complete()
	switch check.Status {
	case updatepkg.StatusUpToDate:
		renderMutationResult(cmd, presentation.StatusInfo, "Already up to date",
			presentation.Field{Label: "current", Value: check.Current},
			presentation.Field{Label: "latest", Value: check.Latest},
		)
		return nil
	case updatepkg.StatusAhead:
		renderMutationResult(cmd, presentation.StatusInfo, "Current version is newer than the latest release",
			presentation.Field{Label: "current", Value: check.Current},
			presentation.Field{Label: "latest", Value: check.Latest},
		)
		return nil
	case updatepkg.StatusDevelopment:
		return updatepkg.ErrDevelopmentUpdate
	case updatepkg.StatusAvailable:
	default:
		return fmt.Errorf("unknown update status %q", check.Status)
	}

	client := updatepkg.Client{UserAgent: version.ClientName + "/" + version.Version}
	downloader := updatepkg.Downloader{UserAgent: version.ClientName + "/" + version.Version}
	ops := linuxPackageUpgradeOps{
		resolve: func(ctx context.Context, target string, kind updatepkg.ArtifactKind) (updatepkg.PackageRelease, error) {
			return client.PackageVersion(ctx, target, kind)
		},
		download:      downloader.DownloadPackage,
		selectInstall: resolveLinuxPackageInstallCommand,
		capture:       captureUpdateRuntimeState,
		validate:      validateLinuxPackageRuntimeState,
		stop:          stopLinuxPackageRuntime,
		install:       runLinuxPackageInstallCommand,
		verify: func(ctx context.Context, target string) (string, string, error) {
			return verifyPackageManagedVersion(ctx, target, func(string) (string, error) {
				if strings.TrimSpace(detection.Executable) == "" {
					return "", errors.New("package-managed cm executable is unavailable")
				}
				return detection.Executable, nil
			}, runPackageBinaryVersion)
		},
		postinstall: postinstallLinuxPackageRuntime,
		restart:     restartLinuxPackageRuntime,
	}
	installed, warnings, err := applyLinuxPackageUpgrade(cmd, detection, check.Latest, kind, noRestart, ops)
	if err != nil {
		return fmt.Errorf("%s upgrade failed: %w", managerName, err)
	}
	session := commandProgressSession(cmd)
	for _, warning := range warnings {
		session.Warn("update.package.signature-warning", "Verifying release signature", warning)
	}
	renderMutationSuccess(cmd, managerName+" update complete",
		presentation.Field{Label: "previous", Value: check.Current},
		presentation.Field{Label: "current", Value: installed},
	)
	return nil
}

func ensureLinuxPackageUpgradeUserContext(euid int, sudoUser string) error {
	sudoUser = strings.TrimSpace(sudoUser)
	if euid == 0 && sudoUser != "" && sudoUser != "root" {
		return fmt.Errorf("run cm upgrade as your normal user %q; CodeMCP will request sudo only for the package replacement so postinstall reconciliation stays in the invoking-user context", sudoUser)
	}
	return nil
}

func applyLinuxPackageUpgrade(cmd *cobra.Command, detection install.Detection, target string, kind updatepkg.ArtifactKind, noRestart bool, ops linuxPackageUpgradeOps) (string, []string, error) {
	if cmd == nil {
		return "", nil, errors.New("command context is required")
	}
	if ops.resolve == nil || ops.download == nil || ops.selectInstall == nil || ops.capture == nil || ops.validate == nil ||
		ops.stop == nil || ops.install == nil || ops.verify == nil || ops.postinstall == nil || ops.restart == nil {
		return "", nil, errors.New("linux package upgrade operations are incomplete")
	}
	release, err := ops.resolve(cmd.Context(), target, kind)
	if err != nil {
		return "", nil, fmt.Errorf("resolve exact release package: %w", err)
	}
	if release.Version != target {
		return "", nil, fmt.Errorf("resolved package release %s does not match target %s", release.Version, target)
	}
	artifact, err := ops.download(cmd.Context(), release)
	if err != nil {
		return "", nil, fmt.Errorf("verify release package: %w", err)
	}
	defer artifact.Cleanup()

	installCommand, err := ops.selectInstall(detection.Method, artifact.Path)
	if err != nil {
		return "", artifact.Warnings, err
	}
	state, err := ops.capture(cmd.Context())
	if err != nil {
		return "", artifact.Warnings, fmt.Errorf("inspect runtime before package replacement: %w", err)
	}
	if err := ops.validate(cmd.Context(), detection, state); err != nil {
		return "", artifact.Warnings, err
	}

	stopped := false
	binaryForRestart := detection.Executable
	restore := func(cause error) error {
		if !stopped || noRestart {
			return cause
		}
		if restartErr := ops.restart(cmd.Context(), cmd, binaryForRestart, state); restartErr != nil {
			return fmt.Errorf("%w; managed runtime restore failed: %v", cause, restartErr)
		}
		return cause
	}
	if state.Running {
		if err := ops.stop(cmd.Context(), cmd, detection, state); err != nil {
			return "", artifact.Warnings, fmt.Errorf("stop managed runtime before package replacement: %w", err)
		}
		stopped = true
	}

	if err := ops.install(cmd.Context(), cmd, installCommand); err != nil {
		return "", artifact.Warnings, restore(fmt.Errorf("install verified package: %w", err))
	}
	binary, installed, err := ops.verify(cmd.Context(), target)
	if binary != "" {
		binaryForRestart = binary
	}
	if err != nil {
		return installed, artifact.Warnings, restore(fmt.Errorf("verify installed package: %w", err))
	}
	if err := ops.postinstall(cmd.Context(), cmd, binary); err != nil {
		return installed, artifact.Warnings, restore(fmt.Errorf("run postinstall reconciliation: %w", err))
	}
	if stopped && !noRestart {
		if err := ops.restart(cmd.Context(), cmd, binary, state); err != nil {
			return installed, artifact.Warnings, fmt.Errorf("restart managed runtime after package replacement: %w", err)
		}
	}
	return installed, artifact.Warnings, nil
}

func linuxPackageKind(method install.Method) (updatepkg.ArtifactKind, string, bool) {
	switch method {
	case install.MethodDebian:
		return updatepkg.ArtifactDebian, "Debian package", true
	case install.MethodRPM:
		return updatepkg.ArtifactRPM, "RPM package", true
	default:
		return "", "", false
	}
}

func linuxPackageExactVersionGuidance(method install.Method, target string) error {
	kind, managerName, ok := linuxPackageKind(method)
	if !ok {
		return fmt.Errorf("unsupported Linux package installation method %q", method)
	}
	version, err := updatepkg.NormalizeVersion(target)
	if err != nil {
		return err
	}
	name, err := updatepkg.ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	packageURL, err := updatepkg.ExactReleaseAssetURL(updatepkg.DefaultOwner, updatepkg.DefaultRepo, version, name)
	if err != nil {
		return err
	}
	checksumURL, err := updatepkg.ExactReleaseAssetURL(updatepkg.DefaultOwner, updatepkg.DefaultRepo, version, updatepkg.ChecksumName)
	if err != nil {
		return err
	}
	return fmt.Errorf("--version is unavailable for automatic %s upgrades; download %s, verify %s against %s, then install the local package with the system package manager", managerName, packageURL, name, checksumURL)
}

func resolveLinuxPackageInstallCommand(method install.Method, packagePath string) (linuxPackageInstallCommand, error) {
	return resolveLinuxPackageInstallCommandWith(method, packagePath, exec.LookPath, install.TrustedSystemExecutable, os.Geteuid())
}

func resolveLinuxPackageInstallCommandWith(method install.Method, packagePath string, lookup func(string) (string, error), trust func(string) bool, euid int) (linuxPackageInstallCommand, error) {
	if !filepath.IsAbs(packagePath) {
		return linuxPackageInstallCommand{}, errors.New("verified package path must be absolute")
	}
	kind, _, ok := linuxPackageKind(method)
	if !ok {
		return linuxPackageInstallCommand{}, fmt.Errorf("unsupported Linux package installation method %q", method)
	}
	expected, err := updatepkg.ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return linuxPackageInstallCommand{}, err
	}
	if filepath.Base(packagePath) != expected {
		return linuxPackageInstallCommand{}, fmt.Errorf("verified package path %q does not match expected artifact %q", packagePath, expected)
	}
	type candidate struct {
		tool string
		args []string
	}
	var candidates []candidate
	switch method {
	case install.MethodDebian:
		candidates = []candidate{
			{tool: "apt-get", args: []string{"install", packagePath}},
			{tool: "apt", args: []string{"install", packagePath}},
			{tool: "dpkg", args: []string{"-i", packagePath}},
		}
	case install.MethodRPM:
		candidates = []candidate{
			{tool: "dnf", args: []string{"install", packagePath}},
			{tool: "yum", args: []string{"localinstall", packagePath}},
			{tool: "rpm", args: []string{"-U", packagePath}},
		}
	}
	for _, candidate := range candidates {
		toolPath, err := lookup(candidate.tool)
		if err != nil || trust == nil || !trust(toolPath) {
			continue
		}
		command := linuxPackageInstallCommand{
			Method: method, Tool: candidate.tool, Executable: toolPath,
			Args: append([]string(nil), candidate.args...), PackagePath: packagePath,
		}
		if euid == 0 {
			return command, nil
		}
		sudoPath, err := lookup("sudo")
		if err != nil {
			return linuxPackageInstallCommand{}, fmt.Errorf("%s is available but root privileges are required and sudo was not found", candidate.tool)
		}
		command.Executable = sudoPath
		command.Args = append([]string{toolPath}, candidate.args...)
		command.Elevated = true
		return command, nil
	}
	return linuxPackageInstallCommand{}, fmt.Errorf("no supported local package installer is available for %s", method)
}

func validateLinuxPackageInstallCommand(command linuxPackageInstallCommand) error {
	kind, _, ok := linuxPackageKind(command.Method)
	if !ok {
		return fmt.Errorf("unsupported Linux package installation method %q", command.Method)
	}
	if !filepath.IsAbs(command.PackagePath) {
		return errors.New("package path must be absolute")
	}
	expected, err := updatepkg.ArtifactName(kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if filepath.Base(command.PackagePath) != expected {
		return fmt.Errorf("package path does not match %s", expected)
	}
	args := command.Args
	executable := command.Executable
	if command.Elevated {
		if filepath.Base(executable) != "sudo" || len(args) < 2 || !filepath.IsAbs(args[0]) {
			return errors.New("invalid elevated package command")
		}
		executable, args = args[0], args[1:]
	}
	tool := filepath.Base(executable)
	var valid bool
	switch command.Method {
	case install.MethodDebian:
		valid = tool == "apt-get" && sameArgs(args, "install", command.PackagePath) ||
			tool == "apt" && sameArgs(args, "install", command.PackagePath) ||
			tool == "dpkg" && sameArgs(args, "-i", command.PackagePath)
	case install.MethodRPM:
		valid = tool == "dnf" && sameArgs(args, "install", command.PackagePath) ||
			tool == "yum" && sameArgs(args, "localinstall", command.PackagePath) ||
			tool == "rpm" && sameArgs(args, "-U", command.PackagePath)
	}
	if !valid {
		return fmt.Errorf("unsupported local package install command: %s %s", tool, strings.Join(args, " "))
	}
	return nil
}

func sameArgs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func runLinuxPackageInstallCommand(ctx context.Context, cmd *cobra.Command, command linuxPackageInstallCommand) error {
	if err := validateLinuxPackageInstallCommand(command); err != nil {
		return err
	}
	process := exec.CommandContext(ctx, command.Executable, command.Args...)
	stdin, commandOut, commandErr := commandProcessIO(cmd)
	process.Stdin = stdin
	var stdout, stderr boundedLinuxPackageOutput
	process.Stdout = io.MultiWriter(commandOut, &stdout)
	process.Stderr = io.MultiWriter(commandErr, &stderr)
	if err := process.Run(); err != nil {
		detail := strings.TrimSpace(strings.Join([]string{stdout.String(), stderr.String()}, "\n"))
		if detail == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, detail)
	}
	return nil
}

func validateLinuxPackageRuntimeState(ctx context.Context, detection install.Detection, state updateRuntimeState) error {
	if !state.Running {
		return nil
	}
	if !state.Status.Managed {
		return fmt.Errorf("foreground runtime is using the package-managed binary (pid %d); stop it before upgrading", state.Status.PID)
	}
	if filepath.Clean(state.Status.ConfigRoot) != filepath.Clean(config.RootPath()) {
		return fmt.Errorf("managed runtime config root mismatch: runtime %s, selected %s", state.Status.ConfigRoot, config.RootPath())
	}
	scope := managed.Scope(state.Status.ServiceScope)
	if scope != managed.ScopeUser && scope != managed.ScopeSystem {
		return fmt.Errorf("managed runtime has invalid service scope %q", state.Status.ServiceScope)
	}
	account, err := managed.InvokingAccountContext(ctx, scope)
	if err != nil {
		return err
	}
	spec, err := managed.NewSpecContext(ctx, state.Status.ConfigRoot, detection.Executable, scope, account)
	if err != nil {
		return err
	}
	if state.Status.ServiceID == "" || state.Status.ServiceID != spec.ID {
		return fmt.Errorf("managed runtime service mismatch: runtime %s, expected %s", state.Status.ServiceID, spec.ID)
	}
	return nil
}

func stopLinuxPackageRuntime(ctx context.Context, cmd *cobra.Command, detection install.Detection, state updateRuntimeState) error {
	if !state.Running {
		return nil
	}
	args := linuxPackageRuntimeArgs("down", state)
	return runPackageLifecycleCommand(ctx, cmd, detection.Executable, args...)
}

func postinstallLinuxPackageRuntime(ctx context.Context, cmd *cobra.Command, binary string) error {
	return runPackageLifecycleCommand(ctx, cmd, binary, "--config-dir", config.RootPath(), "_service", "postinstall")
}

func restartLinuxPackageRuntime(ctx context.Context, cmd *cobra.Command, binary string, state updateRuntimeState) error {
	if !state.Running {
		return nil
	}
	return runPackageLifecycleCommand(ctx, cmd, binary, linuxPackageRuntimeArgs("up", state)...)
}

func linuxPackageRuntimeArgs(action string, state updateRuntimeState) []string {
	args := []string{"--config-dir", config.RootPath(), action}
	if state.Status.ServiceScope == string(managed.ScopeSystem) {
		args = append(args, "--system")
	}
	return args
}

func runPackageLifecycleCommand(ctx context.Context, cmd *cobra.Command, binary string, args ...string) error {
	if strings.TrimSpace(binary) == "" || filepath.Base(binary) != "cm" {
		return fmt.Errorf("invalid CodeMCP lifecycle binary %q", binary)
	}
	process := exec.CommandContext(ctx, binary, args...)
	process.Stdin, process.Stdout, process.Stderr = commandProcessIO(cmd)
	return process.Run()
}
