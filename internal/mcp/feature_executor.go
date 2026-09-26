package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

type FeatureExecutor struct {
	Registry       *FeatureRegistry
	Tools          *tools.Runtime
	BoundWorkspace string
	Source         string
}

func NewFeatureExecutor(registry *FeatureRegistry, runtime *tools.Runtime, boundWorkspace, source string) *FeatureExecutor {
	return &FeatureExecutor{
		Registry:       registry,
		Tools:          runtime,
		BoundWorkspace: strings.TrimSpace(boundWorkspace),
		Source:         strings.TrimSpace(source),
	}
}

func (e *FeatureExecutor) SupportsMethod(name string) bool {
	return e != nil && e.Registry != nil && e.Registry.SupportsMethod(name)
}

func (e *FeatureExecutor) Invoke(ctx context.Context, methodName string, params map[string]any) (map[string]any, error) {
	if e == nil || e.Registry == nil {
		return nil, NewError(ErrInternal, "feature registry is unavailable")
	}
	switch strings.TrimSpace(methodName) {
	case ResourcesListMethod:
		cursor, err := resourceCursor(params)
		if err != nil {
			return nil, err
		}
		return e.ListResources(ctx, cursor)
	case ResourceTemplatesListMethod:
		cursor, err := resourceCursor(params)
		if err != nil {
			return nil, err
		}
		return e.ListResourceTemplates(ctx, cursor)
	case ResourcesReadMethod:
		rawURI, ok := params["uri"].(string)
		if !ok || strings.TrimSpace(rawURI) == "" {
			return nil, NewError(ErrInvalidParams, "resource uri is required")
		}
		result, err := e.ReadResource(ctx, rawURI)
		if err != nil {
			return nil, err
		}
		return resourceReadResultMap(result), nil
	}
	method, ok := e.Registry.method(methodName)
	if !ok {
		return nil, NewError(ErrMethodNotFound, "method not found")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.Source != "" {
		ctx = tools.WithCallSource(ctx, e.Source)
	}
	if e.BoundWorkspace != "" {
		ctx = tools.WithBoundWorkspace(ctx, e.BoundWorkspace)
	}
	var err error
	params, err = cloneFeatureMap(params)
	if err != nil {
		return nil, NewError(ErrInvalidParams, "feature params are not JSON serializable")
	}

	workspaceID := ""
	if method.Scope == FeatureScopeWorkspace {
		if e.Tools == nil {
			return nil, NewError(ErrInternal, "tool runtime is unavailable")
		}
		requested, err := featureWorkspaceID(method, params)
		if err != nil {
			return nil, featureInvalidParams(err)
		}
		resolution, err := e.Tools.ResolveWorkspaceAccess(ctx, requested)
		if err != nil {
			return nil, featureInvalidParams(err)
		}
		workspaceID = resolution.WorkspaceID
		params["workspace_id"] = workspaceID
	}

	result, err := method.Handler(ctx, FeatureRequest{
		Method:      method.Name,
		Params:      params,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return nil, boundFeatureError(method, err)
	}
	if result == nil {
		result = map[string]any{}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, NewError(ErrInternal, "feature result is not JSON serializable")
	}
	if len(data) > method.MaxResultBytes {
		return nil, NewErrorData(ErrInternal, "feature result exceeds size limit", map[string]any{
			"method": method.Name,
			"limit":  method.MaxResultBytes,
		})
	}
	var cloned map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&cloned); err != nil {
		return nil, NewError(ErrInternal, "feature result could not be normalized")
	}
	if cloned == nil {
		cloned = map[string]any{}
	}
	return cloned, nil
}

func resourceCursor(params map[string]any) (string, error) {
	value, exists := params["cursor"]
	if !exists || value == nil {
		return "", nil
	}
	cursor, ok := value.(string)
	if !ok {
		return "", NewError(ErrInvalidParams, "cursor must be a string")
	}
	return cursor, nil
}

func (e *FeatureExecutor) ToolFallback(method string) tools.Handler {
	return func(ctx context.Context, args map[string]any) (tools.Result, error) {
		result, err := e.Invoke(ctx, method, args)
		if err != nil {
			return tools.Result{}, err
		}
		return tools.JSONResult(result), nil
	}
}

func featureWorkspaceID(method FeatureMethod, params map[string]any) (string, error) {
	if method.ResolveWorkspace != nil {
		cloned, err := cloneFeatureMap(params)
		if err != nil {
			return "", err
		}
		return method.ResolveWorkspace(cloned)
	}
	value, exists := params["workspace_id"]
	if !exists {
		return "", nil
	}
	workspaceID, ok := value.(string)
	if !ok {
		return "", errors.New("workspace_id must be a string")
	}
	return strings.TrimSpace(workspaceID), nil
}

func cloneFeatureMap(value map[string]any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var cloned map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&cloned); err != nil {
		return nil, err
	}
	if cloned == nil {
		cloned = map[string]any{}
	}
	return cloned, nil
}

