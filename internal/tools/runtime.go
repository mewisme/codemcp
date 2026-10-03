package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/backgrounddelivery"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/controlguard"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/integrations"
	"go.mewis.me/codemcp/internal/integrations/caveman"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/integrations/ponytail"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
	plandoc "go.mewis.me/codemcp/internal/plan"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

const (
	tunnelToolBudget         = 100 * time.Second
	tunnelResponseReserveMax = 5 * time.Second
)

var errTunnelResponseBudgetExceeded = errors.New("tunnel response budget exhausted")

type Runtime struct {
	Registry             *Registry
	Workspaces           *workspace.Manager
	Checkpoints          *checkpoint.Store
	Upstream             *upstream.Manager
	CallObserver         CallObserver
	SessionAccess        *SessionWorkspaceAccessManager
	Approvals            *approval.Manager
	Completions          *agentcompletion.Service
	CompletionHooks      *agentcompletion.CompletionHookBus
	CodeGraphCompletion  *codegraph.CompletionHook
	Executions           *shellruntime.ExecutionHub
	Shell                *shellruntime.Manager
	Processes            *shellruntime.ProcessManager
	BackgroundDeliveries *backgrounddelivery.Broker
	LoopGuard            *ToolLoopGuard
	InstructionChanges   *instructioncontext.ChangeStream
	PlanExecutions       *plandoc.ExecutionManager
	Semantic             *semantic.Manager
	semanticApproval     semanticApprovalSlot
	protocolFeatures     protocolFeatureSlot
	sessionMu            sync.Mutex
	configReadMu         sync.RWMutex
	configReads          ConfigReadProvider
	configApprovalMu     sync.RWMutex
	configApprovals      ConfigSetApprovalProvider
	configApplyMu        sync.RWMutex
	configApplies        ConfigSetApplyProvider
	instructionAuthoring instructionAuthoringSlot
	planAuthoring        planAuthoringSlot
	prompts              promptProviderSlot
	integrationMu        sync.Mutex
	integrations         integrations.Config
	ponytailManager      *ponytail.Manager
	cavemanManager       *caveman.Manager
	codegraphRuntime     *codegraph.Runtime
}

func NewRuntime() *Runtime {
	return NewRuntimeWithIntegrations(integrations.Default())
}

func NewRuntimeWithIntegrations(integrationConfig integrations.Config) *Runtime {
	return NewRuntimeWithAccess(integrationConfig, nil)
}

