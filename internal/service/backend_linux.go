//go:build linux

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type linuxManager struct{ trace tracepkg.Observer }

func NewManager() Manager                                       { return linuxManager{} }
func NewManagerWithObserver(observer tracepkg.Observer) Manager { return linuxManager{trace: observer} }
func (linuxManager) Backend() string                            { return "systemd" }

func (linuxManager) DefinitionMatches(spec Spec) (bool, error) {
	data, err := os.ReadFile(linuxUnitPath(spec))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return string(data) == LinuxUnit(spec), nil
}

func (m linuxManager) Install(spec Spec) error {
	path := linuxUnitPath(spec)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(LinuxUnit(spec)), 0644); err != nil {
		return err
	}
	if _, err := runCommandObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "daemon-reload")...); err != nil {
		return err
	}
	_, err := runCommandObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "enable", linuxUnitName(spec))...)
	return err
}

func (m linuxManager) Start(spec Spec) error {
	_, err := runCommandObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "start", linuxUnitName(spec))...)
	return err
}

func (m linuxManager) Stop(spec Spec) error {
	_, err := runCommandObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "stop", linuxUnitName(spec))...)
	return err
}

func (m linuxManager) Uninstall(spec Spec) error {
	_, _ = runCommandObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "disable", linuxUnitName(spec))...)
	if err := os.Remove(linuxUnitPath(spec)); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := runCommandObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "daemon-reload")...)
	return err
}

func (m linuxManager) Status(spec Spec) (Status, error) {
	status := Status{Backend: "systemd"}
	load, _ := runCommandObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "show", linuxUnitName(spec), "--property=LoadState", "--value")...)
	status.Installed = strings.TrimSpace(load) != "" && strings.TrimSpace(load) != "not-found"
	if !status.Installed {
		return status, nil
	}
	_, status.Running = commandSucceededObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "is-active", "--quiet", linuxUnitName(spec))...)
	if pidText, ok := commandSucceededObserver(m.trace, "systemctl", linuxSystemctlArgs(spec, "show", linuxUnitName(spec), "--property=MainPID", "--value")...); ok {
		status.PID, _ = strconv.Atoi(strings.TrimSpace(pidText))
	}
	return status, nil
}

func LinuxUnit(spec Spec) string {
	args := append([]string{spec.Binary}, Args(spec)...)
	lines := []string{
		"[Unit]",
		"Description=CodeMCP managed runtime",
		"After=network-online.target",
		"Wants=network-online.target",
		"StartLimitIntervalSec=0",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + systemdCommand(args),
		"NoNewPrivileges=true",
		"Restart=always",
		"RestartSec=3s",
	}
	if spec.Scope == ScopeSystem {
		lines = append(lines, "User="+spec.Account.Username)
		if spec.Account.GID != "" {
			lines = append(lines, "Group="+spec.Account.GID)
		}
	}
	lines = append(lines, "", "[Install]")
	if spec.Scope == ScopeSystem {
		lines = append(lines, "WantedBy=multi-user.target")
	} else {
		lines = append(lines, "WantedBy=default.target")
	}
	return strings.Join(lines, "\n") + "\n"
}

func linuxUnitName(spec Spec) string { return spec.ID + ".service" }

func linuxUnitPath(spec Spec) string {
	if spec.Scope == ScopeSystem {
		return filepath.Join(string(filepath.Separator), "etc", "systemd", "system", linuxUnitName(spec))
	}
	return filepath.Join(spec.Account.HomeDir, ".config", "systemd", "user", linuxUnitName(spec))
}

func linuxSystemctlArgs(spec Spec, args ...string) []string {
	if spec.Scope == ScopeUser {
		return append([]string{"--user"}, args...)
	}
	return args
}

func systemdCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = systemdQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return fmt.Sprintf(`"%s"`, value)
}
