package cli

import (
	"io"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

func addTerminalPresentationFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("color", false, "force color in human command output")
	cmd.PersistentFlags().Bool("no-color", false, "disable color in human command output")
}

func prepareCommandPresentation(cmd *cobra.Command) {
	if cmd == nil || commandPresentationExempt(cmd) {
		return
	}
	mode := commandResultModeFor(cmd)
	machine := commandExplicitMachineOutput(cmd) || mode == resultModeJSON
	capabilities := detectCommandTerminalCapabilities(cmd, mode == resultModeHuman, machine)
	if _, ok := presentation.FromWriter(cmd.OutOrStdout()); !ok {
		cmd.SetOut(presentation.WrapWriter(cmd.OutOrStdout(), capabilities))
	}
	if _, ok := presentation.FromWriter(cmd.ErrOrStderr()); !ok {
		cmd.SetErr(presentation.WrapWriter(cmd.ErrOrStderr(), capabilities))
	}
	commandProgressSession(cmd)
}

func commandTerminalCapabilities(cmd *cobra.Command) presentation.Capabilities {
	if cmd == nil {
		return presentation.Detect(presentation.DetectOptions{})
	}
	if capabilities, ok := presentation.FromWriter(cmd.OutOrStdout()); ok {
		return capabilities
	}
	mode := commandResultModeFor(cmd)
	return detectCommandTerminalCapabilities(cmd, mode == resultModeHuman, commandExplicitMachineOutput(cmd) || mode == resultModeJSON)
}

func detectCommandTerminalCapabilities(cmd *cobra.Command, humanResult, machineOutput bool) presentation.Capabilities {
	forceColor, noColor := commandColorOverrides(cmd)
	var stdout, stderr io.Writer
	if cmd != nil {
		stdout, stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
	}
	return presentation.Detect(presentation.DetectOptions{
		Stdout:        stdout,
		Stderr:        stderr,
		ForceColor:    forceColor,
		NoColor:       noColor,
		HumanResult:   humanResult,
		MachineOutput: machineOutput,
	})
}

func commandColorOverrides(cmd *cobra.Command) (forceColor, noColor bool) {
	if cmd == nil {
		return false, false
	}
	flags := cmd.Root().PersistentFlags()
	if flag := flags.Lookup("color"); flag != nil && flag.Changed {
		forceColor, _ = flags.GetBool("color")
	}
	if flag := flags.Lookup("no-color"); flag != nil && flag.Changed {
		noColor, _ = flags.GetBool("no-color")
	}
	return forceColor, noColor
}

func commandAnimationEligible(cmd *cobra.Command) bool {
	return commandTerminalCapabilities(cmd).Animation
}
