package semantic

import (
	"errors"
	"fmt"
	"strings"
)

type ErrorCategory string

const (
	ErrorDisabled        ErrorCategory = "disabled"
	ErrorUnavailable     ErrorCategory = "unavailable"
	ErrorMisconfigured   ErrorCategory = "misconfigured"
	ErrorTimeout         ErrorCategory = "timeout"
	ErrorRateLimited     ErrorCategory = "rate_limited"
	ErrorOverloaded      ErrorCategory = "overloaded"
	ErrorUnauthorized    ErrorCategory = "unauthorized"
	ErrorInvalidRequest  ErrorCategory = "invalid_request"
	ErrorInvalidResponse ErrorCategory = "invalid_response"
	ErrorTransport       ErrorCategory = "transport"
	ErrorProvider        ErrorCategory = "provider"
	ErrorCancelled       ErrorCategory = "cancelled"
)

type Error struct {
	Category ErrorCategory `json:"category"`
	Detail   string        `json:"-"`
}

type ErrorMetadata struct {
	Category ErrorCategory `json:"category"`
}

func NewError(category ErrorCategory, detail string) *Error {
	return &Error{Category: category, Detail: strings.TrimSpace(detail)}
}

func (e *Error) Metadata() ErrorMetadata {
	if e == nil {
		return ErrorMetadata{}
	}
	return ErrorMetadata{Category: e.Category}
}

func (e *Error) Error() string {
	if e == nil {
		return "semantic evaluation failed"
	}
	if e.Detail != "" {
		return fmt.Sprintf("semantic evaluation failed: %s: %s", e.Category, e.Detail)
	}
	if e.Category != "" {
		return "semantic evaluation failed: " + string(e.Category)
	}
	return "semantic evaluation failed"
}

func AsError(err error) (*Error, bool) {
	var value *Error
	if !errors.As(err, &value) || value == nil {
		return nil, false
	}
	return value, true
}

func IsCategory(err error, category ErrorCategory) bool {
	value, ok := AsError(err)
	return ok && value.Category == category
}
