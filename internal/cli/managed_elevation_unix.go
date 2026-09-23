//go:build linux || darwin

package cli

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	managed "go.mewis.me/codemcp/internal/service"
)

func elevateManagedCommandWithBinary(cmd *cobra.Command, action, environmentHash, binaryPath string) error {
	binary, err := managed.StableBinaryPath(binaryPath)
	if err != nil {
		return err
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("sudo is required for system service scope")
	}
	args := []string{binary, "--config-dir", config.RootPath()}
	verbose, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	debug, _ := cmd.Root().PersistentFlags().GetBool("debug")
	format, _ := cmd.Root().PersistentFlags().GetString("log-format")
	if verbose {
		args = append(args, "--verbose")
	}
	if debug {
		args = append(args, "--debug")
	}
	if format != "" && format != "text" {
		args = append(args, "--log-format", format)
	}
	args = append(args, action, "--system")
	if (action == "up" || action == "restart") && environmentHash != "" {
		args = append(args, "--service-environment-hash", environmentHash)
	}
	child := exec.CommandContext(cmd.Context(), sudo, args...)
	child.Stdin, child.Stdout, child.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := child.Run(); err != nil {
		return fmt.Errorf("system service command failed: %w", err)
	}
	return nil
}
