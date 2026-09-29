//go:build windows

package released024

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/service"
)

type platformHistoricalServiceController struct{}

func (platformHistoricalServiceController) Quiesce(ctx context.Context, _ SourceDescriptor, state ServiceState) error {
	if err := verifyHistoricalServiceDefinitionWindows(state); err != nil {
		return err
	}
	if state.Enabled || state.Bootstrapped {
		if _, err := runHistoricalTask(ctx, "/Change", "/TN", state.ID, "/DISABLE"); err != nil {
			return err
		}
	}
	if state.Running {
		if _, err := runHistoricalTask(ctx, "/End", "/TN", state.ID); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output, err := runHistoricalTask(ctx, "/Query", "/TN", state.ID, "/FO", "LIST", "/V")
		if err != nil || !taskOutputRunning(output) {
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

func verifyHistoricalServiceDefinitionWindows(state ServiceState) error {
	if strings.TrimSpace(state.DefinitionPath) == "" {
		return fmt.Errorf("historical service %s launcher path is missing", state.ID)
	}
	info, err := os.Lstat(state.DefinitionPath)
	if err != nil {
		return fmt.Errorf("inspect historical service %s launcher: %w", state.ID, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("historical service %s launcher is not a regular non-symlink file", state.ID)
	}
	data, err := os.ReadFile(state.DefinitionPath)
	if err != nil {
		return err
	}
	args := releasedWindowsLauncherArgs(data)
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
	if state.Enabled {
		if _, err := runHistoricalTask(ctx, "/Change", "/TN", state.ID, "/ENABLE"); err != nil {
			return err
		}
	} else if state.Bootstrapped {
		if _, err := runHistoricalTask(ctx, "/Change", "/TN", state.ID, "/DISABLE"); err != nil {
			return err
		}
	}
	if state.Running {
		if !state.Enabled {
			if _, err := runHistoricalTask(ctx, "/Change", "/TN", state.ID, "/ENABLE"); err != nil {
				return err
			}
		}
		if _, err := runHistoricalTask(ctx, "/Run", "/TN", state.ID); err != nil {
			return err
		}
		if !state.Enabled {
			_, err := runHistoricalTask(ctx, "/Change", "/TN", state.ID, "/DISABLE")
			return err
		}
	}
	return nil
}

func (platformHistoricalServiceController) Retire(ctx context.Context, source SourceDescriptor, state ServiceState) error {
	spec, err := historicalServiceSpec(ctx, source, state)
	if err != nil {
		return err
	}
	launcherExists := true
	if _, err := os.Lstat(state.DefinitionPath); err != nil {
		if os.IsNotExist(err) {
			launcherExists = false
		} else {
			return err
		}
	}
	query, queryErr := runHistoricalTask(ctx, "/Query", "/TN", state.ID, "/FO", "LIST", "/V")
	taskExists := queryErr == nil
	if !launcherExists && !taskExists {
		return nil
	}
	if !launcherExists && taskExists {
		return fmt.Errorf("historical service %s task remains but verified launcher is missing", state.ID)
	}
	if err := verifyHistoricalServiceDefinitionWindows(state); err != nil {
		return err
	}
	if taskExists {
		xmlText, xmlErr := runHistoricalTask(ctx, "/Query", "/TN", state.ID, "/XML")
		if xmlErr != nil {
			return xmlErr
		}
		normalizedXML := strings.ToLower(strings.ReplaceAll(html.UnescapeString(xmlText), `\`, "/"))
		normalizedLauncher := strings.ToLower(strings.ReplaceAll(filepath.Clean(state.DefinitionPath), `\`, "/"))
		if !strings.Contains(normalizedXML, normalizedLauncher) {
			return fmt.Errorf("historical service %s task ownership changed before retirement", state.ID)
		}
		if taskOutputRunning(query) {
			if _, err := runHistoricalTask(ctx, "/End", "/TN", state.ID); err != nil {
				return err
			}
		}
		if _, err := runHistoricalTask(ctx, "/Delete", "/TN", state.ID, "/F"); err != nil {
			return err
		}
	}
	if err := os.Remove(state.DefinitionPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	status, err := service.NewManager().Status(spec)
	if err != nil {
		return err
	}
	if status.Installed {
		return fmt.Errorf("historical service %s remained installed after retirement", state.ID)
	}
	return nil
}

func runHistoricalTask(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "schtasks.exe", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("schtasks %s: %w", strings.Join(args, " "), err)
	}
	return string(output), nil
}

func taskOutputRunning(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "status:") && strings.Contains(lower, "running")
}
