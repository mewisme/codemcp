package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

type CallObservation struct {
	CallID                string
	Phase                 string
	Source                string
	Tool                  string
	WorkspaceID           string
	Status                string
	DurationMS            int64
	Message               string
	ResultType            string
	Raw                   map[string]any
	SessionHash           string
	SessionAccess         SessionWorkspaceAccessDecision
	SessionWorkspaceCount int
	ReceivedByInstanceID  string
	ExecutedByInstanceID  string
}

type CallObserver func(CallObservation)

type callSourceKey struct{}
type callDetailsKey struct{}
type receivedByInstanceKey struct{}
type mcpSessionIDKey struct{}
type approvalCorrelationKey struct{}
type agentCompletionCorrelationKey struct{}
type clientHintsKey struct{}
type requestCorrelationHintsKey struct{}

type ApprovalCorrelation struct {
	CallerID  string
	RequestID string
}

type AgentCompletionCorrelation struct {
	AgentID string
	Source  string
}

// ClientHints contains optional presentation and diagnostics hints supplied by
// an MCP client. These values are never authorization or workspace identity.
type ClientHints struct {
	Locale    string
	UserAgent string
	Location  ClientLocationHint
}

type ClientLocationHint struct {
	City      string
	Region    string
	Country   string
	Timezone  string
	Longitude *float64
	Latitude  *float64
}

// RequestCorrelationHints contains opaque client-provided identifiers for
// diagnostics only. Approval and workspace correlation use separate context.
type RequestCorrelationHints struct {
	SubjectID      string
	SessionID      string
	OrganizationID string
}

type callDetails struct {
	Method  string
	Params  map[string]any
	Request map[string]any
}

func MCPSessionFingerprint(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])[:12]
}

func mcpSessionStateKey(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])
}

func WithReceivedByInstanceID(ctx context.Context, instanceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, receivedByInstanceKey{}, instanceID)
}

func ReceivedByInstanceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(receivedByInstanceKey{}).(string)
	return value
}

func WithMCPSessionID(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mcpSessionIDKey{}, sessionID)
}

func MCPSessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(mcpSessionIDKey{}).(string)
	return value
}

func WithApprovalCorrelation(ctx context.Context, callerID, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, approvalCorrelationKey{}, ApprovalCorrelation{CallerID: strings.TrimSpace(callerID), RequestID: strings.TrimSpace(requestID)})
}

func ApprovalCorrelationFromContext(ctx context.Context) ApprovalCorrelation {
	if ctx == nil {
		return ApprovalCorrelation{}
	}
	value, _ := ctx.Value(approvalCorrelationKey{}).(ApprovalCorrelation)
	value.CallerID = strings.TrimSpace(value.CallerID)
	value.RequestID = strings.TrimSpace(value.RequestID)
	return value
}

func WithAgentCompletionCorrelation(ctx context.Context, callerID, generationID, source string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, agentCompletionCorrelationKey{}, AgentCompletionCorrelation{
		AgentID: agentcompletion.DeriveAgentID(callerID, generationID),
		Source:  strings.TrimSpace(source),
	})
}

func AgentCompletionCorrelationFromContext(ctx context.Context) AgentCompletionCorrelation {
	if ctx == nil {
		return AgentCompletionCorrelation{}
	}
	value, _ := ctx.Value(agentCompletionCorrelationKey{}).(AgentCompletionCorrelation)
	value.AgentID = strings.TrimSpace(value.AgentID)
	value.Source = strings.TrimSpace(value.Source)
	return value
}

func WithClientHints(ctx context.Context, value ClientHints) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, clientHintsKey{}, cloneClientHints(value))
}

func ClientHintsFromContext(ctx context.Context) ClientHints {
	if ctx == nil {
		return ClientHints{}
	}
	value, _ := ctx.Value(clientHintsKey{}).(ClientHints)
	return cloneClientHints(value)
}

func WithRequestCorrelationHints(ctx context.Context, value RequestCorrelationHints) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	value.SubjectID = strings.TrimSpace(value.SubjectID)
	value.SessionID = strings.TrimSpace(value.SessionID)
	value.OrganizationID = strings.TrimSpace(value.OrganizationID)
	return context.WithValue(ctx, requestCorrelationHintsKey{}, value)
}

func RequestCorrelationHintsFromContext(ctx context.Context) RequestCorrelationHints {
	if ctx == nil {
		return RequestCorrelationHints{}
	}
	value, _ := ctx.Value(requestCorrelationHintsKey{}).(RequestCorrelationHints)
	value.SubjectID = strings.TrimSpace(value.SubjectID)
	value.SessionID = strings.TrimSpace(value.SessionID)
	value.OrganizationID = strings.TrimSpace(value.OrganizationID)
	return value
}

func cloneClientHints(value ClientHints) ClientHints {
	value.Locale = strings.TrimSpace(value.Locale)
	value.UserAgent = strings.TrimSpace(value.UserAgent)
	value.Location.City = strings.TrimSpace(value.Location.City)
	value.Location.Region = strings.TrimSpace(value.Location.Region)
	value.Location.Country = strings.TrimSpace(value.Location.Country)
	value.Location.Timezone = strings.TrimSpace(value.Location.Timezone)
	if value.Location.Longitude != nil {
		longitude := *value.Location.Longitude
		value.Location.Longitude = &longitude
	}
	if value.Location.Latitude != nil {
		latitude := *value.Location.Latitude
		value.Location.Latitude = &latitude
	}
	return value
}

func WithCallSource(ctx context.Context, source string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, callSourceKey{}, source)
}

func CallSource(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	source, _ := ctx.Value(callSourceKey{}).(string)
	return source
}

