package mcp

import (
	"encoding/json"
	"net/http"
)

func writeError(w http.ResponseWriter, code int, message string) {
	writeErrorID(w, nil, code, message)
}

func writeErrorID(w http.ResponseWriter, id any, code int, message string) {
	writeErrorStatusID(w, http.StatusOK, id, code, message)
}

func writeErrorStatusID(w http.ResponseWriter, status int, id any, code int, message string) {
	writeProtocolErrorStatusID(w, status, id, &Error{Code: code, Message: message})
}

func writeProtocolErrorStatusID(w http.ResponseWriter, status int, id any, protocolErr *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: id, Error: protocolErr})
}

func HandleRequest(runtime *ToolRuntime, req Request) Response {
	response := Response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "tools/list":
		response.Result = cacheableCompleteResult(map[string]any{"tools": runtime.ListTools()})
	default:
		response.Error = &Error{Code: ErrMethodNotFound, Message: "method not found"}
	}
	return response
}
