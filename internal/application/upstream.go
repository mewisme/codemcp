package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/upstream"
)

type UpstreamService struct {
	manager   *upstream.Manager
	reconcile func(context.Context) error
}

type UpstreamToolsView struct {
	Server       upstream.Server `json:"server"`
	Tools        []upstream.Tool `json:"tools"`
	ProxiedTools []string        `json:"proxied_tools"`
}

type UpstreamIDInput struct {
	ID string
}

type UpstreamStatusInput struct {
	ID      string
	Refresh bool
}

type UpstreamServerInput struct {
	Server upstream.Server
}

type UpstreamUpdateInput struct {
	ID     string
	Server upstream.Server
}

func NewUpstreamService(manager *upstream.Manager, reconcile ...func(context.Context) error) *UpstreamService {
	service := &UpstreamService{manager: manager}
	if len(reconcile) > 0 {
		service.reconcile = reconcile[0]
	}
	return service
}

func LoadUpstreamService(ctx context.Context) (*UpstreamService, error) {
	manager := upstream.NewManager(upstream.NewStore(upstream.Path())).SetTraceObserver(tracepkg.ObserverFromContext(ctx))
	if err := manager.Load(); err != nil {
		return nil, fmt.Errorf("load upstream configuration: %w", err)
	}
	return NewUpstreamService(manager), nil
}

func (service *UpstreamService) Manager() *upstream.Manager {
	if service == nil {
		return nil
	}
	return service.manager
}

func (service *UpstreamService) List(ctx context.Context) (Result[[]upstream.Server], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerList, "Listing Upstream servers", nil, func() ([]upstream.Server, error) {
		if err := service.require(capability.UpstreamServerList); err != nil {
			return nil, err
		}
		return service.manager.List(), nil
	})
}

func (service *UpstreamService) Statuses(ctx context.Context, refresh bool) (Result[[]upstream.Status], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerList, "Inspecting Upstream server status", []tracepkg.Field{tracepkg.Bool("refresh", refresh)}, func() ([]upstream.Status, error) {
		if err := service.require(capability.UpstreamServerList); err != nil {
			return nil, err
		}
		return service.manager.ListStatuses(ctx, refresh), nil
	})
}

func (service *UpstreamService) Get(ctx context.Context, id string) (Result[upstream.Server], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerShow, "Loading Upstream server", upstreamOperationFields(id), func() (upstream.Server, error) {
		if err := service.require(capability.UpstreamServerShow); err != nil {
			return upstream.Server{}, err
		}
		return service.get(capability.UpstreamServerShow, id)
	})
}

func (service *UpstreamService) Create(ctx context.Context, server upstream.Server) (Result[upstream.Server], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerAdd, "Adding Upstream server", upstreamOperationFields(server.ID), func() (upstream.Server, error) {
		if err := service.require(capability.UpstreamServerAdd); err != nil {
			return upstream.Server{}, err
		}
		normalized, err := normalizeApplicationUpstream(capability.UpstreamServerAdd, server)
		if err != nil {
			return upstream.Server{}, err
		}
		if _, exists := service.manager.Get(normalized.ID); exists {
			return upstream.Server{}, operationError(capability.UpstreamServerAdd, ErrorConflict, fmt.Errorf("upstream server already exists: %s", normalized.ID))
		}
		if err := service.manager.Add(normalized); err != nil {
			return upstream.Server{}, classifyUpstreamRuntimeError(capability.UpstreamServerAdd, err)
		}
		if err := service.reconcileOnce(ctx, capability.UpstreamServerAdd); err != nil {
			return upstream.Server{}, err
		}
		value, _ := service.manager.Get(normalized.ID)
		return value, nil
	})
}

func (service *UpstreamService) CreateBatch(ctx context.Context, servers []upstream.Server) (Result[[]upstream.Server], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerAdd, "Adding Upstream servers", []tracepkg.Field{tracepkg.Int("count", len(servers))}, func() ([]upstream.Server, error) {
		if err := service.require(capability.UpstreamServerAdd); err != nil {
			return nil, err
		}
		normalized := make([]upstream.Server, 0, len(servers))
		seen := map[string]bool{}
		for _, server := range servers {
			value, err := normalizeApplicationUpstream(capability.UpstreamServerAdd, server)
			if err != nil {
				return nil, err
			}
			if seen[value.ID] {
				return nil, operationError(capability.UpstreamServerAdd, ErrorConflict, fmt.Errorf("duplicate upstream server ID: %s", value.ID))
			}
			if _, exists := service.manager.Get(value.ID); exists {
				return nil, operationError(capability.UpstreamServerAdd, ErrorConflict, fmt.Errorf("upstream server already exists: %s", value.ID))
			}
			seen[value.ID] = true
			normalized = append(normalized, value)
		}
		if err := service.manager.CreateBatch(normalized); err != nil {
			return nil, classifyUpstreamRuntimeError(capability.UpstreamServerAdd, err)
		}
		if err := service.reconcileOnce(ctx, capability.UpstreamServerAdd); err != nil {
			return nil, err
		}
		result := make([]upstream.Server, 0, len(normalized))
		for _, server := range normalized {
			value, _ := service.manager.Get(server.ID)
			result = append(result, value)
		}
		return result, nil
	})
}

