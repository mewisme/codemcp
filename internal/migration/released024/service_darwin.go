//go:build darwin

package released024

import (
	"context"
	"encoding/xml"
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
		label := "me.mewis." + id
		path := filepath.Join(string(filepath.Separator), "Library", "LaunchDaemons", label+".plist")
		domain := "system"
		if scope == "user" {
			path = filepath.Join(descriptor.OperatorHome, "Library", "LaunchAgents", label+".plist")
			domain = "gui/" + strconv.Itoa(os.Getuid())
		}
		state := ServiceState{Scope: scope, ID: id, Backend: "launchd", DefinitionPath: path, Ownership: OwnershipAbsent, ConfigRoot: descriptor.Root}
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			state.Installed = true
			args := releasedPlistArgs(data)
			if releasedServiceArgsOwned(args, descriptor) {
				state.Ownership = OwnershipVerified
				state.Reason = "historical launchd definition references the released root and executable"
				state.Launcher = filepath.Clean(args[0])
				state.Binary = state.Launcher
			} else {
				state.Ownership = OwnershipAmbiguous
				state.Reason = "historical launchd identity exists but definition ownership is not verified"
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, err
		}
		if _, err := exec.LookPath("launchctl"); err == nil {
			output, ok := runReadOnlyDarwin(ctx, "launchctl", "print", domain+"/"+label)
			state.Bootstrapped = ok
			state.Enabled = ok
			state.Running = ok && strings.Contains(output, "state = running")
			if ok {
				for _, line := range strings.Split(output, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "pid = ") {
						state.PID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
					}
				}
			}
		}
		result = append(result, state)
	}
	return result, nil
}

func releasedPlistArgs(data []byte) []string {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	wantArray, inArguments := false, false
	args := []string{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return args
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "key":
				var key string
				if err := decoder.DecodeElement(&key, &value); err != nil {
					return args
				}
				wantArray = strings.TrimSpace(key) == "ProgramArguments"
			case "array":
				if wantArray {
					inArguments, wantArray = true, false
				}
			case "string":
				if inArguments {
					var argument string
					if err := decoder.DecodeElement(&argument, &value); err != nil {
						return args
					}
					args = append(args, argument)
				}
			}
		case xml.EndElement:
			if value.Name.Local == "array" && inArguments {
				return args
			}
		}
	}
}

func releasedServiceArgsOwned(args []string, descriptor SourceDescriptor) bool {
	if len(args) == 0 || filepath.Base(args[0]) != descriptor.BinaryName {
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

func runReadOnlyDarwin(ctx context.Context, command string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.Output()
	return string(output), err == nil
}
