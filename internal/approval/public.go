package approval

import (
	"encoding/json"
	"fmt"

	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

// PublicArguments projects approval arguments for model-visible, review,
// telemetry, and diagnostic surfaces while leaving the manager's exact private
// binding untouched.
func PublicArguments(targetTool string, raw json.RawMessage) any {
	if targetTool == mcpconfigwire.SetToolName {
		return mcpconfigwire.SummarizeRawArguments(raw)
	}
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

// PublicArgumentsJSON is the JSON form used by approval review projections.
func PublicArgumentsJSON(targetTool string, raw json.RawMessage) json.RawMessage {
	value := PublicArguments(targetTool, raw)
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

// PublicRequest returns a detached request suitable for public review surfaces.
// Exact private arguments remain stored only in Manager.
func PublicRequest(value Request) Request {
	value = cloneRequest(value)
	if value.TargetTool == mcpconfigwire.SetToolName {
		summary := mcpconfigwire.SummarizeRawArguments(value.Arguments)
		data, err := json.Marshal(summary)
		if err != nil {
			data = []byte(`{"change_count":0,"keys":[]}`)
		}
		value.Arguments = data
		value.Title = fmt.Sprintf("Update %d CodeMCP setting(s)", summary.ChangeCount)
		value.GuardReason = "CodeMCP configuration changes require local approval."
		value.Command = ""
		value.SimilarCommandPattern = ""
	} else {
		value.Arguments = PublicArgumentsJSON(value.TargetTool, value.Arguments)
	}
	return value
}
