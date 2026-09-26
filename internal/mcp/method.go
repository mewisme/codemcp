package mcp

func IsSupportedMethod(method string) bool {
	switch method {
	case "server/discover", "tools/list", "tools/call", "subscriptions/listen", CompletionCompleteMethod:
		return true
	default:
		return false
	}
}
