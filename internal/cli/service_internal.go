package cli

import (
	"context"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	managed "go.mewis.me/codemcp/internal/service"
)

type serviceRuntimeInfo struct {
	Managed bool
	ID      string
	Scope   string
}

type serviceRuntimeContextKey struct{}

func internalServiceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "_service", Short: "Internal managed service commands", Hidden: true}
	var serviceID, serviceScope, environmentHash string
	run := &cobra.Command{
		Use:    "run",
		Short:  "Run chatgpt-mcp as an internal managed service",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "SERVICE", "service.internal.starting", "Starting managed service runtime", logger.WithVerbose("service_id", serviceID), logger.WithVerbose("scope", serviceScope))
			if environmentHash != "" {
				logCommandStep(cmd, "SERVICE", "service.environment.loading", "Loading managed service environment snapshot")
				snapshot, err := managed.LoadEnvironment(config.RootPath(), environmentHash)
				if err != nil {
					return err
				}
				if err := managed.ApplyEnvironment(snapshot); err != nil {
					return err
				}
				logCommandDebug(cmd, "SERVICE", "service.environment.applied", "Managed service environment snapshot applied", logger.WithDebug("variables", len(snapshot.Values)))
			}
			ctx := context.WithValue(cmd.Context(), serviceRuntimeContextKey{}, serviceRuntimeInfo{Managed: true, ID: serviceID, Scope: serviceScope})
			cmd.SetContext(ctx)
			return runServer(cmd, args)
		},
	}
	run.Flags().StringVar(&serviceID, "service-id", "", "managed service identity")
	run.Flags().StringVar(&serviceScope, "service-scope", "user", "managed service scope")
	run.Flags().StringVar(&environmentHash, "service-environment-hash", "", "managed environment snapshot hash")
	cmd.AddCommand(run)
	return cmd
}

func runtimeServiceInfo(cmd *cobra.Command) serviceRuntimeInfo {
	if cmd == nil || cmd.Context() == nil {
		return serviceRuntimeInfo{}
	}
	info, _ := cmd.Context().Value(serviceRuntimeContextKey{}).(serviceRuntimeInfo)
	return info
}
