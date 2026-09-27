package app

import (
	"errors"
	"slices"
	"time"

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
	httpChanged := previous.Server.Enabled != next.Server.Enabled
	integrationsChanged := previous.Integrations != next.Integrations
	permissionsChanged := !slices.Equal(previous.Permissions.AllowDirs, next.Permissions.AllowDirs)
	shellPathChanged := !slices.Equal(previous.Shell.Path, next.Shell.Path)
	semanticApprovalChanged := previous.Approval.Semantic != next.Approval.Semantic
	telemetryChanged := previous.Telemetry != next.Telemetry
	telegramChanged := previous.Telegram.Enabled != next.Telegram.Enabled || !slices.Equal(previous.Telegram.AllowedUserIDs, next.Telegram.AllowedUserIDs)
	tunnelChanged := previous.Tunnel != next.Tunnel
	tunnelRuntimeChanged := tunnelChanged && !tunnel.RuntimeConfigEqual(previous.Tunnel, next.Tunnel)

	if _, err := a.Config.Update(func(config.Config) (config.Config, error) { return next, nil }); err != nil {
		return err
	}
	if reloadTestAfterCommit != nil {
		reloadTestAfterCommit()
	}
	if err := a.applyRuntimeConfig(next, httpChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged, tunnelRuntimeChanged); err != nil {
		_, restoreErr := a.Config.Update(func(config.Config) (config.Config, error) { return previous, nil })
		return errors.Join(err, restoreErr, a.rollbackRuntimeConfig(previous, httpChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged, tunnelRuntimeChanged))
	}
	return nil
}

func (a *App) applyRuntimeConfig(next config.Config, httpChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged, tunnelRuntimeChanged bool) error {
	if integrationsChanged {
		if err := a.Tools.SyncIntegrations(next.Integrations); err != nil {
			return err
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
	if telegramChanged && a.running && a.Telegram != nil {
		if err := a.Telegram.Reconcile(a.runtimeCtx, next.Telegram); err != nil {
			return err
		}
	}
	if httpChanged {
		a.syncMCPHTTP(next.Server.Enabled)
	}
	if tunnelChanged && a.Tunnel != nil {
		var err error
		if tunnelRuntimeChanged {
			if a.running {
				err = a.Tunnel.Reconfigure(next.Tunnel, func() error { return nil })
			} else {
				err = a.Tunnel.Configure(next.Tunnel)
			}
		} else {
			err = a.Tunnel.SyncManagementConfig(next.Tunnel)
		}
		if err != nil {
			return err
		}
		if tunnelRuntimeChanged {
			if metadata, loadErr := config.LoadTunnelMetadata(next.Tunnel.ID); loadErr == nil {
				_ = a.Tunnel.SeedMetadata(metadata)
			}
		}
	}
	return nil
}

func (a *App) rollbackRuntimeConfig(previous config.Config, httpChanged, integrationsChanged, permissionsChanged, shellPathChanged, semanticApprovalChanged, telemetryChanged, telegramChanged, tunnelChanged, tunnelRuntimeChanged bool) error {
	var rollbackErr error
	if tunnelChanged && a.Tunnel != nil {
		if tunnelRuntimeChanged {
			if a.running {
				rollbackErr = errors.Join(rollbackErr, a.Tunnel.Reconfigure(previous.Tunnel, func() error { return nil }))
			} else {
				rollbackErr = errors.Join(rollbackErr, a.Tunnel.Configure(previous.Tunnel))
			}
		} else {
			rollbackErr = errors.Join(rollbackErr, a.Tunnel.SyncManagementConfig(previous.Tunnel))
		}
	}
	if integrationsChanged {
		rollbackErr = errors.Join(rollbackErr, a.Tools.SyncIntegrations(previous.Integrations))
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
		a.syncMCPHTTP(previous.Server.Enabled)
	}
	return rollbackErr
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