func (service *UpstreamService) Update(ctx context.Context, id string, server upstream.Server) (Result[upstream.Server], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerConfigure, "Updating Upstream server", upstreamOperationFields(id), func() (upstream.Server, error) {
		if err := service.require(capability.UpstreamServerConfigure); err != nil {
			return upstream.Server{}, err
		}
		if _, err := service.get(capability.UpstreamServerConfigure, id); err != nil {
			return upstream.Server{}, err
		}
		serverID := strings.TrimSpace(server.ID)
		if serverID != "" && serverID != strings.TrimSpace(id) {
			return upstream.Server{}, operationError(capability.UpstreamServerConfigure, ErrorInvalidArgument, errors.New("upstream id cannot be changed"))
		}
		server.ID = strings.TrimSpace(id)
		normalized, err := normalizeApplicationUpstream(capability.UpstreamServerConfigure, server)
		if err != nil {
			return upstream.Server{}, err
		}
		if err := service.manager.Add(normalized); err != nil {
			return upstream.Server{}, classifyUpstreamRuntimeError(capability.UpstreamServerConfigure, err)
		}
		if err := service.reconcileOnce(ctx, capability.UpstreamServerConfigure); err != nil {
			return upstream.Server{}, err
		}
		value, _ := service.manager.Get(normalized.ID)
		return value, nil
	})
}

func (service *UpstreamService) applySettingUpdate(ctx context.Context, id string, server upstream.Server) (upstream.Server, bool, error) {
	if err := service.require(capability.UpstreamServerConfigure); err != nil {
		return upstream.Server{}, false, err
	}
	previous, err := service.get(capability.UpstreamServerConfigure, id)
	if err != nil {
		return upstream.Server{}, false, err
	}
	serverID := strings.TrimSpace(server.ID)
	if serverID != "" && serverID != strings.TrimSpace(id) {
		return upstream.Server{}, false, operationError(capability.UpstreamServerConfigure, ErrorInvalidArgument, errors.New("upstream id cannot be changed"))
	}
	server.ID = strings.TrimSpace(id)
	normalized, err := normalizeApplicationUpstream(capability.UpstreamServerConfigure, server)
	if err != nil {
		return upstream.Server{}, false, err
	}
	if reflect.DeepEqual(previous, normalized) {
		return previous, false, nil
	}
	if err := service.manager.Add(normalized); err != nil {
		cause := classifyUpstreamRuntimeError(capability.UpstreamServerConfigure, err)
		if current, ok := service.manager.Get(previous.ID); ok && !reflect.DeepEqual(current, previous) {
			return upstream.Server{}, false, service.rollbackSettingUpdate(previous, cause)
		}
		return upstream.Server{}, false, cause
	}
	reconciled := false
	if service.reconcile != nil {
		if err := service.reconcileOnce(ctx, capability.UpstreamServerConfigure); err != nil {
			return upstream.Server{}, false, service.rollbackSettingUpdate(previous, err)
		}
		reconciled = true
	} else {
		_, running, err := ReloadUpstreams(ctx)
		if err != nil {
			cause := operationError(capability.UpstreamServerConfigure, ErrorUnavailable, fmt.Errorf("upstream configuration saved but runtime proxy reconciliation failed: %w", err))
			return upstream.Server{}, false, service.rollbackSettingUpdate(previous, cause)
		}
		reconciled = running
	}
	value, _ := service.manager.Get(normalized.ID)
	return value, reconciled, nil
}

func (service *UpstreamService) rollbackSettingUpdate(previous upstream.Server, cause error) error {
	if rollbackErr := service.manager.Add(previous); rollbackErr != nil {
		return fmt.Errorf("%w; rollback upstream configuration: %v; manual reconciliation required", cause, rollbackErr)
	}
	return fmt.Errorf("%w; upstream configuration rolled back", cause)
}

