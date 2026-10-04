//go:build windows

package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func statusCanonicalPlatform(layout Layout) (CanonicalStatus, error) {
	status := CanonicalStatus{State: CanonicalMissing, Path: layout.CanonicalBinary, Target: layout.CurrentBinary}
	info, err := os.Stat(layout.CanonicalBinary)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return CanonicalStatus{}, err
	}
	if !info.Mode().IsRegular() {
		status.State = CanonicalConflict
		return status, nil
	}
	if ownsWindowsUserPath(layout) {
		registered, err := windowsUserPathContains(layout.BinDir)
		if err != nil {
			return CanonicalStatus{}, err
		}
		if !registered {
			return status, nil
		}
	}
	status.State = CanonicalInstalled
	return status, nil
}

func installCanonicalPlatform(layout Layout) error {
	if !ownsWindowsUserPath(layout) {
		return nil
	}
	return updateWindowsUserPath(layout.BinDir, true)
}

func removeCanonicalPlatform(layout Layout) error {
	if !ownsWindowsUserPath(layout) {
		return nil
	}
	return updateWindowsUserPath(layout.BinDir, false)
}

func ownsWindowsUserPath(layout Layout) bool {
	defaults, err := DefaultLayout()
	return err == nil && samePath(layout.Root, defaults.Root) && samePath(layout.BinDir, defaults.BinDir)
}

func windowsUserPathContains(entry string) (bool, error) {
	value, _, err := readWindowsUserPath()
	if err != nil {
		return false, err
	}
	want := comparableWindowsPathEntry(entry)
	for _, candidate := range strings.Split(value, ";") {
		if comparableWindowsPathEntry(candidate) == want {
			return true, nil
		}
	}
	return false, nil
}

func updateWindowsUserPath(entry string, add bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	value, valueType, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		value, valueType, err = "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		return err
	}
	want := comparableWindowsPathEntry(entry)
	entries := make([]string, 0)
	found := false
	for _, candidate := range strings.Split(value, ";") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if comparableWindowsPathEntry(candidate) == want {
			found = true
			if !add {
				continue
			}
		}
		entries = append(entries, candidate)
	}
	if add && !found {
		entries = append([]string{filepath.Clean(entry)}, entries...)
	}
	if (add && found) || (!add && !found) {
		return nil
	}
	next := strings.Join(entries, ";")
	if valueType == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", next)
	} else {
		err = key.SetStringValue("Path", next)
	}
	if err != nil {
		return err
	}
	broadcastWindowsEnvironmentChange()
	return nil
}

func readWindowsUserPath() (string, uint32, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		return "", 0, err
	}
	defer key.Close()
	value, valueType, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", registry.EXPAND_SZ, nil
	}
	return value, valueType, err
}

func comparableWindowsPathEntry(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	value = expandWindowsEnvironment(value)
	if value == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(value))
}

func expandWindowsEnvironment(value string) string {
	source, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return value
	}
	size, err := windows.ExpandEnvironmentStrings(source, nil, 0)
	if err != nil || size == 0 {
		return value
	}
	buffer := make([]uint16, size)
	if _, err := windows.ExpandEnvironmentStrings(source, &buffer[0], size); err != nil {
		return value
	}
	return windows.UTF16ToString(buffer)
}

func broadcastWindowsEnvironmentChange() {
	const (
		hwndBroadcast    = 0xffff
		wmSettingChange  = 0x001a
		smtoAbortIfHung  = 0x0002
		broadcastTimeout = 5000
	)
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	environment, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = proc.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(environment)),
		smtoAbortIfHung,
		broadcastTimeout,
		uintptr(unsafe.Pointer(&result)),
	)
}
