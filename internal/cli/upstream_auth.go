package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/logger"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func upstreamServerAuthCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage OAuth authorization for an Upstream server"}
	command.AddCommand(upstreamServerAuthLoginCommand(), upstreamServerAuthStatusCommand(), upstreamServerAuthLogoutCommand())
	return command
}

func upstreamServerAuthLoginCommand() *cobra.Command {
	var issuer, clientID, clientSecretEnv, clientMetadataURL, extraScope string
	var noOpen bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:               "login <id>",
		Short:             "Authorize an HTTP Upstream with OAuth",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			server, ok := manager.Get(args[0])
			if !ok {
				return &upstreamServerNotFoundError{ServerID: args[0]}
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
			progress := newCommandProgress(cmd, "OAUTH")
			progress.Start("oauth.starting", "Starting OAuth authorization", "OAuth authorization started")
			credential, err := store.Login(ctx, mcpoauth.LoginConfig{
				ServerID: server.ID, ServerURL: server.URL, Scope: server.Auth.Scope, Issuer: issuer,
				ClientID: clientID, ClientSecretEnvVar: clientSecretEnv, ClientMetadataURL: clientMetadataURL,
			}, mcpoauth.LoginOptions{ExtraScope: extraScope, OnURL: func(raw string) error {
				commandProgressSession(cmd).Append(func(p *presentation.Presenter) {
					p.ChildStatus(presentation.StatusInfo, "Authorization required")
					p.Fields(presentation.Field{Label: "url", Value: raw})
				})
				browserSpan := tracepkg.Start(ctx, "OAUTH", "oauth.browser.open", "Opening OAuth authorization in browser", tracepkg.Bool("skipped", noOpen))
				if noOpen {
					browserSpan.EndMessage("OAuth browser open skipped", tracepkg.Bool("skipped", true))
				} else if err := application.OpenBrowser(raw); err != nil {
					browserSpan.FailMessage("OAuth browser open failed", fmt.Errorf("browser open failed"), tracepkg.Bool("skipped", false))
					log.Verbose("OAUTH", "oauth.browser.open-failed", "Could not open OAuth browser", logger.WithVerbose("error", err.Error()))
					commandProgressSession(cmd).Append(func(p *presentation.Presenter) {
						p.ChildStatus(presentation.StatusWarning, "Could not open browser; use the URL above")
						p.Fields(presentation.Field{Label: "reason", Value: err.Error()})
					})
				} else {
					browserSpan.EndMessage("OAuth authorization opened in browser", tracepkg.Bool("skipped", false))
				}
				progress.Start("oauth.waiting", "Waiting for OAuth authorization", "OAuth authorization completed")
				return nil
			}})
			if err != nil {
				progress.Stop()
				return err
			}
			progress.Complete()
			progress = newCommandProgress(cmd, "UPSTREAM")
			progress.Start("upstream.health.checking", "Checking Upstream health", "Checked Upstream health")
			healthSpan := tracepkg.Start(ctx, "OAUTH", "oauth.post-login.health", "Checking Upstream health after OAuth login", tracepkg.String("server", server.ID))
			status := manager.CheckHealth(ctx, server.ID, true)
			if status.Health != "connected" {
				healthSpan.FailMessage("Post-login Upstream health check failed", fmt.Errorf("upstream health check did not connect"), tracepkg.String("health", string(status.Health)))
				log.Verbose("UPSTREAM", "oauth.post-login.health-warning", "OAuth completed but Upstream health check did not connect", logger.WithVerbose("error", status.LastError))
				commandProgressSession(cmd).Append(func(p *presentation.Presenter) {
					p.ChildStatus(presentation.StatusWarning, "OAuth completed but Upstream health check did not connect")
					if strings.TrimSpace(status.LastError) != "" {
						p.Fields(presentation.Field{Label: "reason", Value: status.LastError})
					}
				})
			} else {
				healthSpan.EndMessage("Post-login Upstream health check connected", tracepkg.String("health", string(status.Health)), tracepkg.Int("tool_count", status.ToolCount))
			}
			progress.Complete()
			fields := []presentation.Field{
				{Label: "issuer", Value: credential.Issuer},
				{Label: "registration", Value: credential.Registration},
				{Label: "scopes", Value: strings.Join(credential.Scopes, " ")},
				{Label: "health", Value: status.Health},
			}
			if !credential.ExpiresAt.IsZero() {
				fields = append(fields, presentation.Field{Label: "expires", Value: credential.ExpiresAt.Format(time.RFC3339)})
			}
			renderEntityMutationSuccess(cmd, "Authorization stored", server.ID, fields...)
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
				return &upstreamServerNotFoundError{ServerID: args[0]}
			}
			status, err := oauthStoreForCommand(cmd).Status(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return writeResultJSON(cmd, status)
			}
			renderUpstreamOAuthStatus(commandPresenter(cmd), status)
			return nil
		},
	}
	addJSONResultFlag(command, &asJSON)
	return command
}

func renderUpstreamOAuthStatus(presenter *presentation.Presenter, status mcpoauth.Status) {
	presenter.Frame("Upstream OAuth authorization")
	if !status.Configured {
		presenter.StateSection(presentation.StatusInactive, "Not authorized")
		presenter.Subsection(status.ServerID)
		presenter.Complete("Done")
		return
	}
	presenter.StateSection(presentation.StatusSuccess, "Authorization configured")
	presenter.Subsection(status.ServerID)
	fields := []presentation.Field{
		{Label: "issuer", Value: status.Issuer},
		{Label: "registration", Value: status.Registration},
		{Label: "scopes", Value: strings.Join(status.Scopes, " ")},
		{Label: "refresh", Value: status.HasRefreshToken},
	}
	if status.ExpiresAt != nil {
		fields = append(fields, presentation.Field{Label: "expires", Value: status.ExpiresAt.Format(time.RFC3339)})
	}
	presenter.NestedFields(fields...)
	presenter.Complete("Done")
}

func upstreamServerAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "logout <id>",
		Short:             "Delete stored OAuth credentials for an Upstream server",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeUpstreamID,
		RunE: func(cmd *cobra.Command, args []string) error {
			logCommandStep(cmd, "OAUTH", "oauth.authorization.removing", "Removing upstream OAuth authorization", logger.WithVerbose("server", args[0]))
			manager, err := loadUpstreamManagerForCommand(cmd)
			if err != nil {
				return err
			}
			if _, ok := manager.Get(args[0]); !ok {
				return &upstreamServerNotFoundError{ServerID: args[0]}
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
			renderEntityMutationSuccess(cmd, "Authorization removed", args[0])
			return nil
		},
	}
}

func oauthStoreForCommand(cmd *cobra.Command) *mcpoauth.Store {
	return mcpoauth.NewStore(mcpoauth.Path()).SetTraceObserver(tracepkg.ObserverFromContext(cmd.Context()))
}