func ReloadUpstreams(ctx context.Context) (runtimecontrol.UpstreamReloadResult, bool, error) {
	var result runtimecontrol.UpstreamReloadResult
	state, err := runtimecontrol.Request(ctx, http.MethodPost, "/upstreams/reload", nil, &result)
	if err != nil {
		if runtimecontrol.IsUnavailable(err) {
			return runtimecontrol.UpstreamReloadResult{}, false, nil
		}
		return runtimecontrol.UpstreamReloadResult{}, true, err
	}
	if err := runtimecontrol.ValidatePID(ctx, state.PID, result.PID, "upstreams-reload"); err != nil {
		return runtimecontrol.UpstreamReloadResult{}, true, err
	}
	return result, true, nil
}

func (service *UpstreamService) Remove(ctx context.Context, id string) (Result[upstream.Server], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerRemove, "Removing Upstream server", upstreamOperationFields(id), func() (upstream.Server, error) {
		if err := service.require(capability.UpstreamServerRemove); err != nil {
			return upstream.Server{}, err
		}
		current, err := service.get(capability.UpstreamServerRemove, id)
		if err != nil {
			return upstream.Server{}, err
		}
		if err := service.manager.Remove(current.ID); err != nil {
			return upstream.Server{}, classifyUpstreamRuntimeError(capability.UpstreamServerRemove, err)
		}
		if err := service.reconcileOnce(ctx, capability.UpstreamServerRemove); err != nil {
			return upstream.Server{}, err
		}
		return current, nil
	})
}

func (service *UpstreamService) Enable(ctx context.Context, id string) (Result[upstream.Server], error) {
	return service.setEnabled(ctx, capability.UpstreamServerEnable, id, true)
}

func (service *UpstreamService) Disable(ctx context.Context, id string) (Result[upstream.Server], error) {
	return service.setEnabled(ctx, capability.UpstreamServerDisable, id, false)
}

func (service *UpstreamService) Status(ctx context.Context, id string, refresh bool) (Result[upstream.Status], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerStatus, "Inspecting Upstream server status", append(upstreamOperationFields(id), tracepkg.Bool("refresh", refresh)), func() (upstream.Status, error) {
		if err := service.require(capability.UpstreamServerStatus); err != nil {
			return upstream.Status{}, err
		}
		value, err := service.get(capability.UpstreamServerStatus, id)
		if err != nil {
			return upstream.Status{}, err
		}
		return service.manager.CheckHealth(ctx, value.ID, refresh), nil
	})
}

func (service *UpstreamService) Tools(ctx context.Context, id string, refresh bool) (Result[UpstreamToolsView], error) {
	return runOperation(ctx, "UPSTREAM", capability.UpstreamServerTools, "Listing Upstream server tools", append(upstreamOperationFields(id), tracepkg.Bool("refresh", refresh)), func() (UpstreamToolsView, error) {
		if err := service.require(capability.UpstreamServerTools); err != nil {
			return UpstreamToolsView{}, err
		}
		server, err := service.get(capability.UpstreamServerTools, id)
		if err != nil {
			return UpstreamToolsView{}, err
		}
		values, err := service.manager.Tools(ctx, server.ID, refresh)
		if err != nil {
			return UpstreamToolsView{}, classifyUpstreamRuntimeError(capability.UpstreamServerTools, err)
		}
		return UpstreamToolsView{
			Server:       server,
			Tools:        append([]upstream.Tool(nil), values...),
			ProxiedTools: service.manager.ProxiedToolNames(server, values),
		}, nil
	})
}

func (service *UpstreamService) Disconnect(id string) error {
	if err := service.require(capability.UpstreamServerStatus); err != nil {
		return err
	}
	value, err := service.get(capability.UpstreamServerStatus, id)
	if err != nil {
		return err
	}
	return service.manager.Disconnect(value.ID)
}

func (service *UpstreamService) setEnabled(ctx context.Context, operation capability.ID, id string, enabled bool) (Result[upstream.Server], error) {
	message := "Enabling Upstream server"
	if !enabled {
		message = "Disabling Upstream server"
	}
	return runOperation(ctx, "UPSTREAM", operation, message, upstreamOperationFields(id), func() (upstream.Server, error) {
		if err := service.require(operation); err != nil {
			return upstream.Server{}, err
		}
		value, err := service.get(operation, id)
		if err != nil {
			return upstream.Server{}, err
		}
		if value.Enabled == enabled {
			return value, nil
		}
		value.Enabled = enabled
		if err := service.manager.Add(value); err != nil {
			return upstream.Server{}, classifyUpstreamRuntimeError(operation, err)
		}
		if err := service.reconcileOnce(ctx, operation); err != nil {
			return upstream.Server{}, err
		}
		updated, _ := service.manager.Get(value.ID)
		return updated, nil
	})
}