func NewRuntimeWithAccess(integrationConfig integrations.Config, globalAllowDirs []string, environments ...ProjectContextEnvironment) *Runtime {
	workspaces := workspace.NewManagerWithGlobalAllowDirs(workspace.DefaultStorePath(), globalAllowDirs)
	checkpoints := checkpoint.NewWorkspaceStore(checkpoint.DefaultRoot(), workspaces)
	upstreams := upstream.NewManager(upstream.NewStore(upstream.Path()))
	_ = upstreams.Load()
	registry := NewRegistry()
	identity, err := workspaces.Instance()
	if err != nil {
		panic(err)
	}
	stateRoot := shellruntime.DefaultStateRoot()
	executions, err := shellruntime.NewPersistentExecutionHub(filepath.Join(stateRoot, "executions.json"))
	if err != nil {
		panic(err)
	}
	shell := shellruntime.NewManagerWithExecutions(workspaces, shellruntime.DefaultStateRoot(), executions)
	processes := shellruntime.NewProcessManagerWithExecutions(workspaces, shell, executions)
	backgroundDeliveries, err := backgrounddelivery.NewPersistent(processes, filepath.Join(stateRoot, "background-deliveries.json"))
	if err != nil {
		panic(err)
	}
	completionHooks := agentcompletion.NewCompletionHookBus(agentcompletion.HookBusOptions{Timeout: codegraph.SyncTimeout + 5*time.Second})
	completions, err := agentcompletion.NewWorkspaceService(workspaces, agentcompletion.Options{Hooks: completionHooks})
	if err != nil {
		panic(err)
	}
	runtime := &Runtime{Registry: registry, Workspaces: workspaces, Checkpoints: checkpoints, Upstream: upstreams, SessionAccess: NewSessionWorkspaceAccessManager(), Approvals: approval.NewManager(identity.ID), Completions: completions, CompletionHooks: completionHooks, Executions: executions, Shell: shell, Processes: processes, BackgroundDeliveries: backgroundDeliveries, LoopGuard: NewToolLoopGuard(), InstructionChanges: instructioncontext.NewChangeStream(), PlanExecutions: plandoc.NewExecutionManager(), Semantic: semantic.NewManager(semantic.ManagerOptions{}), ponytailManager: ponytail.NewManager(integrationConfig.Ponytail.Active, ponytail.Mode(integrationConfig.Ponytail.Mode)), cavemanManager: caveman.NewManager(integrationConfig.Caveman.Active, caveman.Mode(integrationConfig.Caveman.Mode))}
	runtime.SetSemanticApprovalPolicy(DefaultSemanticApprovalPolicy())
	runtime.CodeGraphCompletion = codegraph.NewCompletionHook(func() *codegraph.Runtime {
		return runtime.codeGraphRuntimeSnapshot()
	}, workspaces)
	if err := completionHooks.Register(runtime.CodeGraphCompletion); err != nil {
		panic(err)
	}
	RegisterWorkspaceTools(registry, workspaces, shell)
	RegisterWorkspaceListTool(registry, runtime)
	RegisterWorkspaceContainerTools(registry, workspaces)
	var environment ProjectContextEnvironment
	if len(environments) > 0 {
		environment = environments[0]
	}
	registerCoreWithManagersAndBroker(registry, workspaces, checkpoints, environment, shell, processes, backgroundDeliveries, codeGraphProjectContextProviders(runtime))
	RegisterInstructionAuthoringTools(registry, runtime)
	RegisterPlanAuthoringTool(registry, runtime)
	RegisterPromptTools(registry, runtime)
	RegisterAgentCompletionTool(registry, runtime.Completions, runtime.PlanExecutions)
	RegisterApprovalTools(registry, runtime)
	RegisterConfigTools(registry, runtime)
	RegisterUpstreamTools(registry, upstreams)
	if err := runtime.SyncIntegrations(integrationConfig); err != nil {
		panic(err)
	}

	return runtime
}

