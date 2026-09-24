//go:build windows

package workspace

import "golang.org/x/sys/windows"

func concealLocalState(path string) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_HIDDEN != 0 {
		return nil
	}
	return windows.SetFileAttributes(ptr, attributes|windows.FILE_ATTRIBUTE_HIDDEN)
}