func (service *UpstreamService) require(operation capability.ID) error {
	if service == nil || service.manager == nil {
		return operationError(operation, ErrorUnavailable, errors.New("upstream manager is unavailable"))
	}
	return nil
}

func (service *UpstreamService) get(operation capability.ID, id string) (upstream.Server, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return upstream.Server{}, operationError(operation, ErrorInvalidArgument, errors.New("upstream server id is required"))
	}
	value, ok := service.manager.Get(id)
	if !ok {
		return upstream.Server{}, operationError(operation, ErrorNotFound, fmt.Errorf("unknown upstream server: %s", id))
	}
	return value, nil
}

func (service *UpstreamService) reconcileOnce(ctx context.Context, operation capability.ID) error {
	if service == nil || service.reconcile == nil {
		return nil
	}
	if err := service.reconcile(ctx); err != nil {
		return operationError(operation, ErrorUnavailable, fmt.Errorf("upstream configuration saved but runtime proxy reconciliation failed: %w", err))
	}
	return nil
}

func normalizeApplicationUpstream(operation capability.ID, server upstream.Server) (upstream.Server, error) {
	value, err := upstream.NormalizeServer(server)
	if err != nil {
		return upstream.Server{}, operationError(operation, ErrorInvalidArgument, err)
	}
	return value, nil
}

func classifyUpstreamRuntimeError(operation capability.ID, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return operationError(operation, ErrorUnavailable, err)
	default:
		return operationError(operation, ErrorInternal, err)
	}
}

func upstreamOperationFields(id string) []tracepkg.Field {
	return []tracepkg.Field{tracepkg.String("server_id", strings.TrimSpace(id))}
}

func BindUpstreamOperations(dispatcher *Dispatcher, service *UpstreamService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		return errors.New("upstream service is nil")
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.UpstreamServerList, func(ctx context.Context, _ any) (any, error) {
			result, err := service.List(ctx)
			return result.Value, err
		}},
		{capability.UpstreamServerShow, typedOperation[UpstreamIDInput](capability.UpstreamServerShow, func(ctx context.Context, input UpstreamIDInput) (any, error) {
			result, err := service.Get(ctx, input.ID)
			return result.Value, err
		})},
		{capability.UpstreamServerAdd, typedOperation[UpstreamServerInput](capability.UpstreamServerAdd, func(ctx context.Context, input UpstreamServerInput) (any, error) {
			result, err := service.Create(ctx, input.Server)
			return result.Value, err
		})},
		{capability.UpstreamServerConfigure, typedOperation[UpstreamUpdateInput](capability.UpstreamServerConfigure, func(ctx context.Context, input UpstreamUpdateInput) (any, error) {
			result, err := service.Update(ctx, input.ID, input.Server)
			return result.Value, err
		})},
		{capability.UpstreamServerRemove, typedOperation[UpstreamIDInput](capability.UpstreamServerRemove, func(ctx context.Context, input UpstreamIDInput) (any, error) {
			result, err := service.Remove(ctx, input.ID)
			return result.Value, err
		})},
		{capability.UpstreamServerEnable, typedOperation[UpstreamIDInput](capability.UpstreamServerEnable, func(ctx context.Context, input UpstreamIDInput) (any, error) {
			result, err := service.Enable(ctx, input.ID)
			return result.Value, err
		})},
		{capability.UpstreamServerDisable, typedOperation[UpstreamIDInput](capability.UpstreamServerDisable, func(ctx context.Context, input UpstreamIDInput) (any, error) {
			result, err := service.Disable(ctx, input.ID)
			return result.Value, err
		})},
		{capability.UpstreamServerStatus, typedOperation[UpstreamStatusInput](capability.UpstreamServerStatus, func(ctx context.Context, input UpstreamStatusInput) (any, error) {
			result, err := service.Status(ctx, input.ID, input.Refresh)
			return result.Value, err
		})},
		{capability.UpstreamServerTools, typedOperation[UpstreamStatusInput](capability.UpstreamServerTools, func(ctx context.Context, input UpstreamStatusInput) (any, error) {
			result, err := service.Tools(ctx, input.ID, input.Refresh)
			return result.Value, err
		})},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
