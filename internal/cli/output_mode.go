package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

type commandResultMode uint8

const (
	resultModeHuman commandResultMode = iota
	resultModePlain
	resultModeJSON
)

const (
	resultJSONAnnotation    = "cm.result-json"
	machineOutputAnnotation = "cm.machine-output"
)

func commandResultModeFor(cmd *cobra.Command) commandResultMode {
	if cmd == nil {
		return resultModePlain
	}
	if cmd.Annotations[resultJSONAnnotation] == "true" {
		if flag := cmd.Flags().Lookup("json"); flag != nil && flag.Value.String() == "true" {
			return resultModeJSON
		}
	}
	if capabilities, ok := presentation.FromWriter(cmd.OutOrStdout()); ok {
		if capabilities.Interactive {
			return resultModeHuman
		}
		return resultModePlain
	}
	if detectCommandTerminalCapabilities(cmd, false, commandExplicitMachineOutput(cmd)).Interactive {
		return resultModeHuman
	}
	return resultModePlain
}

func addJSONResultFlag(cmd *cobra.Command, target *bool) {
	if cmd == nil {
		return
	}
	cmd.Flags().BoolVar(target, "json", false, "print the command result as JSON")
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[resultJSONAnnotation] = "true"
}

func markMachineOutput(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[machineOutputAnnotation] = "true"
}

func commandMachineOutput(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	return commandExplicitMachineOutput(cmd) || commandResultModeFor(cmd) == resultModeJSON
}

func commandExplicitMachineOutput(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[machineOutputAnnotation] == "true"
}

func commandResultWriter(cmd *cobra.Command) io.Writer {
	if cmd == nil {
		return io.Discard
	}
	return cmd.OutOrStdout()
}

func writeResultJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(commandResultWriter(cmd))
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
