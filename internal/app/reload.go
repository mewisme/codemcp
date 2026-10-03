package app

import (
	"errors"
	"slices"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/tunnel"
)

// reloadTestAfterCommit runs after Config.Update and before runtime apply. Tests only.
var reloadTestAfterCommit func()

func (a *App) ReloadConfig(next config.Config) error {
	if a == nil || a.Config == nil || a.Tools == nil {
		return errors.New("runtime is unavailable")
	}
	if err := config.Validate(next); err != nil {
		return err
	}
	previous := a.Config.Snapshot()
	typeSafeCandidate, err := a.prepareTypeSafe(next)
	if err != nil {
		return err
	}
	httpChanged := previous.HTTP.MCP.Enabled != next.HTTP.MCP.Enabled
	agentChanged := previous.Agent != next.Agent
	integrationsChanged := previous.Integrations != next.Integrations
	permissionsChanged := !slices.Equal(previous.Permissions.AllowDirs, next.Permissions.AllowDirs)
	shellPathChanged := !slices.Equal(previous.Shell.Path, next.Shell.Path)
	semanticApprovalChanged := previous.Approval.Semantic != next.Approval.Semantic
	telemetryChanged := previous.Telemetry != next.Telemetry
	telegramChanged := previous.Telegram.Enabled != next.Telegram.Enabled ||
		previous.Telegram.TopicsEnabled != next.Telegram.TopicsEnabled ||
		previous.Telegram.LogsMiniApp != next.Telegram.LogsMiniApp ||
		!slices.Equal(previous.Telegram.AllowedUserIDs, next.Telegram.AllowedUserIDs)
	tunnelChanged := previous.Tunnel != next.Tunnel

	if _, err := a.Config.Update(func(config.Config) (config.Config, error) { return next, nil }); err != nil {
		return err
	}
	if reloadTestAfterCommit != nil {
		reloadTestAfterCommit()
	}
	if err := a.applyRuntimeConfig(next, typeSafeCandidate, httpChanged, agentChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged); err != nil {
		_, restoreErr := a.Config.Update(func(config.Config) (config.Config, error) { return previous, nil })
		return errors.Join(err, restoreErr, a.rollbackRuntimeConfig(previous, httpChanged, agentChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged))
	}
	return nil
}

func (a *App) applyRuntimeConfig(next config.Config, typeSafeCandidate typeSafeRuntimeCandidate, httpChanged, agentChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged bool) error {
	if agentChanged {
		if err := application.ConfigureManagedAgentRuntime(a.Tools.Agents, next); err != nil {
			return err
		}
	}
	if integrationsChanged {
		if err := a.Tools.SyncIntegrations(next.Integrations); err != nil {
			return err
		}
		if a.chatGPTWeb != nil {
			if err := a.chatGPTWeb.ReconcileRuntimeConfig(a.runtimeCtx); err != nil {
				return err
			}
		}
	}
	if permissionsChanged {
		a.Tools.SetGlobalAllowDirs(next.Permissions.AllowDirs)
	}
	if shellPathChanged {
		a.Tools.SetShellPath(next.Shell.Path)
	}
	if semanticApprovalChanged {
		a.Tools.SetSemanticApprovalPolicy(semanticApprovalPolicy(next.Approval.Semantic))
	}
	if telemetryChanged && a.ProductTelemetry != nil {
		a.ProductTelemetry.SetEnabled(config.ResolveTelemetryEnabled(next, true).Enabled)
	}
	if a.running && a.Telegram != nil {
		if err := a.Telegram.Reconcile(a.runtimeCtx, next.Telegram); err != nil {
			return err
		}
	}
	if httpChanged {
		a.syncMCPHTTP(next.HTTP.MCP.Enabled)
	}
	if tunnelChanged && a.Tunnel != nil {
		if err := a.Tunnel.Reconcile(next.Tunnel, cachedTunnelMetadata(next.Tunnel), a.running); err != nil {
			return err
		}
	}
	return a.commitTypeSafe(typeSafeCandidate)
}

func (a *App) rollbackRuntimeConfig(previous config.Config, httpChanged, agentChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged bool) error {
	var rollbackErr error
	if tunnelChanged && a.Tunnel != nil {
		rollbackErr = errors.Join(rollbackErr, a.Tunnel.Reconcile(previous.Tunnel, cachedTunnelMetadata(previous.Tunnel), a.running))
	}
	if integrationsChanged {
		rollbackErr = errors.Join(rollbackErr, a.Tools.SyncIntegrations(previous.Integrations))
		if a.chatGPTWeb != nil {
			rollbackErr = errors.Join(rollbackErr, a.chatGPTWeb.ReconcileRuntimeConfig(a.runtimeCtx))
		}
	}
	if agentChanged {
		rollbackErr = errors.Join(rollbackErr, application.ConfigureManagedAgentRuntime(a.Tools.Agents, previous))
	}
	if permissionsChanged {
		a.Tools.SetGlobalAllowDirs(previous.Permissions.AllowDirs)
	}
	if shellPathChanged {
		a.Tools.SetShellPath(previous.Shell.Path)
	}
	if semanticApprovalChanged {
		a.Tools.SetSemanticApprovalPolicy(semanticApprovalPolicy(previous.Approval.Semantic))
	}
	if telemetryChanged && a.ProductTelemetry != nil {
		a.ProductTelemetry.SetEnabled(config.ResolveTelemetryEnabled(previous, true).Enabled)
	}
	if telegramChanged && a.running && a.Telegram != nil {
		rollbackErr = errors.Join(rollbackErr, a.Telegram.Reconcile(a.runtimeCtx, previous.Telegram))
	}
	if httpChanged {
		a.syncMCPHTTP(previous.HTTP.MCP.Enabled)
	}
	return rollbackErr
}

func cachedTunnelMetadata(cfg tunnel.Config) *tunnel.Metadata {
	if cfg.ID == "" {
		return nil
	}
	metadata, err := config.LoadTunnelMetadata(cfg.ID)
	if err != nil {
		return nil
	}
	return &metadata
}

func semanticApprovalPolicy(value config.SemanticApprovalConfig) tools.SemanticApprovalPolicy {
	return tools.SemanticApprovalPolicy{
		Enabled:           value.Enabled,
		Provider:          value.Provider,
		Timeout:           time.Duration(value.TimeoutMS) * time.Millisecond,
		MinimumConfidence: value.MinimumConfidence,
		FailMode:          tools.SemanticApprovalAction(value.FailMode),
		Actions: map[semantic.RiskClass]tools.SemanticApprovalAction{
			semantic.RiskLow:      tools.SemanticApprovalAction(value.LowAction),
			semantic.RiskMedium:   tools.SemanticApprovalAction(value.MediumAction),
			semantic.RiskHigh:     tools.SemanticApprovalAction(value.HighAction),
			semantic.RiskCritical: tools.SemanticApprovalAction(value.CriticalAction),
		},
	}
}
