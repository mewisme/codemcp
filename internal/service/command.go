package service

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func runCommand(name string, args ...string) (string, error) {
	return runCommandObserver(nil, name, args...)
}

func runCommandObserver(observer tracepkg.Observer, name string, args ...string) (string, error) {
	executable := name
	if resolved, err := exec.LookPath(name); err == nil {
		executable = resolved
	}
	span := tracepkg.StartObserver(observer, "SERVICE", "service.process.exec", "Executing managed service command", tracepkg.String("executable", executable), tracepkg.Any("args", sanitizeServiceCommandArgs(args)))
	command := exec.Command(name, args...) // #nosec G204 -- service backend commands are selected by internal platform adapters.
	configureCommand(command)
	output, err := command.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		span.FailMessage("Managed service command failed", errors.New("external command failed"), tracepkg.Int("exit_code", exitCode), tracepkg.Int64("output_bytes", int64(len(output))))
		if text != "" {
			return text, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, text)
		}
		return text, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	span.EndMessage("Managed service command completed", tracepkg.Int("exit_code", 0), tracepkg.Int64("output_bytes", int64(len(output))))
	return text, nil
}

func commandSucceeded(name string, args ...string) (string, bool) {
	output, err := runCommand(name, args...)
	return output, err == nil
}

func commandSucceededObserver(observer tracepkg.Observer, name string, args ...string) (string, bool) {
	output, err := runCommandObserver(observer, name, args...)
	return output, err == nil
}

func sanitizeServiceCommandArgs(args []string) []string {
	result := append([]string(nil), args...)
	redactNext := false
	for index, arg := range result {
		lower := strings.ToLower(strings.TrimSpace(arg))
		if redactNext {
			result[index] = "<redacted>"
			redactNext = false
			continue
		}
		for _, fragment := range []string{"password", "secret", "token", "api-key", "api_key", "apikey"} {
			if strings.Contains(lower, fragment) {
				if strings.Contains(arg, "=") {
					key, _, _ := strings.Cut(arg, "=")
					result[index] = key + "=<redacted>"
				} else {
					redactNext = true
				}
				break
			}
		}
	}
	return result
}
