package mcp

import (
	"fmt"
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/tools"
)

const (
	ProtocolVersionHeader = "MCP-Protocol-Version"
	MethodHeader          = "Mcp-Method"
	NameHeader            = "Mcp-Name"
	SessionIDHeader       = "Mcp-Session-Id"
)

func validateHTTPMirrorHeaders(r *http.Request, req Request, params map[string]any) *Error {
	protocolVersion := strings.TrimSpace(r.Header.Get(ProtocolVersionHeader))
	if protocolVersion == "" {
		return NewError(ErrHeaderMismatch, "missing MCP-Protocol-Version header")
	}
	if protocolVersion != SupportedProtocolVersion {
		return NewErrorData(ErrUnsupportedProtocolVersion, fmt.Sprintf("unsupported protocol version %q", protocolVersion), map[string]any{
			"supported": []string{SupportedProtocolVersion},
			"requested": protocolVersion,
		})
	}

	method := strings.TrimSpace(r.Header.Get(MethodHeader))
	if method == "" {
		return NewError(ErrHeaderMismatch, "missing Mcp-Method header")
	}
	if method != req.Method {
		return NewError(ErrHeaderMismatch, "Mcp-Method header does not match request method")
	}

	expectedName := mirroredRequestName(req.Method, params)
	rawName := strings.TrimSpace(r.Header.Get(NameHeader))
	if expectedName != "" {
		if rawName == "" {
			return NewError(ErrHeaderMismatch, "missing Mcp-Name header")
		}
		name, err := decodeMCPHeaderValue(rawName)
		if err != nil {
			return NewError(ErrHeaderMismatch, "malformed Mcp-Name header")
		}
		if name != expectedName {
			return NewError(ErrHeaderMismatch, "Mcp-Name header does not match request parameters")
		}
	} else if rawName != "" {
		return NewError(ErrHeaderMismatch, "Mcp-Name header is not valid for this method")
	}

	if metaVersion := requestMetaProtocolVersion(params); metaVersion != "" && metaVersion != protocolVersion {
		return NewError(ErrHeaderMismatch, "protocol version metadata does not match MCP-Protocol-Version header")
	}
	return nil
}

// ValidateHTTPRoutingHeaders is the single modern HTTP routing guard for
// method/name mirrors and schema-derived tool parameter headers.
func ValidateHTTPRoutingHeaders(r *http.Request, req Request, params map[string]any, registry *tools.Registry) *Error {
	if err := validateHTTPMirrorHeaders(r, req, params); err != nil {
		return err
	}
	args, _ := params["arguments"].(map[string]any)
	if req.Method != "tools/call" {
		return validateToolParamHeaders(r, tools.Schema{}, args)
	}
	name, _ := params["name"].(string)
	if registry == nil {
		return NewError(ErrInternal, "tool registry is unavailable")
	}
	schema, ok := registry.Schema(name)
	if !ok {
		return validateToolParamHeaders(r, tools.Schema{}, args)
	}
	return validateToolParamHeaders(r, schema, args)
}

func mirroredRequestName(method string, params map[string]any) string {
	switch method {
	case "tools/call":
		name, _ := params["name"].(string)
		return name
	default:
		return ""
	}
}

func requestMetaProtocolVersion(params map[string]any) string {
	meta, _ := params["_meta"].(map[string]any)
	value, _ := meta["io.modelcontextprotocol/protocolVersion"].(string)
	return value
}
