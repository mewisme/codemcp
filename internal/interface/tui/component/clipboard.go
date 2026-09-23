package component

import (
	"errors"
	"strings"

	"github.com/atotto/clipboard"
)

var clipboardWriteAll = clipboard.WriteAll

func CopyText(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("nothing to copy")
	}
	return clipboardWriteAll(value)
}
