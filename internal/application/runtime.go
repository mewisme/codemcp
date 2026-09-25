package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	managed "go.mewis.me/codemcp/internal/service"
)

var ErrNotInitialized = errors.New("cm is not initialized; run cm init first")

const managedReadyTimeout = managed.DefaultLifecycleTimeout

type ExternalCommand struct {
	Command string
	Reason  string
}

type RuntimeOverview struct {
	Running        bool
	Status         runtimecontrol.RuntimeStatus
	MCPHTTPEnabled bool
	MCPHTTPPort    int
	TunnelEnabled  bool
	UserService    ServiceOverview
	SystemService  ServiceOverview
}

type ServiceOverview struct {
	Scope      managed.Scope
	Supported  bool
	ID         string
	Backend    string
	Installed  bool
	Running    bool
	PID        int
	ConfigRoot string
	Warning    string
	Err        string
}

type RuntimeActionResult struct {
	Action   string
	Scope    managed.Scope
	Changed  bool
	Status   runtimecontrol.RuntimeStatus
	Service  ServiceOverview
	External *ExternalCommand
}

var newServiceManager = managed.NewManager
var detectServiceScope = managed.DetectScope

func RuntimeStatus(ctx context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
	var result runtimecontrol.RuntimeStatus
	state, err := runtimecontrol.Request(ctx, http.MethodGet, "/status", nil, &result)
	if err != nil {
		if runtimecontrol.IsUnavailable(err) {
			return runtimecontrol.RuntimeStatus{}, false, nil
		}
		return runtimecontrol.RuntimeStatus{}, false, err
	}
	if err := runtimecontrol.ValidatePID(ctx, state.PID, result.PID, "status"); err != nil {
		return runtimecontrol.RuntimeStatus{}, false, err
	}
	return result, true, nil
}

func LoadRuntimeOverview(ctx context.Context) (RuntimeOverview, error) {
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	status, running, err := RuntimeStatus(statusCtx)
	cancel()
	if err != nil {
		return RuntimeOverview{}, err
	}
	user := loadServiceOverview(managed.ScopeUser)
	system := ServiceOverview{Scope: managed.ScopeSystem, Supported: runtime.GOOS != "windows"}
	if system.Supported {
		system = loadServiceOverview(managed.ScopeSystem)
	}
	cfg, err := config.Load()
	if err != nil {
		return RuntimeOverview{}, err
	}
	return RuntimeOverview{Running: running, Status: status, MCPHTTPEnabled: cfg.Server.Enabled, MCPHTTPPort: cfg.Server.Port, TunnelEnabled: cfg.Tunnel.Enabled, UserService: user, SystemService: system}, nil
}

func loadServiceOverview(scope managed.Scope) ServiceOverview {
	overview := ServiceOverview{Scope: scope, Supported: scope == managed.ScopeUser || runtime.GOOS != "windows"}
	if !overview.Supported {
		return overview
	}
	spec, manager, err := managedService(scope, "")
	if err != nil {
		overview.Err = err.Error()
		return overview
	}
	status, err := manager.Status(spec)
	if err != nil {
		overview.Err = err.Error()
		return overview
	}
	overview.ID, overview.Backend, overview.Installed, overview.Running, overview.PID, overview.ConfigRoot = spec.ID, managedBackend(manager, spec), status.Installed, status.Running, status.PID, spec.ConfigRoot
	overview.Warning = managed.PersistenceWarning(spec)
	return overview
}

func ManagedRuntimeAction(ctx context.Context, action string, scope managed.Scope) (RuntimeActionResult, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "up" && action != "down" && action != "restart" {
		return RuntimeActionResult{}, fmt.Errorf("unsupported runtime action: %s", action)
	}
	if scope == "" {
		scope = detectServiceScope()
	}
	if scope != managed.ScopeUser && scope != managed.ScopeSystem {
		return RuntimeActionResult{}, fmt.Errorf("unsupported service scope: %s", scope)
	}
	if scope == managed.ScopeSystem && runtime.GOOS == "windows" {
		return RuntimeActionResult{}, errors.New("system service scope is not supported on Windows; managed services use a per-user Scheduled Task")
	}
	if scope == managed.ScopeSystem && detectServiceScope() == managed.ScopeUser {
		command := "cm --config-dir " + strconv.Quote(config.RootPath()) + " " + action + " --system"
		return RuntimeActionResult{Action: action, Scope: scope, External: &ExternalCommand{Command: command, Reason: "System service changes require elevation outside the TUI."}}, nil
	}
	spec, manager, err := managedService(scope, "")
	if err != nil {
		return RuntimeActionResult{}, err
	}
	spec, err = prepareManagedActionSpec(spec, action)
	if err != nil {
		return RuntimeActionResult{}, err
	}
	switch action {
	case "up":
		return managedUp(ctx, spec, manager)
	case "down":
		return managedDown(ctx, spec, manager)
	default:
		return managedRestart(ctx, spec, manager)
	}
}