func WithCallDetails(ctx context.Context, method string, params map[string]any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	details, _ := ctx.Value(callDetailsKey{}).(callDetails)
	details.Method = method
	details.Params = cloneMap(params)
	return context.WithValue(ctx, callDetailsKey{}, details)
}

func WithCallRequest(ctx context.Context, request map[string]any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	details, _ := ctx.Value(callDetailsKey{}).(callDetails)
	details.Request = cloneMap(request)
	return context.WithValue(ctx, callDetailsKey{}, details)
}

func callRaw(ctx context.Context, source, name string, args map[string]any) map[string]any {
	method := "tools/call"
	publicArgs := observableToolArguments(name, args)
	params := map[string]any{"name": name, "arguments": publicArgs}
	if ctx != nil {
		if details, ok := ctx.Value(callDetailsKey{}).(callDetails); ok {
			if details.Method != "" {
				method = details.Method
			}
			if details.Params != nil {
				params = observableToolEnvelope(name, details.Params, publicArgs)
			}
			if details.Request != nil {
				request := cloneMap(details.Request)
				if requestParams, ok := request["params"].(map[string]any); ok {
					request["params"] = observableToolEnvelope(name, requestParams, publicArgs)
				}
				return map[string]any{"method": method, "source": source, "tool": name, "arguments": publicArgs, "params": params, "request": request}
			}
		}
	}
	return map[string]any{"method": method, "source": source, "tool": name, "arguments": publicArgs, "params": params}
}

func observableToolArguments(name string, args map[string]any) map[string]any {
	switch name {
	case mcpconfigwire.SetToolName:
		summary := mcpconfigwire.SummarizeArguments(args)
		return map[string]any{
			"change_count": summary.ChangeCount,
			"keys":         append([]string(nil), summary.Keys...),
		}
	case CreateRuleToolName:
		return observableRuleAuthoringArguments(args)
	case CreateSkillToolName:
		return observableSkillAuthoringArguments(args)
	case CreatePlanToolName:
		return observablePlanAuthoringArguments(args)
	default:
		return cloneMap(args)
	}
}

func observableToolEnvelope(name string, value, publicArgs map[string]any) map[string]any {
	out := cloneMap(value)
	if name == mcpconfigwire.SetToolName || name == CreateRuleToolName || name == CreateSkillToolName || name == CreatePlanToolName {
		out["arguments"] = cloneMap(publicArgs)
	}
	return out
}

func observableRuleAuthoringArguments(args map[string]any) map[string]any {
	result := map[string]any{
		"workspace_id":  stringArgument(args, "workspace_id"),
		"mode":          stringArgument(args, "mode"),
		"name":          stringArgument(args, "name"),
		"always_apply":  boolArgument(args, "always_apply"),
		"dry_run":       boolArgument(args, "dry_run"),
		"content_bytes": len([]byte(stringArgument(args, "content"))),
	}
	if globs, ok := sliceArgument(args, "globs"); ok {
		result["glob_count"] = len(globs)
	} else {
		result["glob_count"] = 0
	}
	return result
}

func observableSkillAuthoringArguments(args map[string]any) map[string]any {
	result := map[string]any{
		"workspace_id":      stringArgument(args, "workspace_id"),
		"mode":              stringArgument(args, "mode"),
		"name":              stringArgument(args, "name"),
		"dry_run":           boolArgument(args, "dry_run"),
		"description_bytes": len([]byte(stringArgument(args, "description"))),
		"instruction_bytes": len([]byte(stringArgument(args, "instructions"))),
	}
	files, ok := sliceArgument(args, "supporting_files")
	if !ok {
		result["supporting_file_count"] = 0
		result["supporting_bytes"] = 0
		return result
	}
	bytes := 0
	for _, item := range files {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		bytes += len([]byte(stringArgument(entry, "content")))
	}
	result["supporting_file_count"] = len(files)
	result["supporting_bytes"] = bytes
	return result
}

func observablePlanAuthoringArguments(args map[string]any) map[string]any {
	result := map[string]any{
		"workspace_id":               stringArgument(args, "workspace_id"),
		"mode":                       stringArgument(args, "mode"),
		"name":                       stringArgument(args, "name"),
		"expected_content_id":        stringArgument(args, "expected_content_id"),
		"dry_run":                    boolArgument(args, "dry_run"),
		"plan_content_bytes":         len([]byte(stringArgument(args, "plan_content"))),
		"implementation_order_bytes": len([]byte(stringArgument(args, "implementation_order"))),
	}
	return result
}

func observableToolMessage(name, status, message string) string {
	if name == CreatePlanToolName && status != "ok" && strings.TrimSpace(message) != "" {
		return "plan authoring failed"
	}
	return message
}

func stringArgument(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func boolArgument(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}

func sliceArgument(args map[string]any, key string) ([]any, bool) {
	value, exists := args[key]
	if !exists {
		return nil, false
	}
	if values, ok := value.([]any); ok {
		return values, true
	}
	if values, ok := value.([]string); ok {
		result := make([]any, len(values))
		for index := range values {
			result[index] = values[index]
		}
		return result, true
	}
	if values, ok := value.([]map[string]any); ok {
		result := make([]any, len(values))
		for index := range values {
			result[index] = values[index]
		}
		return result, true
	}
	return nil, false
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = cloneValue(item)
	}
	return result
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneValue(item)
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func (r *Runtime) SetCallObserver(observer CallObserver) {
	if r != nil {
		r.CallObserver = observer
	}
}

func (r *Runtime) HasCallObserver() bool {
	return r != nil && r.CallObserver != nil
}

func (r *Runtime) observeCall(observation CallObservation) {
	if r != nil && r.CallObserver != nil {
		r.CallObserver(observation)
	}
}
