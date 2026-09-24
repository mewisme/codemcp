package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/logger"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func upstreamServerAuthCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage OAuth authorization for an upstream MCP server"}
	command.AddCommand(upstreamServerAuthLoginCommand(), upstreamServerAuthStatusCommand(), upstreamServerAuthLogoutCommand())
	return command
}

func upstreamServerAuthLoginCommand() *cobra.Command {
	var issuer, clientID, clientSecretEnv, clientMetadataURL, extraScope string
	var noOpen bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:               "login <id>",
		Short:             "Authorize an HTTP MCP server with OAuth",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			server, ok := manager.Get(args[0])
			if !ok {
				return fmt.Errorf("unknown upstream server: %s", args[0])
			}
			if server.Transport != "http" {
				return fmt.Errorf("OAuth login requires an HTTP upstream server")
			}
			if server.Auth.Type == "none" {
				return fmt.Errorf("OAuth is disabled for %s; configure --auth oauth or --auth auto first", server.ID)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			logCommandStep(cmd, "OAUTH", "oauth.authorization.preparing", "Preparing upstream OAuth authorization", logger.WithVerbose("server", server.ID))
			store := oauthStoreForCommand(cmd)
			log := commandLogger(cmd)
			startCommandSpinner(cmd, log, "OAUTH", "oauth.starting", "Starting OAuth authorization")
			credential, err := store.Login(ctx, mcpoauth.LoginConfig{
				ServerID: server.ID, ServerURL: server.URL, Scope: server.Auth.Scope, Issuer: issuer,
				ClientID: clientID, ClientSecretEnvVar: clientSecretEnv, ClientMetadataURL: clientMetadataURL,
			}, mcpoauth.LoginOptions{ExtraScope: extraScope, OnURL: func(raw string) error {
				log.Info("OAUTH", "authorization required")
				log.Detail("url", raw)
				browserSpan := tracepkg.Start(ctx, "OAUTH", "oauth.browser.open", "Opening OAuth authorization in browser", tracepkg.Bool("skipped", noOpen))
				if noOpen {
					browserSpan.EndMessage("OAuth browser open skipped", tracepkg.Bool("skipped", true))
				} else if err := application.OpenBrowser(raw); err != nil {
					browserSpan.FailMessage("OAuth browser open failed", fmt.Errorf("browser open failed"), tracepkg.Bool("skipped", false))
					log.Warn("OAUTH", "could not open browser; use the URL above", "error", err)
				} else {
					browserSpan.EndMessage("OAuth authorization opened in browser", tracepkg.Bool("skipped", false))
				}
				startCommandSpinner(cmd, log, "OAUTH", "oauth.waiting", "Waiting for OAuth authorization")
				return nil
			}})
			if err != nil {
				return err
			}
			log.Success("OAUTH", "authorization stored", "id", server.ID)
			log.Detail("issuer", credential.Issuer)
			log.Detail("registration", credential.Registration)
			log.Detail("scopes", strings.Join(credential.Scopes, " "))
			if !credential.ExpiresAt.IsZero() {
				log.Detail("expires", credential.ExpiresAt.Format(time.RFC3339))
			}
			startCommandSpinner(cmd, log, "MCP", "mcp.health.checking", "Checking upstream MCP health")
			healthSpan := tracepkg.Start(ctx, "OAUTH", "oauth.post-login.health", "Checking upstream MCP health after OAuth login", tracepkg.String("server", server.ID))
			status := manager.CheckHealth(ctx, server.ID, true)
			if status.Health != "connected" {
				healthSpan.FailMessage("Post-login upstream MCP health check failed", fmt.Errorf("upstream MCP health check did not connect"), tracepkg.String("health", string(status.Health)))
				log.Warn("MCP", "OAuth completed but upstream health check did not connect", "error", status.LastError)
			} else {
				healthSpan.EndMessage("Post-login upstream MCP health check connected", tracepkg.String("health", string(status.Health)), tracepkg.Int("tool_count", status.ToolCount))
				log.Ready("MCP", "mcp.health.connected", "Upstream MCP server connected")
			}
			return nil
		},
	}
	command.Flags().StringVar(&issuer, "issuer", "", "authorization server issuer when the resource advertises multiple issuers")
	command.Flags().StringVar(&clientID, "client-id", "", "pre-registered OAuth client ID")
	command.Flags().StringVar(&clientSecretEnv, "client-secret-env", "", "environment variable containing a pre-registered client secret")
	command.Flags().StringVar(&clientMetadataURL, "client-metadata-url", "", "Client ID Metadata Document URL")
	command.Flags().StringVar(&extraScope, "scope", "", "additional OAuth scopes for this login")
	command.Flags().BoolVar(&noOpen, "no-open", false, "print the authorization URL without opening a browser")
	command.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "OAuth login timeout")
	return command
}

func upstreamServerAuthStatusCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:               "status <id>",
		Short:             "Show OAuth authorization status without secrets",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "OAUTH", "oauth.status.loading", "Loading upstream OAuth authorization state", logger.WithVerbose("server", args[0]))
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			if _, ok := manager.Get(args[0]); !ok {
				return fmt.Errorf("unknown upstream server: %s", args[0])
			}
			status, err := oauthStoreForCommand(cmd).Status(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return writeResultJSON(cmd, status)
			}
			log := commandLogger(cmd)
			if !status.Configured {
				log.Info("OAUTH", "not authorized", "id", args[0])
				return nil
			}
			log.Success("OAUTH", "authorization configured", "id", args[0])
			log.Detail("issuer", status.Issuer)
			log.Detail("registration", status.Registration)
			log.Detail("scopes", strings.Join(status.Scopes, " "))
			log.Detail("refresh", status.HasRefreshToken)
			if status.ExpiresAt != nil {
				log.Detail("expires", status.ExpiresAt.Format(time.RFC3339))
			}
			return nil
		},
	}
	addJSONResultFlag(command, &asJSON)
	return command
}

func upstreamServerAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "logout <id>",
		Short:             "Delete stored OAuth credentials for an upstream MCP server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "OAUTH", "oauth.authorization.removing", "Removing upstream OAuth authorization", logger.WithVerbose("server", args[0]))
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			if _, ok := manager.Get(args[0]); !ok {
				return fmt.Errorf("unknown upstream server: %s", args[0])
			}
			if err := oauthStoreForCommand(cmd).Delete(args[0]); err != nil {
				return err
			}
			disconnectSpan := tracepkg.Start(cmd.Context(), "OAUTH", "oauth.logout.disconnect", "Disconnecting upstream after OAuth authorization removal", tracepkg.String("server", args[0]))
			if err := manager.Disconnect(args[0]); err != nil {
				disconnectSpan.FailMessage("Upstream disconnect after OAuth logout failed", err)
				return err
			}
			disconnectSpan.EndMessage("Upstream disconnected after OAuth logout", tracepkg.Bool("credential_invalidated", true))
			commandLogger(cmd).Success("OAUTH", "authorization removed", "id", args[0])
			return nil
		},
	}
}

func oauthStoreForCommand(cmd *cobra.Command) *mcpoauth.Store {
	return mcpoauth.NewStore(mcpoauth.Path()).SetTraceObserver(tracepkg.ObserverFromContext(cmd.Context()))
}
