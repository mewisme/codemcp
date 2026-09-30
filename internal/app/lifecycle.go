package app

import (
	"context"
	"errors"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func (a *App) Start(ctx context.Context) error {
	started := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	if tracepkg.ObserverFromContext(ctx) == nil && a.trace != nil {
		ctx = tracepkg.WithObserver(ctx, a.trace)
	}
	span := tracepkg.Start(ctx, "APP", "app.runtime.start", "Starting application runtime")
	if err := a.Bootstrap(); err != nil {
		a.recordRuntimeUsage(ctx, producttelemetry.EventRuntimeStarted, started, err)
		span.FailMessage("Application runtime bootstrap failed", err)
		return err
	}
	if a.Tools != nil && a.Tools.Workspaces != nil {
		if err := a.Tools.Workspaces.Activate(); err != nil {
			a.recordRuntimeUsage(ctx, producttelemetry.EventRuntimeStarted, started, err)
			span.FailMessage("Workspace runtime ownership activation failed", err)
			return err
		}
	}
	a.runtimeCtx = ctx
	if a.ApprovalExplain != nil {
		if err := a.ApprovalExplain.Start(ctx); err != nil {
			a.recordRuntimeUsage(ctx, producttelemetry.EventRuntimeStarted, started, err)
			span.FailMessage("Approval explanation runtime could not start", err)
			return err
		}
	}
	if a.Telegram != nil {
		a.Telegram.Reconcile(ctx, a.Config.Snapshot().Telegram)
		if a.TelegramUI != nil {
			go a.TelegramUI.ReconcilePendingRuntimeOperations(ctx)
		}
	}
	if a.ApprovalNotifications != nil {
		if err := a.ApprovalNotifications.Start(ctx); err != nil && a.Logger != nil {
			a.Logger.Warning("NOTIFICATION", "notification.coordinator.start.failed", "Approval notification coordinator could not start", err)
		}
	}
	if a.BackgroundNotifications != nil {
		if err := a.BackgroundNotifications.Start(ctx); err != nil && a.Logger != nil {
			a.Logger.Warning("NOTIFICATION", "notification.background.start.failed", "Background job notification bridge could not start", err)
		}
	}
	if a.Tools != nil {
		go func() {
			refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			refreshSpan := tracepkg.Start(refreshCtx, "APP", "app.upstream.initial-discovery", "Starting initial Upstream discovery", tracepkg.Bool("force_refresh", false))
			if err := a.Tools.RefreshUpstreams(refreshCtx, false); err != nil {
				refreshSpan.FailMessage("Initial Upstream discovery failed", err)
				if refreshCtx.Err() == nil && a.Logger != nil {
					a.Logger.Warning("UPSTREAM", "upstream.bootstrap.failed", "Initial upstream proxy discovery failed", err)
				}
				return
			}
			refreshSpan.EndMessage("Initial Upstream discovery completed")
		}()
	}
	if a.Tunnel != nil {
		tunnelSpan := tracepkg.Start(ctx, "APP", "app.tunnel.start", "Starting tunnel runtime")
		if err := a.Tunnel.StartContext(ctx); err != nil {
			tunnelSpan.FailMessage("Tunnel runtime start failed", err)
			span.FailMessage("Application runtime start failed", err)
			if a.ApprovalNotifications != nil {
				a.ApprovalNotifications.Stop()
			}
			if a.ApprovalExplain != nil {
				a.ApprovalExplain.Stop()
			}
			if a.BackgroundNotifications != nil {
				a.BackgroundNotifications.Stop()
			}
			if a.Tools != nil && a.Tools.Completions != nil {
				a.Tools.Completions.Close()
			}
			if a.Tools != nil && a.Tools.CompletionHooks != nil {
				a.Tools.CompletionHooks.Stop()
			}
			a.runtimeCtx = nil
			if a.Tools != nil && a.Tools.Workspaces != nil {
				err = errors.Join(err, a.Tools.Workspaces.Deactivate())
			}
			a.recordRuntimeUsage(ctx, producttelemetry.EventRuntimeStarted, started, err)
			return err
		}
		tunnelSpan.EndMessage("Tunnel runtime started", tracepkg.Bool("enabled", a.Tunnel.Status().Enabled), tracepkg.Bool("running", a.Tunnel.Status().Running))
	}
	if a.ProductLifecycleTelemetry != nil {
		a.ProductLifecycleTelemetry.Start(ctx)
	}
	a.running = true
	a.recordRuntimeUsage(ctx, producttelemetry.EventRuntimeStarted, started, nil)
	span.EndMessage("Application runtime started", tracepkg.Bool("running", true))
	return nil
}

func (a *App) Stop() error {
	started := time.Now()
	span := tracepkg.StartObserver(a.trace, "APP", "app.runtime.stop", "Stopping application runtime")
	var stopErr error
	if a.Tools != nil && a.Tools.Approvals != nil {
		for _, request := range a.Tools.Approvals.List(approval.Filter{Status: approval.StatusPending}) {
			if _, err := a.Tools.Approvals.Cancel(request.ID, "runtime", "runtime shutdown"); err != nil {
				stopErr = errors.Join(stopErr, err)
			}
		}
	}
	if a.ApprovalExplain != nil {
		a.ApprovalExplain.Stop()
	}
	if a.Tools != nil && a.Tools.Completions != nil {
		a.Tools.Completions.Close()
	}
	if a.Tools != nil && a.Tools.CompletionHooks != nil {
		a.Tools.CompletionHooks.Stop()
	}
	if a.ApprovalNotifications != nil {
		a.ApprovalNotifications.Stop()
	}
	if a.MCP != nil {
		subscriptionsSpan := tracepkg.StartObserver(a.trace, "APP", "app.mcp.subscriptions.close", "Closing MCP subscriptions")
		if a.Logger != nil {
			a.Logger.Verbose("RUNTIME", "runtime.subscriptions.closing", "Closing MCP subscriptions")
		}
		a.MCP.CloseSubscriptions()
		subscriptionsSpan.EndMessage("MCP subscriptions closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if a.Tools != nil && a.Tools.Processes != nil {
		if err := a.Tools.Processes.Shutdown(ctx); err != nil {
			stopErr = errors.Join(stopErr, err)
		}
	}
	if a.BackgroundNotifications != nil {
		a.BackgroundNotifications.Stop()
	}
	if a.Telegram != nil {
		a.Telegram.Stop()
	}
	if a.Notifications != nil {
		a.Notifications.Stop()
	}
	if a.Tunnel != nil {
		tunnelSpan := tracepkg.StartObserver(a.trace, "APP", "app.tunnel.stop", "Stopping tunnel runtime")
		if err := a.Tunnel.StopContext(ctx); err != nil {
			tunnelSpan.FailMessage("Tunnel runtime stop failed", err)
			stopErr = errors.Join(stopErr, err)
		} else {
			tunnelSpan.EndMessage("Tunnel runtime stopped")
		}
	}
	if a.Upstream != nil {
		if a.Logger != nil {
			a.Logger.Verbose("UPSTREAM", "upstream.stopping", "Stopping upstream servers")
		}
		upstreamSpan := tracepkg.StartObserver(a.trace, "APP", "app.upstream.shutdown", "Shutting down Upstream manager")
		if err := a.Upstream.Shutdown(ctx); err != nil {
			upstreamSpan.FailMessage("Upstream manager shutdown failed", err)
			if a.Logger != nil {
				a.Logger.Failure("UPSTREAM", "upstream.shutdown.failed", "Upstream shutdown failed", err)
			}
			stopErr = errors.Join(stopErr, err)
		} else {
			upstreamSpan.EndMessage("Upstream manager shut down")
			if a.Logger != nil {
				a.Logger.Verbose("UPSTREAM", "upstream.stopped", "Upstream servers stopped")
			}
		}
	}
	if a.ProductLifecycleTelemetry != nil {
		a.ProductLifecycleTelemetry.Stop()
	}
	if a.Tools != nil && a.Tools.BackgroundDeliveries != nil {
		a.Tools.BackgroundDeliveries.Close()
	}
	if a.Tools != nil && a.Tools.Executions != nil {
		a.Tools.Executions.Close()
	}
	if a.Tools != nil && a.Tools.Processes != nil {
		a.Tools.Processes.CloseSubscriptions()
	}
	if a.Tools != nil && a.Tools.Workspaces != nil {
		stopErr = errors.Join(stopErr, a.Tools.Workspaces.Deactivate())
	}
	a.runtimeCtx = nil
	a.running = false
	a.recordRuntimeUsage(context.Background(), producttelemetry.EventRuntimeStopped, started, stopErr)
	if a.ProductTelemetry != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		a.ProductTelemetry.Close(flushCtx)
		cancel()
	}
	if stopErr != nil {
		span.FailMessage("Application runtime stop failed", stopErr)
	} else {
		span.EndMessage("Application runtime stopped", tracepkg.Bool("running", false))
	}
	return stopErr
}

func (a *App) recordRuntimeUsage(ctx context.Context, name producttelemetry.EventName, started time.Time, err error) {
	if a == nil || a.ProductTelemetry == nil {
		return
	}
	success := err == nil
	errorCode := producttelemetry.ErrorCode("")
	if err != nil {
		errorCode = producttelemetry.ErrorInternal
		if errors.Is(err, context.Canceled) {
			errorCode = producttelemetry.ErrorCancelled
		} else if errors.Is(err, context.DeadlineExceeded) {
			errorCode = producttelemetry.ErrorTimeout
		}
	}
	a.ProductTelemetry.Record(ctx, name, producttelemetry.Usage{
		Interface: producttelemetry.InterfaceRuntime,
		Feature:   "runtime",
		ErrorCode: errorCode,
		Duration:  time.Since(started),
		Success:   success,
	})
}
