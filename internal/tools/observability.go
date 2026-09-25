package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
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

type ApprovalCorrelation struct {
	CallerID  string
	RequestID string
}

type AgentCompletionCorrelation struct {
	AgentID string
	Source  string
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
	params := map[string]any{"name": name, "arguments": cloneMap(args)}
	if ctx != nil {
		if details, ok := ctx.Value(callDetailsKey{}).(callDetails); ok {
			if details.Method != "" {
				method = details.Method
			}
			if details.Params != nil {
				params = cloneMap(details.Params)
			}
			if details.Request != nil {
				request := cloneMap(details.Request)
				return map[string]any{"method": method, "source": source, "tool": name, "arguments": cloneMap(args), "params": params, "request": request}
			}
		}
	}
	return map[string]any{"method": method, "source": source, "tool": name, "arguments": cloneMap(args), "params": params}
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
