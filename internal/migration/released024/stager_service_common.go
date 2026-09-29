package released024

import (
	"context"
	"fmt"
	"time"

	"go.mewis.me/codemcp/internal/service"
)

type HistoricalServiceRetirer interface {
	Retire(context.Context, SourceDescriptor, ServiceState) error
}

func historicalServiceSpec(ctx context.Context, source SourceDescriptor, state ServiceState) (service.Spec, error) {
	scope := service.ScopeUser
	if state.Scope == string(service.ScopeSystem) {
		scope = service.ScopeSystem
	}
	account, err := service.InvokingAccountContext(ctx, scope)
	if err != nil {
		return service.Spec{}, err
	}
	if source.OperatorHome != "" && scope == service.ScopeUser {
		account.HomeDir = source.OperatorHome
	}
	return service.Spec{ID: state.ID, Scope: scope, ConfigRoot: state.ConfigRoot, Binary: state.Binary, Account: account}, nil
}

func waitHistoricalStopped(ctx context.Context, manager service.Manager, spec service.Spec) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		status, err := manager.Status(spec)
		if err != nil {
			return err
		}
		if !status.Running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("historical service %s did not stop", spec.ID)
		case <-ticker.C:
		}
	}
}
