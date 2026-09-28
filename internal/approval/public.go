package approval

import (
	"encoding/json"
	"fmt"

	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	tracepkg "go.mewis.me/codemcp/internal/trace"
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
		return tracepkg.SanitizeText(string(raw))
	}
	return sanitizePublicArgumentValue("", value)
}

func sanitizePublicArgumentValue(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for childKey, child := range typed {
			result[childKey] = sanitizePublicArgumentValue(childKey, child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = sanitizePublicArgumentValue("", item)
		}
		return result
	case string:
		if key == "command" {
			return tracepkg.SanitizeText(tracepkg.SanitizeCommand(typed))
		}
	}
	return tracepkg.SanitizeValue(key, value)
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
	value.SessionHash = ""
	value.Title = tracepkg.SanitizeText(value.Title)
	value.GuardReason = tracepkg.SanitizeText(value.GuardReason)
	value.Command = tracepkg.SanitizeText(tracepkg.SanitizeCommand(value.Command))
	value.SimilarCommandPattern = tracepkg.SanitizeText(value.SimilarCommandPattern)
	value.Reason = tracepkg.SanitizeText(value.Reason)
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

func PublicRequests(values []Request) []Request {
	result := make([]Request, len(values))
	for index := range values {
		result[index] = PublicRequest(values[index])
	}
	return result
}

func PublicEvent(value Event) Event {
	value.ChallengeID = ""
	value.SessionHash = ""
	return value
}
