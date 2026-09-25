package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/logger"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/version"
)

type packageManagerRunFunc func(context.Context, updatepkg.PackageManagerCommand) (string, error)
type packageBinaryLookupFunc func(string) (string, error)
type packageBinaryVersionFunc func(context.Context, string) (string, error)

func runPackageManagedUpgrade(cmd *cobra.Command, detection install.Detection, targetVersion string, noRestart bool) error {
	plan, ok := updatepkg.PackageManagerPlanFor(detection.Method)
	if !ok {
		return fmt.Errorf("package manager update plan unavailable for %s", detection.Method)
	}
	if strings.TrimSpace(targetVersion) != "" {
		return fmt.Errorf("--version is unavailable for %s installations; package-manager upgrades follow the latest published manifest", plan.Name)
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
		renderMutationResult(cmd, "Upgrade CodeMCP", presentation.StatusInfo, "Already up to date",
			presentation.Field{Label: "current", Value: check.Current},
			presentation.Field{Label: "latest", Value: check.Latest},
		)
		return nil
	case updatepkg.StatusAhead:
		renderMutationResult(cmd, "Upgrade CodeMCP", presentation.StatusInfo, "Current version is newer than the latest release",
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

	logCommandStep(cmd, "UPDATE", "update.runtime.inspecting", "Inspecting managed runtime state")
	runtimeState, err := captureUpdateRuntimeState(cmd.Context())
	if err != nil {
		return fmt.Errorf("inspect managed runtime before update: %w", err)
	}
	handoff, err := preparePackageUpgradeHandoff(plan, check.Latest, config.RootPath(), runtimeState, noRestart)
	if err != nil {
		return fmt.Errorf("prepare %s update handoff: %w", plan.Name, err)
	}
	if err := launchPackageUpgradeHandoff(handoff); err != nil {
		_ = os.Remove(handoff.ScriptPath)
		return fmt.Errorf("launch %s update handoff: %w", plan.Name, err)
	}
	renderMutationSuccess(cmd, "Upgrade CodeMCP", plan.Name+" update handed off",
		presentation.Field{Label: "target", Value: check.Latest},
		presentation.Field{Label: "log", Value: handoff.LogPath},
		presentation.Field{Label: "state", Value: "update continues after this process exits"},
	)
	return nil
}

func runPackageManagerPhase(cmd *cobra.Command, log *logger.Logger, plan updatepkg.PackageManagerPlan, phase string, run packageManagerRunFunc) error {
	command := plan.Refresh
	message := "Refreshing " + plan.Name + " metadata"
	done := plan.Name + " metadata refreshed"
	event := "update.package.refresh"
	if phase == "apply" {
		command = plan.Apply
		message = "Applying update via " + plan.Name
		done = plan.Name + " update command completed"
		event = "update.package.apply"
	}
	progress := newCommandProgress(cmd, "UPDATE")
	progress.Start(event, message, done)
	output, err := run(cmd.Context(), command)
	if strings.TrimSpace(output) != "" {
		logCommandDebug(cmd, "UPDATE", event+".output", plan.Name+" command output", logger.WithDebug("output", strings.TrimSpace(output)))
	}
	if err != nil {
		progress.Stop()
		return fmt.Errorf("%s: %w", strings.ToLower(message), err)
	}
	progress.Complete()
	return nil
}

func runPackageManagerCommand(ctx context.Context, command updatepkg.PackageManagerCommand) (string, error) {
	key := command.Name + "\x00" + strings.Join(command.Args, "\x00")
	var process *exec.Cmd
	switch key {
	case "brew\x00update":
		process = exec.CommandContext(ctx, "brew", "update")
	case "brew\x00upgrade\x00--cask\x00" + updatepkg.HomebrewCask:
		process = exec.CommandContext(ctx, "brew", "upgrade", "--cask", updatepkg.HomebrewCask)
	case "scoop\x00update":
		if runtime.GOOS == "windows" {
			return runScoopPowerShell(ctx, false)
		}
		process = exec.CommandContext(ctx, "scoop", "update")
	case "scoop\x00update\x00" + updatepkg.ScoopPackage:
		if runtime.GOOS == "windows" {
			return runScoopPowerShell(ctx, true)
		}
		process = exec.CommandContext(ctx, "scoop", "update", updatepkg.ScoopPackage)
	default:
		return "", fmt.Errorf("unsupported package manager command: %s %s", command.Name, strings.Join(command.Args, " "))
	}
	return packageCommandOutput(process)
}

func runScoopPowerShell(ctx context.Context, apply bool) (string, error) {
	shell, err := packagePowerShell()
	if err != nil {
		return "", err
	}
	var process *exec.Cmd
	switch shell {
	case "pwsh":
		if apply {
			process = exec.CommandContext(ctx, "pwsh", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "& scoop update "+updatepkg.ScoopPackage)
		} else {
			process = exec.CommandContext(ctx, "pwsh", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "& scoop update")
		}
	case "powershell.exe":
		if apply {
			process = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "& scoop update "+updatepkg.ScoopPackage)
		} else {
			process = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "& scoop update")
		}
	case "powershell":
		if apply {
			process = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "& scoop update "+updatepkg.ScoopPackage)
		} else {
			process = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "& scoop update")
		}
	default:
		return "", errors.New("PowerShell is required to run Scoop")
	}
	return packageCommandOutput(process)
}

func packageCommandOutput(process *exec.Cmd) (string, error) {
	output, err := process.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err == nil {
		return text, nil
	}
	if text == "" {
		return "", err
	}
	return text, fmt.Errorf("%w: %s", err, text)
}

func packagePowerShell() (string, error) {
	for _, name := range []string{"pwsh", "powershell.exe", "powershell"} {
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		}
	}
	return "", errors.New("PowerShell is required to run Scoop")
}

func verifyPackageManagedVersion(ctx context.Context, target string, lookup packageBinaryLookupFunc, readVersion packageBinaryVersionFunc) (string, string, error) {
	target, err := updatepkg.NormalizeVersion(target)
	if err != nil {
		return "", "", err
	}
	var binary, commandName string
	for _, name := range []string{"cm"} {
		binary, err = lookup(name)
		if err == nil {
			commandName = name
			break
		}
	}
	if err != nil {
		return "", "", errors.New("updated cm command was not found on PATH")
	}
	output, err := readVersion(ctx, commandName)
	if err != nil {
		return binary, "", err
	}
	installed, err := packageVersionFromOutput(output)
	if err != nil {
		return binary, "", err
	}
	comparison, err := updatepkg.CompareVersions(installed, target)
	if err != nil {
		return binary, installed, err
	}
	if comparison < 0 {
		return binary, installed, fmt.Errorf("package metadata did not install %s; installed version is %s", target, installed)
	}
	return binary, installed, nil
}

func runPackageBinaryVersion(ctx context.Context, commandName string) (string, error) {
	var process *exec.Cmd
	switch commandName {
	case "cm":
		process = exec.CommandContext(ctx, "cm", "--version")
	default:
		return "", fmt.Errorf("unsupported package binary: %s", commandName)
	}
	return packageCommandOutput(process)
}

func packageVersionFromOutput(output string) (string, error) {
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, "()[]{}<>,;:\"")
		if version, err := updatepkg.NormalizeVersion(candidate); err == nil {
			return version, nil
		}
	}
	return "", fmt.Errorf("unable to parse installed version from %q", strings.TrimSpace(output))
}
