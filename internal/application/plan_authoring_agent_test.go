package application

import (
	"errors"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestPlanAuthoringAgentMapsCanonicalErrorSemantics(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		code      ErrorCode
		retryable bool
		stale     bool
	}{
		{name: "invalid", err: ErrPlanInvalid, code: ErrorInvalidArgument},
		{name: "not found", err: ErrPlanNotFound, code: ErrorNotFound},
		{name: "conflict", err: ErrPlanConflict, code: ErrorConflict},
		{name: "stale", err: ErrPlanStale, code: ErrorConflict, retryable: true, stale: true},
		{name: "unavailable", err: ErrPlanUnavailable, code: ErrorUnavailable, retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := classifyAgentPlanAuthoringError(capability.PlanCreate, test.err)
			if !errors.Is(err, test.err) {
				t.Fatalf("wrapped error=%v", err)
			}
			semantics := ErrorSemanticsOf(err)
			if semantics.Code != test.code || semantics.Retryable != test.retryable || semantics.Stale != test.stale {
				t.Fatalf("semantics=%#v", semantics)
			}
			var operationErr *OperationError
			if !errors.As(err, &operationErr) || operationErr.Operation != capability.PlanCreate {
				t.Fatalf("operation error=%#v", operationErr)
			}
		})
	}
}
