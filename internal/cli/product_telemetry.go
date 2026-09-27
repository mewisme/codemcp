package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

type cliProductUsageRecorder interface {
	Record(context.Context, producttelemetry.EventName, producttelemetry.Usage) bool
	Close(context.Context)
}

var newCLIProductUsageRecorder = func(cfg config.Config, configured bool) (cliProductUsageRecorder, error) {
	effective := config.ResolveTelemetryEnabled(cfg, configured)
	return producttelemetry.NewRecorder(producttelemetry.RecorderOptions{
		Enabled:  effective.Enabled,
		Endpoint: producttelemetry.Endpoint,
	})
}

func recordCLIProductUsage(ctx context.Context, command *cobra.Command, commandErr error, started time.Time) {
	if command == nil || productTelemetrySuppressed(command) || application.OperationObserved(ctx) {
		return
	}
	operation, ok := canonicalCommandOperation(command)
	if !ok {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		return
	}
	source, err := config.Source()
	if err != nil {
		return
	}
	recorder, err := newCLIProductUsageRecorder(cfg, source.Exists)
	if err != nil || recorder == nil {
		return
	}
	success := commandErr == nil
	recorder.Record(ctx, producttelemetry.EventOperationCompleted, producttelemetry.Usage{
		Interface: producttelemetry.InterfaceCLI,
		Command:   string(operation),
		Feature:   string(operation),
		ErrorCode: application.ProductErrorCode(commandErr),
		Duration:  time.Since(started),
		Success:   success,
	})
	closeCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	recorder.Close(closeCtx)
	cancel()
}
