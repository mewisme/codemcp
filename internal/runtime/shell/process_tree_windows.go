//go:build windows

package shell

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureCommandLifecycle(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	cmd.Cancel = func() error { return signalCommandTree(cmd, true) }
	cmd.WaitDelay = time.Second
}

func signalCommandTree(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	pid := uint32(cmd.Process.Pid)
	if !force && windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid) == nil {
		return nil
	}
	return terminateProcessTree(pid)
}

func terminateProcessTree(root uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	children := map[uint32][]uint32{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		entry.Size = uint32(unsafe.Sizeof(windows.ProcessEntry32{}))
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return err
		}
	}
	var terminate func(uint32) error
	terminate = func(pid uint32) error {
		var result error
		for _, child := range children[pid] {
			result = errors.Join(result, terminate(child))
		}
		handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				return result
			}
			return errors.Join(result, err)
		}
		defer windows.CloseHandle(handle)
		if err := windows.TerminateProcess(handle, 1); err != nil {
			result = errors.Join(result, err)
		}
		return result
	}
	return terminate(root)
}
