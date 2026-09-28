//go:build darwin

package released024

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type platformHistoricalServiceController struct{}

func (platformHistoricalServiceController) Quiesce(ctx context.Context, _ SourceDescriptor, state ServiceState) error {
	if err := verifyHistoricalServiceDefinitionDarwin(state); err != nil {
		return err
	}
	target, _ := historicalLaunchdTarget(state)
	if _, err := runHistoricalCommand(ctx, "launchctl", "bootout", target); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := runHistoricalCommand(ctx, "launchctl", "print", target); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("historical service %s did not stop", state.ID)
}

func verifyHistoricalServiceDefinitionDarwin(state ServiceState) error {
	if strings.TrimSpace(state.DefinitionPath) == "" {
		return fmt.Errorf("historical service %s definition path is missing", state.ID)
	}
	info, err := os.Lstat(state.DefinitionPath)
	if err != nil {
		return fmt.Errorf("inspect historical service %s definition: %w", state.ID, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("historical service %s definition is not a regular non-symlink file", state.ID)
	}
	data, err := os.ReadFile(state.DefinitionPath)
	if err != nil {
		return err
	}
	args := releasedPlistArgs(data)
	descriptor := SourceDescriptor{Root: state.ConfigRoot, BinaryName: filepath.Base(state.Binary)}
	if !releasedServiceArgsOwned(args, descriptor) {
		return fmt.Errorf("historical service %s ownership changed before quiescence", state.ID)
	}
	if len(args) == 0 || !sameComparablePath(args[0], state.Binary) {
		return fmt.Errorf("historical service %s executable changed before quiescence", state.ID)
	}
	return nil
}

func (platformHistoricalServiceController) Restore(ctx context.Context, _ SourceDescriptor, state ServiceState) error {
	if !state.Bootstrapped && !state.Enabled && !state.Running {
		return nil
	}
	target, domain := historicalLaunchdTarget(state)
	if strings.TrimSpace(state.DefinitionPath) == "" {
		return fmt.Errorf("historical service %s definition path is missing", state.ID)
	}
	if _, err := runHistoricalCommand(ctx, "launchctl", "bootstrap", domain, state.DefinitionPath); err != nil {
		return err
	}
	if state.Running {
		_, err := runHistoricalCommand(ctx, "launchctl", "kickstart", "-k", target)
		return err
	}
	return nil
}

func historicalLaunchdTarget(state ServiceState) (string, string) {
	domain := "system"
	if state.Scope == "user" {
		domain = "gui/" + strconv.Itoa(os.Getuid())
	}
	return domain + "/me.mewis." + state.ID, domain
}

func runHistoricalCommand(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(output), nil
}
