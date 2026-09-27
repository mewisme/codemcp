package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type capturedProductUsage struct {
	name  producttelemetry.EventName
	usage producttelemetry.Usage
}

type fakeProductUsageRecorder struct {
	values []capturedProductUsage
}

func (recorder *fakeProductUsageRecorder) Record(_ context.Context, name producttelemetry.EventName, usage producttelemetry.Usage) bool {
	recorder.values = append(recorder.values, capturedProductUsage{name: name, usage: usage})
	return true
}

func TestDispatcherProductObservationIsSafeAndExactlyOnceAcrossInterfaces(t *testing.T) {
	for _, iface := range []OperationInterface{
		OperationInterfaceCLI, OperationInterfaceTUI, OperationInterfaceAdmin, OperationInterfaceMCP,
	} {
		t.Run(string(iface), func(t *testing.T) {
			recorder := &fakeProductUsageRecorder{}
			dispatcher := NewDispatcher()
			dispatcher.SetObserver(ProductOperationObserver(recorder))
			const secret = "/home/private/workspace SECRET_RESULT"
			if err := dispatcher.Register(capability.WorkspaceList, func(context.Context, any) (any, error) {
				return secret, nil
			}); err != nil {
				t.Fatal(err)
			}
			var localEvents []tracepkg.Event
			ctx := WithOperationInterface(t.Context(), iface)
			ctx = tracepkg.WithObserver(ctx, func(event tracepkg.Event) { localEvents = append(localEvents, event) })
			result, err := dispatcher.Dispatch(ctx, DispatchRequest{
				Operation: capability.WorkspaceList,
				Input:     map[string]any{"path": secret, "args": []string{"--secret"}},
			})
			if err != nil || result.Value != secret {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if !OperationObserved(ctx) {
				t.Fatal("dispatch context was not marked observed")
			}
			if len(recorder.values) != 1 {
				t.Fatalf("remote observations=%d want=1; local traces=%d", len(recorder.values), len(localEvents))
			}
			got := recorder.values[0]
			if got.name != producttelemetry.EventOperationCompleted ||
				got.usage.Interface != producttelemetry.Interface(iface) ||
				got.usage.Command != string(capability.WorkspaceList) ||
				got.usage.Feature != string(capability.WorkspaceList) ||
				!got.usage.Success || got.usage.ErrorCode != "" || got.usage.Duration < 0 {
				t.Fatalf("usage=%#v", got)
			}
			if strings.Contains(got.usage.Command+got.usage.Feature, secret) {
				t.Fatalf("usage leaked request/result material: %#v", got)
			}
		})
	}
}

func TestDispatcherProductObservationMapsFailureWithoutRawError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code producttelemetry.ErrorCode
	}{
		{"cancelled", context.Canceled, producttelemetry.ErrorCancelled},
		{"timeout", context.DeadlineExceeded, producttelemetry.ErrorTimeout},
		{"invalid", &OperationError{Code: ErrorInvalidArgument, Err: errors.New("private invalid path")}, producttelemetry.ErrorInvalidInput},
		{"not-found", &OperationError{Code: ErrorNotFound, Err: errors.New("private missing id")}, producttelemetry.ErrorNotFound},
		{"conflict", &OperationError{Code: ErrorConflict, Err: errors.New("private conflict")}, producttelemetry.ErrorConflict},
		{"unavailable", &OperationError{Code: ErrorUnavailable, Err: errors.New("private provider body")}, producttelemetry.ErrorUnavailable},
		{"internal", errors.New("private stack and token"), producttelemetry.ErrorInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &fakeProductUsageRecorder{}
			dispatcher := NewDispatcher()
			dispatcher.SetObserver(ProductOperationObserver(recorder))
			if err := dispatcher.Register(capability.WorkspaceList, func(context.Context, any) (any, error) {
				time.Sleep(time.Microsecond)
				return nil, test.err
			}); err != nil {
				t.Fatal(err)
			}
			ctx := WithOperationInterface(t.Context(), OperationInterfaceAdmin)
			_, _ = dispatcher.Dispatch(ctx, DispatchRequest{Operation: capability.WorkspaceList, Input: "SECRET_ARGUMENT"})
			if len(recorder.values) != 1 {
				t.Fatalf("observations=%d", len(recorder.values))
			}
			got := recorder.values[0].usage
			if got.Success || got.ErrorCode != test.code || got.Duration < 0 {
				t.Fatalf("usage=%#v", got)
			}
			if strings.Contains(got.Command+got.Feature+string(got.ErrorCode), "private") ||
				strings.Contains(got.Command+got.Feature, "SECRET_ARGUMENT") {
				t.Fatalf("bounded failure telemetry leaked raw data: %#v", got)
			}
		})
	}
}

func TestTelemetryAdministrationIsNotSelfObserved(t *testing.T) {
	recorder := &fakeProductUsageRecorder{}
	dispatcher := NewDispatcher()
	dispatcher.SetObserver(ProductOperationObserver(recorder))
	if err := dispatcher.Register(capability.TelemetryStatus, func(context.Context, any) (any, error) { return struct{}{}, nil }); err != nil {
		t.Fatal(err)
	}
	ctx := WithOperationInterface(t.Context(), OperationInterfaceCLI)
	if _, err := dispatcher.Dispatch(ctx, DispatchRequest{Operation: capability.TelemetryStatus}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.values) != 0 {
		t.Fatalf("telemetry administration self-observed: %#v", recorder.values)
	}
}
