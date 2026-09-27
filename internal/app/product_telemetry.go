package app

import (
	"context"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
	"go.mewis.me/codemcp/internal/tools"
)

func productToolObserver(recorder productTelemetryRuntime) tools.CallObserver {
	if recorder == nil {
		return nil
	}
	return func(observation tools.CallObservation) {
		if observation.Phase != "finish" {
			return
		}
		operation, ok := capability.ForMCPTool(observation.Tool)
		if !ok || isTelemetryCapability(operation) {
			return
		}
		source := strings.ToLower(strings.TrimSpace(observation.Source))
		switch source {
		case "http", "stdio", "tunnel", "openai":
		default:
			return
		}
		success := observation.Status == "ok"
		errorCode := producttelemetry.ErrorCode("")
		if !success {
			errorCode = producttelemetry.ErrorInternal
			if observation.Status == "cancelled" {
				errorCode = producttelemetry.ErrorCancelled
			}
		}
		recorder.Record(context.Background(), producttelemetry.EventOperationCompleted, producttelemetry.Usage{
			Interface: producttelemetry.InterfaceMCP,
			Command:   string(operation),
			Feature:   string(operation),
			ErrorCode: errorCode,
			Duration:  time.Duration(max(observation.DurationMS, 0)) * time.Millisecond,
			Success:   success,
		})
	}
}

func isTelemetryCapability(operation capability.ID) bool {
	switch operation {
	case capability.TelemetryStatus, capability.TelemetryEnable, capability.TelemetryDisable, capability.TelemetryShow:
		return true
	default:
		return false
	}
}