func prepareManagedActionSpec(spec managed.Spec, action string) (managed.Spec, error) {
	if action == "down" {
		return spec, nil
	}
	binary, err := managed.PrepareManagedBinary(spec.ConfigRoot, spec.Binary)
	if err != nil {
		return spec, err
	}
	spec.Binary = binary
	return spec, nil
}

func managedService(scope managed.Scope, binary string) (managed.Spec, managed.Manager, error) {
	account, err := managed.InvokingAccount(scope)
	if err != nil {
		return managed.Spec{}, nil, err
	}
	if strings.TrimSpace(binary) == "" {
		binary = os.Args[0]
	}
	spec, err := managed.NewSpec(config.RootPath(), binary, scope, account)
	if err != nil {
		return managed.Spec{}, nil, err
	}
	return spec, newServiceManager(), nil
}

func prepareManagedSpec(spec managed.Spec) (managed.Spec, error) {
	source, err := config.Source()
	if err != nil {
		return spec, err
	}
	if !source.Exists {
		return spec, ErrNotInitialized
	}
	if _, err := config.VerifyRuntime(); err != nil {
		return spec, err
	}
	cfg, err := config.Load()
	if err != nil {
		return spec, err
	}
	hash, err := managed.SaveEnvironment(spec.ConfigRoot, managed.CaptureEnvironment(spec.Account, cfg.Shell.Path))
	if err != nil {
		return spec, err
	}
	spec.EnvironmentHash = hash
	return spec, nil
}

func managedUp(ctx context.Context, spec managed.Spec, manager managed.Manager) (RuntimeActionResult, error) {
	var err error
	spec, err = prepareManagedSpec(spec)
	if err != nil {
		return RuntimeActionResult{}, err
	}
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: runtimeStatusFast, Shutdown: requestRuntimeShutdown, Timeout: managed.DefaultLifecycleTimeout}
	result, err := lifecycle.Up(ctx)
	if err != nil {
		return RuntimeActionResult{}, err
	}
	return runtimeActionResult("up", spec, manager, result.Status, result.Changed), nil
}

func managedDown(ctx context.Context, spec managed.Spec, manager managed.Manager) (RuntimeActionResult, error) {
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: runtimeStatusFast, Shutdown: requestRuntimeShutdown, Timeout: managed.DefaultLifecycleTimeout}
	result, err := lifecycle.Down(ctx)
	if err != nil {
		return RuntimeActionResult{}, err
	}
	return runtimeActionResult("down", spec, manager, runtimecontrol.RuntimeStatus{}, result.Changed), nil
}

func managedRestart(ctx context.Context, spec managed.Spec, manager managed.Manager) (RuntimeActionResult, error) {
	var err error
	spec, err = prepareManagedSpec(spec)
	if err != nil {
		return RuntimeActionResult{}, err
	}
	lifecycle := managed.Lifecycle{Manager: manager, Spec: spec, Probe: runtimeStatusFast, Shutdown: requestRuntimeShutdown, Timeout: managed.DefaultLifecycleTimeout}
	result, err := lifecycle.Restart(ctx)
	if err != nil {
		return RuntimeActionResult{}, err
	}
	return runtimeActionResult("restart", spec, manager, result.Status, result.Changed), nil
}

func runtimeStatusFast(ctx context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return RuntimeStatus(statusCtx)
}

func requestRuntimeShutdown(ctx context.Context) error {
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := runtimecontrol.Request(requestCtx, http.MethodPost, "/shutdown", nil, &map[string]bool{})
	return err
}

func stopManagedBackend(spec managed.Spec, manager managed.Manager) error {
	return managed.StopBackend(manager, spec)
}

func waitRuntimeStopped(ctx context.Context, timeout time.Duration) error {
	return managed.WaitRuntimeStopped(ctx, runtimeStatusFast, timeout)
}

func waitManagedReady(ctx context.Context, spec managed.Spec, previousRunID string, timeout time.Duration) (runtimecontrol.RuntimeStatus, error) {
	return managed.WaitRuntimeReady(ctx, spec, runtimeStatusFast, previousRunID, timeout)
}

func runtimeActionResult(action string, spec managed.Spec, manager managed.Manager, status runtimecontrol.RuntimeStatus, changed bool) RuntimeActionResult {
	serviceStatus, err := manager.Status(spec)
	overview := ServiceOverview{Scope: spec.Scope, Supported: true, ID: spec.ID, Backend: managedBackend(manager, spec), ConfigRoot: spec.ConfigRoot, Warning: managed.PersistenceWarning(spec)}
	if err != nil {
		overview.Err = err.Error()
	} else {
		overview.Installed, overview.Running, overview.PID = serviceStatus.Installed, serviceStatus.Running, serviceStatus.PID
	}
	return RuntimeActionResult{Action: action, Scope: spec.Scope, Changed: changed, Status: status, Service: overview}
}

func managedBackend(manager managed.Manager, spec managed.Spec) string {
	if runtime.GOOS == "linux" && spec.Scope == managed.ScopeUser {
		return "systemd --user"
	}
	return manager.Backend()
}
