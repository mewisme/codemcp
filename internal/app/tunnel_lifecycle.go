package app

import (
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/runtime/activity"
	"go.mewis.me/codemcp/internal/tunnel"
)

func (a *App) attachTunnelLifecycle() {
	if a == nil || a.Tunnel == nil {
		return
	}
	a.Tunnel.SetLifecycleObserver(tunnel.TraceLifecycleObserver(a.trace, func(event tunnel.LifecycleEvent) {
		fields := []logger.Field{}
		if event.ID != "" {
			fields = append(fields, logger.WithVerbose("tunnel_id", event.ID))
		}
		if a.Logger != nil {
			switch event.State {
			case tunnel.LifecycleConnecting:
				a.Logger.Action("TUNNEL", "tunnel.connecting", "Connecting tunnel", fields...)
			case tunnel.LifecycleReconnecting:
				fields = append(fields, logger.WithVerbose("attempt", event.Attempt), logger.WithVerbose("retry_in", event.RetryIn.String()))
				a.Logger.Action("TUNNEL", "tunnel.reconnecting", "Reconnecting tunnel", fields...)
			case tunnel.LifecycleReady:
				a.Logger.Ready("TUNNEL", "tunnel.connected", "Tunnel connected", fields...)
			case tunnel.LifecycleDegraded:
				var err error
				if strings.TrimSpace(event.Message) != "" {
					err = errors.New(strings.TrimSpace(event.Message))
				}
				a.Logger.Warning("TUNNEL", "tunnel.degraded", "Tunnel degraded", err, fields...)
			case tunnel.LifecycleStopped:
				a.Logger.Ready("TUNNEL", "tunnel.stopped", "Tunnel stopped", fields...)
			}
		}
		if a.Activity != nil {
			message := strings.TrimSpace(event.Message)
			if event.ID != "" {
				if message != "" {
					message = "tunnel_id=" + event.ID + " " + message
				} else {
					message = "tunnel_id=" + event.ID
				}
			}
			a.Activity.Publish(activity.Event{Kind: "tunnel." + string(event.State), Source: "tunnel", Status: string(event.State), Message: message})
		}
	}))
}
