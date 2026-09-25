package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/tunnel-client/pkg/tunnelctx"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/idgen"
	localmcp "go.mewis.me/codemcp/internal/mcp"
	"go.mewis.me/codemcp/internal/tools"
)

type sdkBridge struct {
	runtime          *tools.Runtime
	server           *sdkmcp.Server
	mu               sync.Mutex
	fingerprints     map[string]string
	sessionNamespace uint64
	sessionIDs       map[*sdkmcp.ServerSession]string
	nextSession      uint64
	approvalCallers  *approval.CallerRegistry
}

var sdkBridgeNamespace atomic.Uint64

func newSDKBridge(runtime *tools.Runtime) (*sdkBridge, error) {
	if runtime == nil || runtime.Registry == nil {
		return nil, errors.New("MCP tools runtime is required")
	}
	descriptors := localmcp.DescribeProtocol(nil)
	implementation, options := localmcp.ProjectSDKServer(localmcp.BaseProfile(), descriptors)
	server := sdkmcp.NewServer(implementation, options)
	bridge := &sdkBridge{runtime: runtime, server: server, fingerprints: map[string]string{}, sessionNamespace: sdkBridgeNamespace.Add(1), sessionIDs: map[*sdkmcp.ServerSession]string{}, approvalCallers: approval.NewCallerRegistry()}
	if err := bridge.syncTools(); err != nil {
		return nil, err
	}
	return bridge, nil
}

func (b *sdkBridge) Run(ctx context.Context, transport sdkmcp.Transport) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	changes := b.runtime.Registry.SubscribeChanges()
	defer b.runtime.Registry.UnsubscribeChanges(changes)
	serverDone := make(chan error, 1)
	go func() { serverDone <- b.server.Run(runCtx, transport) }()

	for {
		select {
		case <-changes:
			if err := b.syncTools(); err != nil {
				return fmt.Errorf("sync tunnel tools: %w", err)
			}
		case err := <-serverDone:
			return err
		case <-ctx.Done():
			cancel()
			return <-serverDone
		}
	}
}

func (b *sdkBridge) syncTools() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	type preparedTool struct {
		tool        *sdkmcp.Tool
		fingerprint string
	}
	prepared := map[string]preparedTool{}
	for _, schema := range b.runtime.List() {
		descriptor := localmcp.DescribeTool(schema)
		tool, err := localmcp.ProjectSDKTool(localmcp.BaseProfile(), descriptor, localmcp.ToolProjectionOptions{})
		if err != nil {
			return fmt.Errorf("tool %q: %w", schema.Name, err)
		}
		data, err := json.Marshal(descriptor)
		if err != nil {
			return fmt.Errorf("fingerprint tool %q: %w", schema.Name, err)
		}
		prepared[schema.Name] = preparedTool{tool: tool, fingerprint: string(data)}
	}

	var removed []string
	for name := range b.fingerprints {
		if _, ok := prepared[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	if len(removed) > 0 {
		b.server.RemoveTools(removed...)
	}

	names := make([]string, 0, len(prepared))
	for name := range prepared {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		item := prepared[name]
		if b.fingerprints[name] == item.fingerprint {
			continue
		}
		if err := addSDKTool(b.server, item.tool, b.toolHandler(name)); err != nil {
			return err
		}
	}

	next := make(map[string]string, len(prepared))
	for name, item := range prepared {
		next[name] = item.fingerprint
	}
	b.fingerprints = next
	return nil
}

func addSDKTool(server *sdkmcp.Server, tool *sdkmcp.Tool, handler sdkmcp.ToolHandler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("register SDK tool %q: %v", tool.Name, recovered)
		}
	}()
	server.AddTool(tool, handler)
	return nil
}

func (b *sdkBridge) toolHandler(name string) sdkmcp.ToolHandler {
	return func(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		if request == nil || request.Params == nil {
			return nil, errors.New("tools/call params are required")
		}
		args, err := decodeToolArguments(request.Params.Arguments)
		if err != nil {
			return nil, err
		}
		requestContext := localmcp.RequestContextFromSDK(request)
		ctx = localmcp.WithRequestContext(ctx, requestContext)
		ctx = tools.WithInputRound(ctx, requestContext.RequestState, requestContext.InputResponses)
		ctx = tools.WithCallSource(ctx, "tunnel")
		if requestContext.Modern() {
			ctx = tools.WithApprovalCorrelation(ctx, b.approvalCallers.Caller("modern:tunnel"), idgen.Must("apr", 8))
		} else if sessionID := b.sessionID(ctx, request); sessionID != "" {
			ctx = tools.WithMCPSessionID(ctx, sessionID)
			ctx = tools.WithApprovalCorrelation(ctx, b.approvalCallers.Caller("legacy:tunnel:"+sessionID), idgen.Must("apr", 8))
		}
		if request.Params.Meta != nil {
			delete(request.Params.Meta, sessionMetaKey)
		}
		if data, err := json.Marshal(request.Params); err == nil {
			var params map[string]any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if decoder.Decode(&params) == nil {
				ctx = tools.WithCallDetails(ctx, "tools/call", params)
				ctx = tools.WithCallRequest(ctx, map[string]any{"method": "tools/call", "params": params})
			}
		}
		result, err := b.runtime.Call(ctx, name, args)
		if err != nil {
			return nil, err
		}
		return sdkResultFromTools(result)
	}
}

func (b *sdkBridge) sessionID(ctx context.Context, request *sdkmcp.CallToolRequest) string {
	if sessionID, ok := tunnelctx.SessionIDFromContext(ctx); ok {
		if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
			return sessionID
		}
	}
	if request != nil && request.Params != nil && request.Params.Meta != nil {
		if sessionID, _ := request.Params.Meta[sessionMetaKey].(string); strings.TrimSpace(sessionID) != "" {
			return strings.TrimSpace(sessionID)
		}
	}
	if request != nil && request.Session != nil {
		if sessionID := strings.TrimSpace(request.Session.ID()); sessionID != "" {
			return "sdk:" + sessionID
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		if sessionID := b.sessionIDs[request.Session]; sessionID != "" {
			return sessionID
		}
		b.nextSession++
		sessionID := fmt.Sprintf("sdk:%d:%d", b.sessionNamespace, b.nextSession)
		b.sessionIDs[request.Session] = sessionID
		return sessionID
	}
	return ""
}

func decodeToolArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var args map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil {
		return nil, fmt.Errorf("decode tool arguments: %w", err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func sdkResultFromTools(result tools.Result) (*sdkmcp.CallToolResult, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	var converted sdkmcp.CallToolResult
	if err := json.Unmarshal(data, &converted); err != nil {
		return nil, fmt.Errorf("convert tool result to MCP SDK: %w", err)
	}
	return &converted, nil
}
