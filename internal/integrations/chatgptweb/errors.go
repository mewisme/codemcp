package chatgptweb

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorUIContract          ErrorCode = "ui_contract"
	ErrorAuthentication      ErrorCode = "authentication"
	ErrorModelMismatch       ErrorCode = "model_mismatch"
	ErrorEffortMismatch      ErrorCode = "effort_mismatch"
	ErrorConnectorMismatch   ErrorCode = "connector_mismatch"
	ErrorRateLimited         ErrorCode = "rate_limited"
	ErrorUpstream            ErrorCode = "upstream"
	ErrorSubmissionAmbiguous ErrorCode = "submission_ambiguous"
	ErrorCompletionAmbiguous ErrorCode = "completion_ambiguous"
	ErrorCancelled           ErrorCode = "cancelled"
)

type DriverError struct {
	Code ErrorCode
	Op   string
	Msg  string
	Err  error
}

func (err *DriverError) Error() string {
	if err == nil {
		return ""
	}
	prefix := "ChatGPT Web"
	if err.Op != "" {
		prefix += " " + err.Op
	}
	if err.Msg != "" {
		return prefix + ": " + err.Msg
	}
	if err.Err != nil {
		return prefix + ": " + err.Err.Error()
	}
	return prefix
}

func (err *DriverError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func driverError(code ErrorCode, op, message string, cause error) error {
	return &DriverError{Code: code, Op: op, Msg: message, Err: cause}
}

func IsDriverErrorCode(err error, code ErrorCode) bool {
	var target *DriverError
	return errors.As(err, &target) && target.Code == code
}

func uiContractError(op, format string, args ...any) error {
	return driverError(ErrorUIContract, op, fmt.Sprintf(format, args...), nil)
}
