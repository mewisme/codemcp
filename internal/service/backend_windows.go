//go:build windows

package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type windowsManager struct{ trace tracepkg.Observer }

func NewManager() Manager { return windowsManager{} }
func NewManagerWithObserver(observer tracepkg.Observer) Manager {
	return windowsManager{trace: observer}
}
func (windowsManager) Backend() string { return "task-scheduler" }

func (m windowsManager) DefinitionMatches(spec Spec) (bool, error) {
	command, err := windowsTaskCommand()
	if err != nil {
		return false, err
	}
	launcher, err := os.ReadFile(windowsLauncherPath(spec))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(launcher) != string(windowsLauncherBytes(spec)) {
		return false, nil
	}
	output, ok := commandSucceededObserver(m.trace, "schtasks.exe", "/Query", "/TN", windowsTaskName(spec), "/XML")
	if !ok {
		return false, nil
	}
	return strings.Contains(output, "<Command>"+xmlText(command)+"</Command>") && strings.Contains(output, "<Arguments>"+xmlText(windowsTaskArguments(spec))+"</Arguments>") && strings.Contains(output, "<Hidden>true</Hidden>"), nil
}

func (m windowsManager) Install(spec Spec) error {
	if err := os.MkdirAll(spec.ConfigRoot, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(windowsLauncherPath(spec), windowsLauncherBytes(spec), 0600); err != nil {
		return fmt.Errorf("write Windows managed runtime launcher: %w", err)
	}
	file, err := os.CreateTemp(spec.ConfigRoot, ".service-task-*.xml")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	xml, err := WindowsTaskXML(spec)
	if err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.WriteString(xml); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	_, err = runCommandObserver(m.trace, "schtasks.exe", "/Create", "/TN", windowsTaskName(spec), "/XML", path, "/F")
	return err
}

func (m windowsManager) Start(spec Spec) error {
	_, err := runCommandObserver(m.trace, "schtasks.exe", "/Run", "/TN", windowsTaskName(spec))
	return err
}

func (m windowsManager) Stop(spec Spec) error {
	_, err := runCommandObserver(m.trace, "schtasks.exe", "/End", "/TN", windowsTaskName(spec))
	return err
}

func (m windowsManager) Uninstall(spec Spec) error {
	_, err := runCommandObserver(m.trace, "schtasks.exe", "/Delete", "/TN", windowsTaskName(spec), "/F")
	if err != nil {
		return err
	}
	if err := os.Remove(windowsLauncherPath(spec)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Windows managed runtime launcher: %w", err)
	}
	return nil
}

func (m windowsManager) Status(spec Spec) (Status, error) {
	_, installed := commandSucceededObserver(m.trace, "schtasks.exe", "/Query", "/TN", windowsTaskName(spec))
	return Status{Installed: installed, Backend: "task-scheduler"}, nil
}

func WindowsTaskXML(spec Spec) (string, error) {
	command, err := windowsTaskCommand()
	if err != nil {
		return "", err
	}
	arguments := windowsTaskArguments(spec)
	return fmt.Sprintf(`<?xml version="1.0"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>CodeMCP managed runtime</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>5</Count>
    </RestartOnFailure>
    <Hidden>true</Hidden>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, xmlText(spec.Account.Username), xmlText(spec.Account.Username), xmlText(command), xmlText(arguments), xmlText(spec.Account.HomeDir)), nil
}

func windowsTaskCommand() (string, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("resolve Windows system directory: %w", err)
	}
	return filepath.Join(systemDir, "wscript.exe"), nil
}

func windowsTaskArguments(spec Spec) string {
	return windowsCommandLine([]string{"//B", "//NoLogo", windowsLauncherPath(spec)})
}

func windowsLauncherPath(spec Spec) string {
	return filepath.Join(spec.ConfigRoot, ".service-launcher-"+spec.ID+".vbs")
}

func windowsLauncherScript(spec Spec) string {
	command := windowsCommandLine(append([]string{spec.Binary}, Args(spec)...))
	return strings.Join([]string{
		`Set shell = CreateObject("WScript.Shell")`,
		"shell.CurrentDirectory = " + windowsVBScriptString(spec.Account.HomeDir),
		"exitCode = shell.Run(" + windowsVBScriptString(command) + ", 0, True)",
		"WScript.Quit exitCode",
	}, "\r\n") + "\r\n"
}

func windowsLauncherBytes(spec Spec) []byte {
	encoded := utf16.Encode([]rune(windowsLauncherScript(spec)))
	bytes := make([]byte, 2+len(encoded)*2)
	bytes[0], bytes[1] = 0xff, 0xfe
	for i, value := range encoded {
		bytes[2+i*2] = byte(value)
		bytes[2+i*2+1] = byte(value >> 8)
	}
	return bytes
}

func windowsVBScriptString(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func windowsTaskName(spec Spec) string { return spec.ID }

func windowsCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windowsQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func windowsQuoteArg(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"") {
		return value
	}
	var builder strings.Builder
	builder.WriteByte('"')
	backslashes := 0
	for _, char := range value {
		if char == '\\' {
			backslashes++
			continue
		}
		if char == '"' {
			builder.WriteString(strings.Repeat(`\`, backslashes*2+1))
			builder.WriteRune(char)
			backslashes = 0
			continue
		}
		builder.WriteString(strings.Repeat(`\`, backslashes))
		backslashes = 0
		builder.WriteRune(char)
	}
	builder.WriteString(strings.Repeat(`\`, backslashes*2))
	builder.WriteByte('"')
	return builder.String()
}
