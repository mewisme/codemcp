package mcp

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/tools"
)

const (
	ErrHeaderMismatch             = -32020
	ErrUnsupportedProtocolVersion = -32022
	ErrParse                      = -32700
	ErrInvalidRequest             = -32600
	ErrMethodNotFound             = -32601
	ErrInvalidParams              = -32602
	ErrInternal                   = -32603
)

func (e *Error) Error() string { return e.Message }

func NewError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

func NewErrorData(code int, message string, data any) *Error {
	return &Error{Code: code, Message: message, Data: data}
}

func ProtocolError(err error) *Error {
	if err == nil {
		return nil
	}
	var protocolErr *Error
	if errors.As(err, &protocolErr) {
		return protocolErr
	}
	if errors.Is(err, tools.ErrToolNotFound) {
		return NewError(ErrInvalidParams, err.Error())
	}
	return NewError(ErrInternal, err.Error())
}

// ResourceNotFoundError follows the modern MCP resource-not-found contract:
// invalid params with the unresolved URI in error data.
func ResourceNotFoundError(uri string) *Error {
	return NewErrorData(ErrInvalidParams, "Resource not found", map[string]any{"uri": strings.TrimSpace(uri)})
}
