package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/logger"
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
	if logger.CanAnimate(cmd.OutOrStdout()) {
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
	return cmd.Annotations[machineOutputAnnotation] == "true" || commandResultModeFor(cmd) == resultModeJSON
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
