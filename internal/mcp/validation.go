package mcp

import "encoding/json"

func ValidateRequest(req Request) error {
	if req.JSONRPC != "2.0" {
		return NewError(ErrInvalidRequest, "invalid jsonrpc version")
	}
	if req.Method == "" {
		return NewError(ErrInvalidRequest, "missing method")
	}
	if req.ID == nil {
		return NewError(ErrInvalidRequest, "request requires an id")
	}
	return nil
}

func DecodeParams(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, NewError(ErrInvalidParams, "params must be an object")
	}
	return params, nil
}

func ValidateParams(method string, params map[string]any) error {
	if err := validateRequestMeta(params); err != nil {
		return err
	}
	switch method {
	case "server/discover":
		return nil
	case "tools/list":
		if cursor, exists := params["cursor"]; exists {
			if _, ok := cursor.(string); !ok {
				return NewError(ErrInvalidParams, "cursor must be a string")
			}
		}
	case "tools/call":
		name, ok := params["name"].(string)
		if !ok || name == "" {
			return NewError(ErrInvalidParams, "missing tool name")
		}
		if args, exists := params["arguments"]; exists {
			if _, ok := args.(map[string]any); !ok {
				return NewError(ErrInvalidParams, "arguments must be an object")
			}
		}
		if requestState, exists := params["requestState"]; exists {
			if _, ok := requestState.(string); !ok {
				return NewError(ErrInvalidParams, "requestState must be a string")
			}
		}
		if inputResponses, exists := params["inputResponses"]; exists {
			if _, ok := inputResponses.(map[string]any); !ok {
				return NewError(ErrInvalidParams, "inputResponses must be an object")
			}
		}
	case "subscriptions/listen":
		return validateListenNotifications(params)
	case CompletionCompleteMethod:
		return validateCompletionParams(params)
	}
	return nil
}

func validateRequestMeta(params map[string]any) error {
	value, exists := params["_meta"]
	if !exists {
		return NewError(ErrInvalidParams, "_meta is required")
	}
	meta, ok := value.(map[string]any)
	if !ok {
		return NewError(ErrInvalidParams, "_meta must be an object")
	}
	version, ok := meta["io.modelcontextprotocol/protocolVersion"].(string)
	if !ok || version == "" {
		return NewError(ErrInvalidParams, "protocolVersion metadata is required and must be a string")
	}
	if value, exists := meta["io.modelcontextprotocol/clientInfo"]; exists {
		if _, ok := value.(map[string]any); !ok {
			return NewError(ErrInvalidParams, "clientInfo metadata must be an object")
		}
	}
	if value, exists := meta["io.modelcontextprotocol/clientCapabilities"]; !exists {
		return NewError(ErrInvalidParams, "clientCapabilities metadata is required")
	} else if _, ok := value.(map[string]any); !ok {
		return NewError(ErrInvalidParams, "clientCapabilities metadata must be an object")
	}
	return nil
}
