package application

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.mewis.me/codemcp/internal/capability"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type ErrorCode string

const (
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	ErrorNotFound        ErrorCode = "not_found"
	ErrorConflict        ErrorCode = "conflict"
	ErrorUnavailable     ErrorCode = "unavailable"
	ErrorInternal        ErrorCode = "internal"
	ErrorUnsupported     ErrorCode = "unsupported"
)

type OperationError struct {
	Operation capability.ID
	Code      ErrorCode
	Err       error
}

func (err *OperationError) Error() string {
	if err == nil || err.Err == nil {
		return "operation failed"
	}
	return err.Err.Error()
}

func (err *OperationError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func ErrorCodeOf(err error) ErrorCode {
	var operationErr *OperationError
	if errors.As(err, &operationErr) {
		return operationErr.Code
	}
	return ErrorInternal
}

type Result[T any] struct {
	Operation capability.ID
	Value     T
}

type DispatchRequest struct {
	Operation capability.ID
	Input     any
}

type DispatchResult struct {
	Operation capability.ID
	Metadata  capability.Spec
	Value     any
}

type OperationHandler func(context.Context, any) (any, error)

type OperationDispatcher interface {
	Dispatch(context.Context, DispatchRequest) (DispatchResult, error)
}

type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[capability.ID]OperationHandler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: map[capability.ID]OperationHandler{}}
}

func (dispatcher *Dispatcher) Register(id capability.ID, handler OperationHandler) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if _, ok := capability.Lookup(id); !ok {
		return fmt.Errorf("unknown canonical operation: %s", id)
	}
	if handler == nil {
		return fmt.Errorf("operation %s handler is required", id)
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if _, exists := dispatcher.handlers[id]; exists {
		return fmt.Errorf("operation %s already has an application handler", id)
	}
	dispatcher.handlers[id] = handler
	return nil
}

func (dispatcher *Dispatcher) Dispatch(ctx context.Context, request DispatchRequest) (DispatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	spec, ok := capability.Lookup(request.Operation)
	if !ok {
		return DispatchResult{}, &OperationError{Operation: request.Operation, Code: ErrorUnsupported, Err: fmt.Errorf("unknown canonical operation: %s", request.Operation)}
	}
	if dispatcher == nil {
		return DispatchResult{}, &OperationError{Operation: request.Operation, Code: ErrorUnavailable, Err: errors.New("operation dispatcher is unavailable")}
	}
	dispatcher.mu.RLock()
	handler := dispatcher.handlers[request.Operation]
	dispatcher.mu.RUnlock()
	if handler == nil {
		return DispatchResult{}, &OperationError{Operation: request.Operation, Code: ErrorUnsupported, Err: fmt.Errorf("canonical operation is not bound to an application handler: %s", request.Operation)}
	}

	value, err := handler(ctx, request.Input)
	if err != nil {
		return DispatchResult{}, normalizeOperationError(request.Operation, err)
	}
	return DispatchResult{Operation: request.Operation, Metadata: spec, Value: value}, nil
}

func normalizeOperationError(id capability.ID, err error) error {
	if err == nil {
		return nil
	}
	var operationErr *OperationError
	if errors.As(err, &operationErr) {
		if operationErr.Operation == "" {
			operationErr.Operation = id
		}
		return operationErr
	}
	code := ErrorInternal
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = ErrorUnavailable
	}
	return &OperationError{Operation: id, Code: code, Err: err}
}

func operationError(id capability.ID, code ErrorCode, err error) error {
	if err == nil {
		return nil
	}
	return &OperationError{Operation: id, Code: code, Err: err}
}

func runOperation[T any](ctx context.Context, component string, id capability.ID, message string, fields []tracepkg.Field, fn func() (T, error)) (Result[T], error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var zero T
	span := tracepkg.Start(ctx, component, string(id), message, fields...)
	value, err := fn()
	if err != nil {
		err = normalizeOperationError(id, err)
		span.FailMessage(message+" failed", err, tracepkg.String("operation_id", string(id)), tracepkg.String("error_code", string(ErrorCodeOf(err))))
		return Result[T]{Operation: id, Value: zero}, err
	}
	span.EndMessage(message+" completed", tracepkg.String("operation_id", string(id)))
	return Result[T]{Operation: id, Value: value}, nil
}
