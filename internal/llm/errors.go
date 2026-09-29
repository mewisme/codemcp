package llm

import (
	"errors"
	"fmt"
	"strings"
)

type ErrorCategory string

const (
	ErrorInvalidID        ErrorCategory = "invalid_id"
	ErrorReservedID       ErrorCategory = "reserved_id"
	ErrorDuplicateID      ErrorCategory = "duplicate_id"
	ErrorInvalidProvider  ErrorCategory = "invalid_provider"
	ErrorInvalidProtocol  ErrorCategory = "invalid_protocol"
	ErrorInvalidAuth      ErrorCategory = "invalid_auth"
	ErrorInvalidDiscovery ErrorCategory = "invalid_discovery"
	ErrorInvalidEndpoint  ErrorCategory = "invalid_endpoint"
	ErrorCoreInvariant    ErrorCategory = "core_invariant"
	ErrorMissingCore      ErrorCategory = "missing_core"
	ErrorInvalidActive    ErrorCategory = "invalid_active_provider"
	ErrorProviderNotFound ErrorCategory = "provider_not_found"
	ErrorActiveRemoval    ErrorCategory = "active_provider_removal"
	ErrorUnsupported      ErrorCategory = "unsupported"
	ErrorMisconfigured    ErrorCategory = "misconfigured"
	ErrorUnavailable      ErrorCategory = "unavailable"
	ErrorUnauthorized     ErrorCategory = "unauthorized"
	ErrorRateLimited      ErrorCategory = "rate_limited"
	ErrorTimeout          ErrorCategory = "timeout"
	ErrorInvalidRequest   ErrorCategory = "invalid_request"
	ErrorInvalidResponse  ErrorCategory = "invalid_response"
	ErrorTransport        ErrorCategory = "transport"
	ErrorProvider         ErrorCategory = "provider"
	ErrorCancelled        ErrorCategory = "cancelled"
)

type Error struct {
	Category ErrorCategory `json:"category"`
	Field    string        `json:"field,omitempty"`
	Detail   string        `json:"-"`
}

func NewError(category ErrorCategory, field, detail string) *Error {
	return &Error{Category: category, Field: strings.TrimSpace(field), Detail: strings.TrimSpace(detail)}
}

func (e *Error) Error() string {
	if e == nil {
		return "llm operation failed"
	}
	prefix := "llm operation failed"
	if e.Category != "" {
		prefix += ": " + string(e.Category)
	}
	if e.Field != "" {
		prefix += ": " + e.Field
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s", prefix, e.Detail)
	}
	return prefix
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
