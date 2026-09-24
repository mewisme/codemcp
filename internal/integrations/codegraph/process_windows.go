//go:build windows

package codegraph

import "os/exec"

func configureCommandCancellation(cmd *exec.Cmd) {}
