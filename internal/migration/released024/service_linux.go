//go:build linux

package released024

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func inspectPlatformServices(ctx context.Context, descriptor SourceDescriptor) ([]ServiceState, error) {
	result := make([]ServiceState, 0, 2)
	for _, scope := range []string{"user", "system"} {
		id := historicalServiceID(descriptor.Root, scope)
		path := filepath.Join(string(filepath.Separator), "etc", "systemd", "system", id+".service")
		argsPrefix := []string{}
		if scope == "user" {
			path = filepath.Join(descriptor.OperatorHome, ".config", "systemd", "user", id+".service")
			argsPrefix = []string{"--user"}
		}
		state := ServiceState{
			Scope: scope, ID: id, Backend: "systemd", DefinitionPath: path,
			Ownership: OwnershipAbsent, ConfigRoot: descriptor.Root,
		}
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			state.Installed = true
			definition := string(data)
			state.Ownership, state.Reason = inspectSystemdOwnership(definition, descriptor)
			state.Launcher = inspectSystemdLauncher(definition)
			state.Binary = state.Launcher
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, err
		}
		if _, err := exec.LookPath("systemctl"); err == nil {
			if output, ok := runReadOnly(ctx, "systemctl", append(argsPrefix, "show", id+".service", "--property=LoadState", "--value")...); ok {
				loaded := strings.TrimSpace(output)
				state.Bootstrapped = loaded != "" && loaded != "not-found"
				state.Installed = state.Installed || state.Bootstrapped
			}
			_, state.Enabled = runReadOnly(ctx, "systemctl", append(argsPrefix, "is-enabled", "--quiet", id+".service")...)
			_, state.Running = runReadOnly(ctx, "systemctl", append(argsPrefix, "is-active", "--quiet", id+".service")...)
			if output, ok := runReadOnly(ctx, "systemctl", append(argsPrefix, "show", id+".service", "--property=MainPID", "--value")...); ok {
				state.PID, _ = strconv.Atoi(strings.TrimSpace(output))
			}
		}
		if state.Installed && state.Ownership == OwnershipAbsent {
			state.Ownership = OwnershipAmbiguous
		}
		result = append(result, state)
	}
	return result, nil
}

func inspectSystemdOwnership(definition string, descriptor SourceDescriptor) (Ownership, string) {
	args := releasedSystemdArgs(definition)
	if len(args) == 0 || filepath.Base(args[0]) != descriptor.BinaryName {
		return OwnershipAmbiguous, "historical service identity exists but executable ownership is not verified"
	}
	configRoot, serviceRun := "", false
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--config-dir":
			if index+1 < len(args) {
				configRoot = args[index+1]
				index++
			}
		case "_service":
			if index+1 < len(args) && args[index+1] == "run" {
				serviceRun = true
			}
		}
	}
	if serviceRun && comparablePath(configRoot) == comparablePath(descriptor.Root) {
		return OwnershipVerified, "historical service definition references the released root and executable"
	}
	return OwnershipAmbiguous, "historical service identity exists but definition ownership is not verified"
}

func inspectSystemdLauncher(definition string) string {
	args := releasedSystemdArgs(definition)
	if len(args) == 0 {
		return ""
	}
	return filepath.Clean(args[0])
}

func releasedSystemdArgs(definition string) []string {
	command := ""
	for _, line := range strings.Split(definition, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExecStart=") {
			command = strings.TrimSpace(strings.TrimPrefix(line, "ExecStart="))
			break
		}
	}
	if command == "" {
		return nil
	}
	args := []string{}
	for index := 0; index < len(command); {
		for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
			index++
		}
		if index >= len(command) {
			break
		}
		var value strings.Builder
		quoted := command[index] == '"'
		if quoted {
			index++
		}
		for index < len(command) {
			char := command[index]
			if quoted && char == '"' {
				index++
				break
			}
			if !quoted && (char == ' ' || char == '\t') {
				break
			}
			if char == '\\' && index+1 < len(command) {
				index++
				char = command[index]
			}
			value.WriteByte(char)
			index++
		}
		args = append(args, value.String())
		for index < len(command) && command[index] != '"' && (command[index] == ' ' || command[index] == '\t') {
			index++
		}
	}
	return args
}

func runReadOnly(ctx context.Context, command string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.Output()
	return string(output), err == nil
}
