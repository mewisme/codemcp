package application

import (
	"context"
	"os"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/install"
	"go.mewis.me/codemcp/internal/runtimecontrol"
	"go.mewis.me/codemcp/internal/runtimeevent"
	"go.mewis.me/codemcp/internal/systeminfo"
	"go.mewis.me/codemcp/internal/version"
)

type AboutInfo struct {
	Version         string
	Commit          string
	BuildTime       string
	Executable      string
	ConfigRoot      string
	ConfigPath      string
	LogsPath        string
	InstallMethod   install.Method
	InstallRoot     string
	RuntimeRunning  bool
	Runtime         runtimecontrol.RuntimeStatus
	ServerUptime    time.Duration
	MachineUptime   time.Duration
	MachineUptimeOK bool
}

var machineUptime = systeminfo.Uptime

func LoadAbout(ctx context.Context) (AboutInfo, error) {
	executable, _ := os.Executable()
	source, _ := config.Source()
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	status, running, err := RuntimeStatus(statusCtx)
	cancel()
	if err != nil {
		return AboutInfo{}, err
	}
	machine, machineErr := machineUptime()
	detection, detectErr := install.DetectCurrent(version.Version)
	info := AboutInfo{
		Version: version.Version, Commit: version.Commit, BuildTime: version.Date, Executable: executable,
		ConfigRoot: config.RootPath(), ConfigPath: source.Path, LogsPath: runtimeevent.Path(config.RootPath()), RuntimeRunning: running, Runtime: status,
		MachineUptimeOK: machineErr == nil,
	}
	if machineErr == nil {
		info.MachineUptime = nonNegativeDuration(machine)
	}
	if running && !status.StartedAt.IsZero() {
		info.ServerUptime = nonNegativeDuration(time.Since(status.StartedAt))
	}
	if detectErr == nil {
		info.InstallMethod, info.InstallRoot = detection.Method, detection.Root
	}
	return info, nil
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value.Truncate(time.Second)
}
