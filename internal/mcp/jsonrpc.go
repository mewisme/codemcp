package mcp

import "encoding/json"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

func (r Response) MarshalJSON() ([]byte, error) {
	type wire struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      any             `json:"id,omitempty"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *Error          `json:"error,omitempty"`
	}
	var result json.RawMessage
	if r.Result != nil {
		raw, err := json.Marshal(r.Result)
		if err != nil {
			return nil, err
		}
		if r.Error == nil {
			raw, err = stampServerInfo(raw)
			if err != nil {
				return nil, err
			}
		}
		result = raw
	}
	return json.Marshal(wire{JSONRPC: r.JSONRPC, ID: r.ID, Result: result, Error: r.Error})
}

func stampServerInfo(raw []byte) (json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return append(json.RawMessage(nil), raw...), nil
	}

	meta := map[string]json.RawMessage{}
	if rawMeta, ok := result["_meta"]; ok && len(rawMeta) != 0 && string(rawMeta) != "null" {
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(rawMeta, &decoded); err == nil && decoded != nil {
			meta = decoded
		}
	}
	if _, ok := meta[serverInfoMetaKey]; !ok {
		info, err := json.Marshal(serverInfo())
		if err != nil {
			return nil, err
		}
		meta[serverInfoMetaKey] = info
	}
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	result["_meta"] = rawMeta
	return json.Marshal(result)
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
