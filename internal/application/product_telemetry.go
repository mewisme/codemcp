package application

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

type OperationInterface string

const (
	OperationInterfaceCLI   OperationInterface = "cli"
	OperationInterfaceTUI   OperationInterface = "tui"
	OperationInterfaceAdmin OperationInterface = "admin"
	OperationInterfaceMCP   OperationInterface = "mcp"
)

type operationInterfaceKey struct{}

type operationContextState struct {
	Interface OperationInterface
	Observed  atomic.Bool
}

func WithOperationInterface(ctx context.Context, value OperationInterface) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, operationInterfaceKey{}, &operationContextState{Interface: value})
}

func OperationInterfaceFromContext(ctx context.Context) OperationInterface {
	if ctx == nil {
		return ""
	}
	state, _ := ctx.Value(operationInterfaceKey{}).(*operationContextState)
	if state == nil {
		return ""
	}
	return state.Interface
}

func OperationObserved(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	state, _ := ctx.Value(operationInterfaceKey{}).(*operationContextState)
	return state != nil && state.Observed.Load()
}

func markOperationObserved(ctx context.Context) {
	if ctx == nil {
		return
	}
	state, _ := ctx.Value(operationInterfaceKey{}).(*operationContextState)
	if state != nil {
		state.Observed.Store(true)
	}
}

type OperationObservation struct {
	Operation capability.ID
	Interface OperationInterface
	Duration  time.Duration
	Success   bool
	ErrorCode producttelemetry.ErrorCode
}

type OperationObserver func(context.Context, OperationObservation)

type ProductUsageRecorder interface {
	Record(context.Context, producttelemetry.EventName, producttelemetry.Usage) bool
}

func ProductOperationObserver(recorder ProductUsageRecorder) OperationObserver {
	if recorder == nil {
		return nil
	}
	return func(ctx context.Context, observation OperationObservation) {
		if isTelemetryAdministration(observation.Operation) {
			return
		}
		recorder.Record(ctx, producttelemetry.EventOperationCompleted, producttelemetry.Usage{
			Interface: producttelemetry.Interface(observation.Interface),
			Command:   string(observation.Operation),
			Feature:   string(observation.Operation),
			ErrorCode: observation.ErrorCode,
			Duration:  observation.Duration,
			Success:   observation.Success,
		})
	}
}

func ProductErrorCode(err error) producttelemetry.ErrorCode {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return producttelemetry.ErrorCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return producttelemetry.ErrorTimeout
	}
	switch ErrorCodeOf(err) {
	case ErrorInvalidArgument:
		return producttelemetry.ErrorInvalidInput
	case ErrorNotFound:
		return producttelemetry.ErrorNotFound
	case ErrorConflict:
		return producttelemetry.ErrorConflict
	case ErrorUnavailable:
		return producttelemetry.ErrorUnavailable
	default:
		return producttelemetry.ErrorInternal
	}
}

func isTelemetryAdministration(id capability.ID) bool {
	switch id {
	case capability.TelemetryStatus, capability.TelemetryEnable, capability.TelemetryDisable, capability.TelemetryShow:
		return true
	default:
		return false
	}
}
