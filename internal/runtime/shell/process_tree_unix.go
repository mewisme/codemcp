//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package shell

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureCommandLifecycle(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return signalCommandTree(cmd, true) }
	cmd.WaitDelay = time.Second
}

func signalCommandTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	signal := syscall.SIGINT
	if force {
		signal = syscall.SIGKILL
	}
	if err := syscall.Kill(-cmd.Process.Pid, signal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
