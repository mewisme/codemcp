package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type RequestContext struct {
	ProtocolVersion      string                     `json:"protocol_version"`
	ClientInfo           *sdkmcp.Implementation     `json:"client_info,omitempty"`
	ClientCapabilities   *sdkmcp.ClientCapabilities `json:"client_capabilities,omitempty"`
	NegotiatedExtensions map[string]any             `json:"negotiated_extensions,omitempty"`
	LogLevelHint         string                     `json:"log_level_hint,omitempty"`
	RequestState         string                     `json:"request_state,omitempty"`
	InputResponses       map[string]any             `json:"input_responses,omitempty"`
}

const requestMetaLogLevelKey = "io.modelcontextprotocol/logLevel"

type requestContextKey struct{}

func WithRequestContext(ctx context.Context, value RequestContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestContextKey{}, cloneRequestContext(value))
}

func RequestContextFromContext(ctx context.Context) RequestContext {
	if ctx == nil {
		return RequestContext{}
	}
	value, _ := ctx.Value(requestContextKey{}).(RequestContext)
	return cloneRequestContext(value)
}

func (c RequestContext) Modern() bool {
	return strings.TrimSpace(c.ProtocolVersion) >= SupportedProtocolVersion
}

func requestContextFromParams(params map[string]any) (RequestContext, error) {
	meta, _ := params["_meta"].(map[string]any)
	if meta == nil {
		return RequestContext{}, errors.New("MCP request metadata is required")
	}
	value := RequestContext{}
	value.ProtocolVersion, _ = meta[sdkmcp.MetaKeyProtocolVersion].(string)
	if raw, exists := meta[sdkmcp.MetaKeyClientInfo]; exists {
		info := new(sdkmcp.Implementation)
		if err := remarshalRequestContextValue(raw, info); err != nil {
			return RequestContext{}, err
		}
		value.ClientInfo = info
	}
	if raw, exists := meta[sdkmcp.MetaKeyClientCapabilities]; exists {
		capabilities := new(sdkmcp.ClientCapabilities)
		if err := remarshalRequestContextValue(raw, capabilities); err != nil {
			return RequestContext{}, err
		}
		value.ClientCapabilities = capabilities
		value.NegotiatedExtensions = cloneAnyMap(capabilities.Extensions)
	}
	if raw, exists := meta[requestMetaLogLevelKey]; exists {
		value.LogLevelHint, _ = raw.(string)
	}
	value.RequestState, _ = params["requestState"].(string)
	if responses, ok := params["inputResponses"].(map[string]any); ok {
		value.InputResponses = cloneAnyMap(responses)
	}
	return cloneRequestContext(value), nil
}

func RequestContextFromSDK(request *sdkmcp.CallToolRequest) RequestContext {
	if request == nil {
		return RequestContext{}
	}
	value := RequestContext{
		ProtocolVersion:    strings.TrimSpace(request.ProtocolVersion()),
		ClientInfo:         cloneClientInfo(request.ClientInfo()),
		ClientCapabilities: cloneClientCapabilities(request.ClientCapabilities()),
	}
	if value.ClientCapabilities != nil {
		value.NegotiatedExtensions = cloneAnyMap(value.ClientCapabilities.Extensions)
	}
	if request.Params != nil {
		value.RequestState = request.Params.RequestState
		value.InputResponses = inputResponses(request.Params.InputResponses)
		if raw, exists := request.Params.Meta[requestMetaLogLevelKey]; exists {
			value.LogLevelHint, _ = raw.(string)
		}
	}
	return cloneRequestContext(value)
}

func cloneRequestContext(value RequestContext) RequestContext {
	value.ProtocolVersion = strings.TrimSpace(value.ProtocolVersion)
	value.ClientInfo = cloneClientInfo(value.ClientInfo)
	value.ClientCapabilities = cloneClientCapabilities(value.ClientCapabilities)
	value.NegotiatedExtensions = cloneAnyMap(value.NegotiatedExtensions)
	value.InputResponses = cloneAnyMap(value.InputResponses)
	return value
}

func cloneClientInfo(value *sdkmcp.Implementation) *sdkmcp.Implementation {
	if value == nil {
		return nil
	}
	var result sdkmcp.Implementation
	if remarshalRequestContextValue(value, &result) != nil {
		return nil
	}
	return &result
}

func cloneClientCapabilities(value *sdkmcp.ClientCapabilities) *sdkmcp.ClientCapabilities {
	if value == nil {
		return nil
	}
	var result sdkmcp.ClientCapabilities
	if remarshalRequestContextValue(value, &result) != nil {
		return nil
	}
	return &result
}

func cloneAnyMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	result := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		return nil
	}
	return result
}

func remarshalRequestContextValue(value any, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
