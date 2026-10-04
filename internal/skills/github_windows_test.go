//go:build windows

package skills

import "errors"

func syscallMkfifo(string) error {
	return errors.New("mkfifo unsupported")
}