func boundFeatureError(method FeatureMethod, err error) *Error {
	protocol := featureProtocolError(err)
	encoded, marshalErr := json.Marshal(map[string]any{
		"code": protocol.Code, "message": protocol.Message, "data": protocol.Data,
	})
	if marshalErr != nil {
		return NewError(ErrInternal, "feature error is not JSON serializable")
	}
	if len(encoded) > method.MaxResultBytes {
		return NewErrorData(ErrInternal, "feature error exceeds size limit", map[string]any{
			"method": method.Name,
			"limit":  method.MaxResultBytes,
		})
	}
	return protocol
}

func featureInvalidParams(err error) *Error {
	if protocol := featureProtocolError(err); protocol.Code != ErrInternal {
		return protocol
	}
	return NewError(ErrInvalidParams, err.Error())
}

func featureProtocolError(err error) *Error {
	if err == nil {
		return nil
	}
	var protocol *Error
	if errors.As(err, &protocol) {
		return protocol
	}
	return NewError(ErrInternal, err.Error())
}

type featureMethodParams struct {
	sdkmcp.ParamsBase
	Fields map[string]any `json:"-"`
}

func (p *featureMethodParams) UnmarshalJSON(data []byte) error {
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	if fields == nil {
		fields = map[string]any{}
	}
	if rawMeta, ok := fields["_meta"]; ok {
		encoded, err := json.Marshal(rawMeta)
		if err != nil {
			return err
		}
		var meta sdkmcp.Meta
		if err := json.Unmarshal(encoded, &meta); err != nil {
			return err
		}
		p.ParamsBase.Meta = meta
	}
	delete(fields, "_meta")
	p.Fields = fields
	return nil
}

type featureMethodResult struct {
	sdkmcp.ResultBase
	Fields map[string]any `json:"-"`
}

func (r *featureMethodResult) MarshalJSON() ([]byte, error) {
	fields := cloneAnyMap(r.Fields)
	if fields == nil {
		fields = map[string]any{}
	}
	if meta := r.GetMeta(); len(meta) > 0 {
		fields["_meta"] = cloneAnyMap(meta)
	}
	return json.Marshal(fields)
}

func InstallFeatureMethods(server *sdkmcp.Server, executor *FeatureExecutor) error {
	if server == nil || executor == nil || executor.Registry == nil {
		return nil
	}
	for _, descriptor := range executor.Registry.Snapshot().Methods {
		if !descriptor.Custom {
			continue
		}
		methodName := descriptor.Name
		if err := sdkmcp.AddReceivingCustomMethod(server, methodName, func(ctx context.Context, _ *sdkmcp.ServerSession, params *featureMethodParams) (*featureMethodResult, error) {
			fields := map[string]any{}
			if params != nil {
				fields = cloneAnyMap(params.Fields)
			}
			result, err := executor.Invoke(ctx, methodName, fields)
			if err != nil {
				return nil, featureJSONRPCError(err)
			}
			return &featureMethodResult{Fields: result}, nil
		}); err != nil {
			return fmt.Errorf("register feature method %q: %w", methodName, err)
		}
	}
	return nil
}

func featureJSONRPCError(err error) error {
	protocol := featureProtocolError(err)
	var data json.RawMessage
	if protocol.Data != nil {
		if encoded, marshalErr := json.Marshal(protocol.Data); marshalErr == nil {
			data = encoded
		}
	}
	return &jsonrpc.Error{Code: int64(protocol.Code), Message: protocol.Message, Data: data}
}
