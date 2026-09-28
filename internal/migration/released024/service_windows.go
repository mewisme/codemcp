//go:build windows

package released024

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

func inspectPlatformServices(ctx context.Context, descriptor SourceDescriptor) ([]ServiceState, error) {
	result := make([]ServiceState, 0, 2)
	for _, scope := range []string{"user", "system"} {
		id := historicalServiceID(descriptor.Root, scope)
		launcher := filepath.Join(descriptor.Root, ".service-launcher-"+id+".vbs")
		state := ServiceState{
			Scope: scope, ID: id, Backend: "task-scheduler", DefinitionPath: launcher,
			Launcher: launcher, ConfigRoot: descriptor.Root, Ownership: OwnershipAbsent,
		}
		data, err := os.ReadFile(launcher)
		switch {
		case err == nil:
			state.Installed = true
			args := releasedWindowsLauncherArgs(data)
			if releasedServiceArgsOwned(args, descriptor) {
				state.Ownership = OwnershipVerified
				state.Reason = "historical task launcher references the released root and executable"
			} else {
				state.Ownership = OwnershipAmbiguous
				state.Reason = "historical task identity exists but launcher ownership is not verified"
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, err
		}
		if _, err := exec.LookPath("schtasks.exe"); err == nil {
			cmd := exec.CommandContext(ctx, "schtasks.exe", "/Query", "/TN", id, "/FO", "LIST", "/V")
			output, err := cmd.Output()
			if err == nil {
				state.Bootstrapped, state.Installed, state.Enabled = true, true, true
				state.Running = strings.Contains(strings.ToLower(string(output)), "status:") && strings.Contains(strings.ToLower(string(output)), "running")
			}
		}
		result = append(result, state)
	}
	return result, nil
}

func releasedWindowsLauncherArgs(data []byte) []string {
	text := decodeReleasedWindowsLauncher(data)
	start := strings.Index(text, "shell.Run(")
	if start < 0 {
		return nil
	}
	rest := text[start+len("shell.Run("):]
	end := strings.Index(rest, ", 0, True)")
	if end < 0 {
		return nil
	}
	literal := strings.TrimSpace(rest[:end])
	if len(literal) < 2 || literal[0] != '"' || literal[len(literal)-1] != '"' {
		return nil
	}
	command := strings.ReplaceAll(literal[1:len(literal)-1], `""`, `"`)
	return parseReleasedWindowsCommandLine(command)
}

func decodeReleasedWindowsLauncher(data []byte) string {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xfe {
		return string(data)
	}
	words := make([]uint16, 0, (len(data)-2)/2)
	for index := 2; index+1 < len(data); index += 2 {
		words = append(words, uint16(data[index])|uint16(data[index+1])<<8)
	}
	return string(utf16.Decode(words))
}

func parseReleasedWindowsCommandLine(command string) []string {
	args := []string{}
	for index := 0; index < len(command); {
		for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
			index++
		}
		if index >= len(command) {
			break
		}
		var value strings.Builder
		quoted := false
		for index < len(command) {
			if !quoted && (command[index] == ' ' || command[index] == '\t') {
				break
			}
			if command[index] == '"' {
				quoted = !quoted
				index++
				continue
			}
			if command[index] != '\\' {
				value.WriteByte(command[index])
				index++
				continue
			}
			start := index
			for index < len(command) && command[index] == '\\' {
				index++
			}
			count := index - start
			if index < len(command) && command[index] == '"' {
				value.WriteString(strings.Repeat(`\`, count/2))
				if count%2 == 0 {
					quoted = !quoted
				} else {
					value.WriteByte('"')
				}
				index++
				continue
			}
			value.WriteString(strings.Repeat(`\`, count))
		}
		args = append(args, value.String())
	}
	return args
}

func releasedServiceArgsOwned(args []string, descriptor SourceDescriptor) bool {
	if len(args) == 0 || !strings.EqualFold(filepath.Base(args[0]), descriptor.BinaryName) {
		return false
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
			serviceRun = index+1 < len(args) && args[index+1] == "run"
		}
	}
	return serviceRun && comparablePath(configRoot) == comparablePath(descriptor.Root)
}