func (r *Runtime) RefreshUpstreams(ctx context.Context, force bool) error {
	if r == nil || r.Registry == nil || r.Upstream == nil {
		return errors.New("tool runtime is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return RefreshUpstreamProxies(ctx, r.Registry, r.Upstream, force)
}

func (r *Runtime) SyncIntegrations(integrationConfig integrations.Config) (syncErr error) {
	if r == nil || r.Registry == nil || r.Workspaces == nil {
		return errors.New("tool runtime is unavailable")
	}
	r.integrationMu.Lock()
	defer func() {
		r.integrationMu.Unlock()
		if syncErr == nil && r.CodeGraphCompletion != nil && r.Completions != nil {
			ctx, cancel := context.WithTimeout(context.Background(), codegraph.SyncTimeout+5*time.Second)
			defer cancel()
			_ = r.CodeGraphCompletion.CatchUp(ctx, r.Completions)
		}
	}()
	if r.ponytailManager == nil {
		r.ponytailManager = ponytail.NewManager(integrationConfig.Ponytail.Active, ponytail.Mode(integrationConfig.Ponytail.Mode))
	}
	if r.cavemanManager == nil {
		r.cavemanManager = caveman.NewManager(integrationConfig.Caveman.Active, caveman.Mode(integrationConfig.Caveman.Mode))
	}
	r.codegraphRuntime = codegraph.New(codegraph.Options{
		Enabled:        integrationConfig.CodeGraph.Enabled,
		ConfiguredPath: integrationConfig.CodeGraph.Path,
	})
	if err := r.Registry.ReplaceOwned(integrations.Owner(integrations.PonytailID), ponytailToolEntries(r.Workspaces, r.ponytailManager)); err != nil {
		return err
	}
	if err := r.Registry.ReplaceOwned(integrations.Owner(integrations.CavemanID), cavemanToolEntries(r.Workspaces, r.cavemanManager)); err != nil {
		return err
	}
	if err := r.Registry.ReplaceOwned(integrations.Owner(integrations.CodeGraphID), codeGraphToolEntries(r)); err != nil {
		return err
	}
	r.ponytailManager.SetDefaults(integrationConfig.Ponytail.Active, ponytail.Mode(integrationConfig.Ponytail.Mode))
	r.cavemanManager.SetDefaults(integrationConfig.Caveman.Active, caveman.Mode(integrationConfig.Caveman.Mode))
	if r.Shell != nil {
		r.Shell.ConfigureRTK(integrationConfig.RTK.Enabled, integrationConfig.RTK.Path)
	}
	r.integrations = integrationConfig
	return nil
}

func (r *Runtime) Integrations() integrations.Config {
	if r == nil {
		return integrations.Config{}
	}
	r.integrationMu.Lock()
	defer r.integrationMu.Unlock()
	return r.integrations
}

func (r *Runtime) codeGraphRuntimeSnapshot() *codegraph.Runtime {
	if r == nil {
		return nil
	}
	r.integrationMu.Lock()
	defer r.integrationMu.Unlock()
	return r.codegraphRuntime
}

func (r *Runtime) SetGlobalAllowDirs(allowDirs []string) {
	if r != nil && r.Workspaces != nil {
		r.Workspaces.SetGlobalAllowDirs(allowDirs)
	}
}

func (r *Runtime) ReloadWorkspaces() error {
	if r == nil || r.Workspaces == nil {
		return errors.New("tool runtime is unavailable")
	}
	return r.Workspaces.Reload()
}

func (r *Runtime) SetShellPath(paths []string) {
	if r != nil && r.Workspaces != nil {
		r.Workspaces.SetShellPath(paths)
	}
}

func (r *Runtime) List() []Schema      { return r.Registry.ListSchemas() }
func (r *Runtime) ListTools() []Schema { return r.List() }

func (r *Runtime) Call(ctx context.Context, name string, args map[string]any) (Result, error) {
	callID := r.nextCallID()
	started := time.Now()
	source := CallSource(ctx)
	callCtx, cancelCall := toolCallContext(ctx, source, started)
	defer cancelCall()
	ctx = callCtx
	receivedBy := ReceivedByInstanceID(ctx)
	if receivedBy == "" {
		receivedBy = r.runtimeInstanceID()
	}
	workspaceID := ""
	sessionID := MCPSessionID(ctx)
	sessionHash := MCPSessionFingerprint(sessionID)
	approvalCorrelation := ApprovalCorrelationFromContext(ctx)
	sessionAccess := SessionWorkspaceAccessDecision("")
	sessionWorkspaceCount := 0
	var preflightErr error
	if r.Registry != nil {
		workspaceScoped, err := r.Registry.WorkspaceScoped(name)
		if err != nil {
			preflightErr = err
		} else if workspaceScoped {
			boundWorkspaceID := BoundWorkspace(ctx)
			if _, exists := args["workspace_id"]; !exists && boundWorkspaceID != "" {
				args = cloneMap(args)
				args["workspace_id"] = boundWorkspaceID
			}
			workspaceID, preflightErr = requiredString(args, "workspace_id")
			if preflightErr == nil && name == ApprovalRequestToolName {
				// Approval requests may target the synthetic local-control scope used by global control-plane tools.
			} else if preflightErr == nil {
				var resolution WorkspaceAccessResolution
				resolution, preflightErr = r.ResolveWorkspaceAccess(ctx, workspaceID)
				if preflightErr == nil {
					if resolution.WorkspaceID != workspaceID {
						args = cloneMap(args)
						args["workspace_id"] = resolution.WorkspaceID
					}
					workspaceID = resolution.WorkspaceID
					sessionAccess = resolution.SessionAccess
					sessionWorkspaceCount = resolution.SessionWorkspaceCount
				}
			}
		} else if name == "workspace_register" {
			workspaceID = approvalControlWorkspace
		}
	}
	claimedApproval := approval.Request{}
	var forcedResult *Result
	approvalArgs := args
	if preflightErr == nil && r.Registry != nil && name == mcpconfigwire.SetToolName {
		if _, registered := r.Registry.Schema(name); registered {
			bound, code := r.bindConfigSetApproval(ctx, args)
			if code != "" {
				blocked := configSetApprovalError(code)
				forcedResult = &blocked
			} else {
				approvalArgs = bound
			}
		}
	}
	if preflightErr == nil && forcedResult == nil {
		ctx, claimedApproval, forcedResult, preflightErr = r.prepareApprovalRetry(ctx, approvalCorrelation, workspaceID, source, name, approvalArgs)
	}
	if preflightErr == nil && forcedResult == nil {
		preflightErr = r.semanticApprovalPreflight(ctx, approvalCorrelation, workspaceID, name, approvalArgs, claimedApproval.ID != "")
	}
	loopClass, loopDecision := toolLoopClassMutation, toolLoopDecision{}
	if preflightErr == nil && forcedResult == nil && strings.TrimSpace(sessionID) != "" && r.Registry != nil {
		if schema, ok := r.Registry.Schema(name); ok {
			loopClass = toolLoopClassFor(name, schema, args)
			loopDecision = r.loopGuard().Check(sessionID, name, args, loopClass)
			if loopDecision.blocked {
				blocked := toolLoopBlockedResult(name, loopDecision)
				forcedResult = &blocked
			}
		}
	}
	executedBy := r.runtimeInstanceID()
	ctx = WithAgentCompletionCorrelation(ctx, approvalCorrelation.CallerID, executedBy, source)
	ctx = shellruntime.WithExecutionMetadata(ctx, shellruntime.ExecutionMetadata{
		Source: source, CallID: callID, SessionHash: sessionHash, ReceivedByInstanceID: receivedBy, ExecutedByInstanceID: executedBy,
	})
	raw := callRaw(ctx, source, name, args)
	raw["call_id"] = callID
	if sessionHash != "" {
		raw["session"] = map[string]any{"hash": sessionHash, "access": sessionAccess, "workspace_count": sessionWorkspaceCount}
	}
	r.observeCall(CallObservation{CallID: callID, Phase: "start", Source: source, Tool: name, WorkspaceID: workspaceID, Raw: raw, SessionHash: sessionHash, SessionAccess: sessionAccess, SessionWorkspaceCount: sessionWorkspaceCount, ReceivedByInstanceID: receivedBy})

	result, err := Result{}, preflightErr
	if err == nil && forcedResult != nil {
		result = *forcedResult
	} else if err == nil {
		result, err = r.Registry.Call(ctx, name, args)
	}
	if err == nil {
		result = limitToolResult(result)
	}
	if err != nil && errors.Is(context.Cause(ctx), errTunnelResponseBudgetExceeded) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		err = tunnelResponseBudgetError(name)
	}
	if err != nil {
		if guard, ok := controlguard.As(err); ok {
			if guardedResult, handled, guardErr := r.approvalResultForGuard(guard, approvalCorrelation, sessionHash, workspaceID, source, name, approvalArgs, claimedApproval); guardErr != nil {
				err = guardErr
			} else if handled {
				result, err = guardedResult, nil
			}
		}
	}
	finishRaw := cloneMap(raw)
	finishRaw["routing"] = map[string]any{"received_by_instance_id": receivedBy, "executed_by_instance_id": executedBy}
	if err == nil {
		if loopClass == toolLoopClassMutation && !result.IsError && forcedResult == nil && strings.TrimSpace(sessionID) != "" {
			r.loopGuard().MarkMutationSuccess(sessionID, name, args)
		}
		result = addToolLoopWarning(result, name, loopDecision)
		if result.ResultType == "" {
			result.ResultType = "complete"
		}
		status, message := "ok", ""
		if result.IsError {
			status = "error"
			if len(result.Content) > 0 {
				message = result.Content[0].Text
			}
		}
		observedMessage := observableToolMessage(name, status, message)
		finishRaw["status"] = status
		finishRaw["result_type"] = result.ResultType
		finishRaw["result"] = observedResult(name, result)
		r.observeCall(CallObservation{CallID: callID, Phase: "finish", Source: source, Tool: name, WorkspaceID: workspaceID, Status: status, DurationMS: time.Since(started).Milliseconds(), Message: observedMessage, ResultType: result.ResultType, Raw: finishRaw, SessionHash: sessionHash, SessionAccess: sessionAccess, SessionWorkspaceCount: sessionWorkspaceCount, ReceivedByInstanceID: receivedBy, ExecutedByInstanceID: executedBy})
		return result, nil
	}

	status, message := "error", err.Error()
	if errors.Is(context.Cause(ctx), errTunnelResponseBudgetExceeded) {
		message = err.Error()
	} else if ctx != nil && ctx.Err() != nil {
		status, message = "cancelled", ctx.Err().Error()
	}
	finishRaw["status"] = status
	finishRaw["error"] = message
	if errors.Is(err, ErrToolNotFound) {
		r.observeCall(CallObservation{CallID: callID, Phase: "finish", Source: source, Tool: name, WorkspaceID: workspaceID, Status: status, DurationMS: time.Since(started).Milliseconds(), Message: message, Raw: finishRaw, SessionHash: sessionHash, SessionAccess: sessionAccess, SessionWorkspaceCount: sessionWorkspaceCount, ReceivedByInstanceID: receivedBy, ExecutedByInstanceID: executedBy})
		return Result{}, err
	}
	result = ErrorResult(err)
	finishRaw["result_type"] = result.ResultType
	finishRaw["result"] = observedResult(name, result)
	r.observeCall(CallObservation{CallID: callID, Phase: "finish", Source: source, Tool: name, WorkspaceID: workspaceID, Status: status, DurationMS: time.Since(started).Milliseconds(), Message: message, ResultType: result.ResultType, Raw: finishRaw, SessionHash: sessionHash, SessionAccess: sessionAccess, SessionWorkspaceCount: sessionWorkspaceCount, ReceivedByInstanceID: receivedBy, ExecutedByInstanceID: executedBy})
	return result, nil
}

func (r *Runtime) loopGuard() *ToolLoopGuard {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.LoopGuard == nil {
		r.LoopGuard = NewToolLoopGuard()
	}
	return r.LoopGuard
}

func workspaceScopePreflightError(manager *workspace.Manager, id string, original error) error {
	id = strings.TrimSpace(id)
	if manager == nil || !strings.HasPrefix(id, "wsc_") {
		return original
	}
	if _, err := manager.GetContainer(id); err == nil {
		return fmt.Errorf("%s is a workspace container, not a workspace. Call workspace_container_context, then use one member ws_* workspace_id for this tool", id)
	} else if errors.Is(err, workspace.ErrContainerNotFound) {
		return fmt.Errorf("%w: %s", workspace.ErrContainerNotFound, id)
	} else {
		return err
	}
}

func toolCallContext(parent context.Context, source string, now time.Time) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if source != "tunnel" {
		return parent, func() {}
	}
	budgetDeadline := now.Add(tunnelToolBudget)
	if deadline, ok := parent.Deadline(); ok {
		remaining := deadline.Sub(now)
		if remaining <= 0 {
			return context.WithDeadlineCause(parent, deadline, errTunnelResponseBudgetExceeded)
		}
		reserve := remaining / 4
		if reserve > tunnelResponseReserveMax {
			reserve = tunnelResponseReserveMax
		}
		if reserve > 0 {
			reservedDeadline := deadline.Add(-reserve)
			if reservedDeadline.Before(budgetDeadline) {
				budgetDeadline = reservedDeadline
			}
		}
	}
	return context.WithDeadlineCause(parent, budgetDeadline, errTunnelResponseBudgetExceeded)
}

func tunnelResponseBudgetError(name string) error {
	if name == "run_command" {
		return errors.New("run_command exceeded the synchronous tunnel response budget; use start_process for long-running commands and rely on lifecycle-driven completion when available; process_status and process_output are for explicit inspection or recovery, not polling")
	}
	return fmt.Errorf("%s exceeded the synchronous tunnel response budget; split the operation into shorter tool calls", name)
}

func (r *Runtime) nextCallID() string {
	return idgen.Must("call", 8)
}

func observedResult(name string, result Result) any {
	if name != "run_command" {
		return result
	}
	value, ok := result.StructuredContent.(shellruntime.ExecResult)
	if !ok {
		return result
	}
	return map[string]any{
		"result_type":        result.ResultType,
		"is_error":           result.IsError,
		"structured_content": value,
	}
}

func (r *Runtime) sessionAccessManager() *SessionWorkspaceAccessManager {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.SessionAccess == nil {
		r.SessionAccess = NewSessionWorkspaceAccessManager()
	}
	return r.SessionAccess
}

func (r *Runtime) runtimeInstanceID() string {
	if r == nil || r.Workspaces == nil {
		return ""
	}
	identity, err := r.Workspaces.Instance()
	if err != nil {
		return ""
	}
	return identity.ID
}
