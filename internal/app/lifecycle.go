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
	a.approvalNotificationsReady.Store(false)
	a.completionNotificationsReady.Store(a.CompletionNotifications != nil && a.BackgroundNotifications == nil)
	tasks := make([]runtimeStartupTask, 0, 5)
	if a.ApprovalExplain != nil {
		tasks = append(tasks, runtimeStartupTask{fatal: true, run: func() error {
			return a.ApprovalExplain.Start(ctx)
		}})
	}
	if a.Telegram != nil {
		tasks = append(tasks, runtimeStartupTask{run: func() error {
			err := a.Telegram.Reconcile(ctx, a.Config.Snapshot().Telegram)
			if a.TelegramUI != nil {
				go a.TelegramUI.ReconcilePendingRuntimeOperations(ctx)
			}
			return err
		}, onError: func(err error) {
			if a.Logger != nil {
				a.Logger.Warning("TELEGRAM", "telegram.runtime.start.failed", "Telegram runtime could not start", err)
			}
		}})
	}
	if a.ApprovalNotifications != nil {
		tasks = append(tasks, runtimeStartupTask{run: func() error {
			err := a.ApprovalNotifications.Start(ctx)
			a.approvalNotificationsReady.Store(err == nil)
			return err
		}, onError: func(err error) {
			if a.Logger != nil {
				a.Logger.Warning("NOTIFICATION", "notification.coordinator.start.failed", "Approval notification coordinator could not start", err)
			}
		}})
	}
	if a.BackgroundNotifications != nil {
		tasks = append(tasks, runtimeStartupTask{run: func() error {
			err := a.BackgroundNotifications.Start(ctx)
			a.completionNotificationsReady.Store(err == nil && a.CompletionNotifications != nil)
			return err
		}, onError: func(err error) {
			if a.Logger != nil {
				a.Logger.Warning("NOTIFICATION", "notification.background.start.failed", "Background job notification bridge could not start", err)
			}
		}})
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
		go func() {
			catchUpSpan := tracepkg.Start(ctx, "APP", "app.codegraph.completion-catch-up", "Starting CodeGraph completion catch-up")
			if err := a.Tools.CatchUpCodeGraphCompletions(ctx); err != nil {
				catchUpSpan.FailMessage("CodeGraph completion catch-up failed", err)
				return
			}
			catchUpSpan.EndMessage("CodeGraph completion catch-up completed")
		}()
	}
	if a.Tunnel != nil {
		tasks = append(tasks, runtimeStartupTask{fatal: true, run: func() error {
			tunnelSpan := tracepkg.Start(ctx, "APP", "app.tunnel.start", "Starting tunnel runtime")
			snapshot := a.Tunnel.Snapshot()
			if snapshot.Status.Enabled && snapshot.Configured {
				if err := a.Tunnel.StartContext(ctx); err != nil {
					tunnelSpan.FailMessage("Tunnel runtime start failed", err)
					return err
				}
			}
			status := a.Tunnel.Status()
			tunnelSpan.EndMessage("Tunnel runtime reconciled", tracepkg.Bool("enabled", status.Enabled), tracepkg.Bool("configured", snapshot.Configured), tracepkg.Bool("running", status.Running))
			return nil
		}})
	}
	if err := runRuntimeStartupTasks(tasks); err != nil {
		err = a.rollbackRuntimeStart(ctx, err)
		a.recordRuntimeUsage(ctx, producttelemetry.EventRuntimeStarted, started, err)
		span.FailMessage("Application runtime start failed", err)
		return err
	}
	if a.ProductLifecycleTelemetry != nil {
		a.ProductLifecycleTelemetry.Start(ctx)
	}
	a.running = true
	a.recordRuntimeUsage(ctx, producttelemetry.EventRuntimeStarted, started, nil)
	span.EndMessage("Application runtime started", tracepkg.Bool("running", true))
	return nil
}

type runtimeStartupTask struct {
	run     func() error
	onError func(error)
	fatal   bool
}

type runtimeStartupResult struct {
	task runtimeStartupTask
	err  error
}

func runRuntimeStartupTasks(tasks []runtimeStartupTask) error {
	if len(tasks) == 0 {
		return nil
	}
	results := make(chan runtimeStartupResult, len(tasks))
	for _, task := range tasks {
		task := task
		go func() {
			var err error
			if task.run != nil {
				err = task.run()
			}
			results <- runtimeStartupResult{task: task, err: err}
		}()
	}
	var fatalErr error
	for range tasks {
		result := <-results
		if result.err == nil {
			continue
		}
		if result.task.fatal {
			fatalErr = errors.Join(fatalErr, result.err)
			continue
		}
		if result.task.onError != nil {
			result.task.onError(result.err)
		}
	}
	return fatalErr
}

func (a *App) rollbackRuntimeStart(ctx context.Context, cause error) error {
	if a.ApprovalNotifications != nil {
		a.ApprovalNotifications.Stop()
	}
	if a.ApprovalExplain != nil {
		a.ApprovalExplain.Stop()
	}
	if a.BackgroundNotifications != nil {
		a.BackgroundNotifications.Stop()
	}
	if a.Telegram != nil {
		a.Telegram.Stop()
	}
	if a.Tunnel != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		cause = errors.Join(cause, a.Tunnel.StopContext(stopCtx))
		cancel()
	}
	if a.Tools != nil && a.Tools.Completions != nil {
		a.Tools.Completions.Close()
	}
	if a.Tools != nil && a.Tools.CompletionHooks != nil {
		a.Tools.CompletionHooks.Stop()
	}
	a.approvalNotificationsReady.Store(false)
	a.completionNotificationsReady.Store(false)
	a.runtimeCtx = nil
	if a.Tools != nil && a.Tools.Workspaces != nil {
		cause = errors.Join(cause, a.Tools.Workspaces.Deactivate())
	}
	return cause
}

func (a *App) Stop() error {
	started := time.Now()
	span := tracepkg.StartObserver(a.trace, "APP", "app.runtime.stop", "Stopping application runtime")
	var stopErr error
	agentCtx, agentCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if a.Tools != nil && a.Tools.Agents != nil {
		if err := a.Tools.Agents.Shutdown(agentCtx); err != nil {
			stopErr = errors.Join(stopErr, err)
		}
	}
	agentCancel()
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
		stopErr = errors.Join(stopErr, a.Tools.Executions.Close())
	}
	if a.Tools != nil && a.Tools.Processes != nil {
		a.Tools.Processes.CloseSubscriptions()
	}
	if a.Tools != nil && a.Tools.Workspaces != nil {
		stopErr = errors.Join(stopErr, a.Tools.Workspaces.Deactivate())
	}
	a.runtimeCtx = nil
	a.running = false
	a.approvalNotificationsReady.Store(false)
	a.completionNotificationsReady.Store(false)
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
