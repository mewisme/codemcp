package activity

type Type string

const (
	EventToolCall Type = "tool_call"
	EventRequest  Type = "mcp_request"
	EventSession  Type = "session"
	EventSystem   Type = "system"
	EventApproval Type = "approval"
)
