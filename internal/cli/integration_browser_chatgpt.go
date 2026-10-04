package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/integrations/chatgptweb"
)

const chatGPTWebLoginInstruction = "Complete sign-in in the CodeMCP browser, then close the CodeMCP browser completely to continue verification"

func browserIntegrationCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "browser", Short: "Inspect the optional browser integration"}
	cmd.AddCommand(browserIntegrationStatusCommand(), browserIntegrationDoctorCommand())
	return cmd
}

func browserIntegrationStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show browser integration status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := application.NewBrowserIntegrationService().Status(cmd.Context())
			if err != nil {
				return err
			}
			renderBrowserIntegrationStatus(commandPresenter(cmd), status)
			return nil
		},
	}
}

func browserIntegrationDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use: "doctor", Short: "Check browser integration health", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewBrowserIntegrationService().Doctor(cmd.Context())
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			presenter.Frame("Browser doctor")
			for _, check := range result.Checks {
				kind := presentation.StatusSuccess
				if !check.OK {
					kind = presentation.StatusWarning
				}
				presenter.StateSection(kind, check.Message)
			}
			presenter.Complete("Doctor complete")
			return nil
		},
	}
}

func renderBrowserIntegrationStatus(presenter *presentation.Presenter, status application.BrowserIntegrationStatus) {
	presenter.Frame("Browser integration")
	kind := presentation.StatusInfo
	if status.State == application.BrowserIntegrationAvailable || status.State == application.BrowserIntegrationRunning {
		kind = presentation.StatusSuccess
	} else if status.State == application.BrowserIntegrationUnavailable {
		kind = presentation.StatusWarning
	}
	presenter.StateSection(kind, "Browser is "+string(status.State))
	fields := []presentation.Field{
		{Label: "enabled", Value: status.Enabled},
		{Label: "graphical", Value: status.Graphical},
		{Label: "running", Value: status.Running},
	}
	if status.Family != "" {
		fields = append(fields, presentation.Field{Label: "family", Value: status.Family})
	}
	if status.Version != "" {
		fields = append(fields, presentation.Field{Label: "version", Value: status.Version})
	}
	if status.HostPlatform != "" {
		fields = append(fields, presentation.Field{Label: "host platform", Value: status.HostPlatform})
	}
	if status.Transport != "" {
		fields = append(fields, presentation.Field{Label: "transport", Value: status.Transport})
	}
	if strings.TrimSpace(status.Reason) != "" {
		fields = append(fields, presentation.Field{Label: "reason", Value: status.Reason})
	}
	presenter.NestedFields(fields...)
	presenter.Complete("Status complete")
}

func chatGPTWebIntegrationCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "chatgpt-web", Short: "Manage the ChatGPT Web browser integration"}
	cmd.AddCommand(
		chatGPTWebStatusCommand(),
		chatGPTWebLoginCommand(),
		chatGPTWebLogoutCommand(),
		chatGPTWebDoctorCommand(),
	)
	return cmd
}

func chatGPTWebStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show ChatGPT Web integration status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := application.NewChatGPTWebService().Status(cmd.Context())
			if err != nil {
				return err
			}
			renderChatGPTWebStatus(commandPresenter(cmd), status)
			return nil
		},
	}
}

func chatGPTWebLoginCommand() *cobra.Command {
	return &cobra.Command{
		Use: "login", Short: "Sign in to ChatGPT using the isolated CodeMCP browser profile", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			presenter := commandPresenter(cmd)
			presenter.Frame("ChatGPT Web login")
			presenter.StateSection(presentation.StatusInfo, chatGPTWebLoginInstruction)
			status, err := application.NewChatGPTWebService().Login(cmd.Context())
			if err != nil {
				if strings.TrimSpace(status.Reason) != "" {
					presenter.StateSection(presentation.StatusWarning, status.Reason)
				}
				return err
			}
			presenter.StateSection(presentation.StatusSuccess, "ChatGPT authentication verified")
			presenter.NestedFields(
				presentation.Field{Label: "state", Value: status.State},
				presentation.Field{Label: "connector", Value: status.ConnectorName},
			)
			if account, ok := status.LoginAccount(); ok {
				renderChatGPTWebLoginAccount(presenter, account)
			}
			presenter.Complete("Login complete")
			return nil
		},
	}
}

func renderChatGPTWebLoginAccount(presenter *presentation.Presenter, account chatgptweb.AccountSummary) {
	fields := make([]presentation.Field, 0, 2)
	if value := strings.TrimSpace(account.Name); value != "" {
		fields = append(fields, presentation.Field{Label: "name", Value: value})
	}
	if value := strings.TrimSpace(account.Email); value != "" {
		fields = append(fields, presentation.Field{Label: "email", Value: value})
	}
	if len(fields) > 0 {
		presenter.NestedFieldGroup("account", fields...)
	}
}

func chatGPTWebLogoutCommand() *cobra.Command {
	var confirm bool
	var force bool
	cmd := &cobra.Command{
		Use: "logout", Short: "Delete only the CodeMCP-owned local ChatGPT browser profile", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDestructiveConfirmation(confirm, "ChatGPT Web local logout"); err != nil {
				return err
			}
			status, err := application.NewChatGPTWebService().Logout(cmd.Context(), force)
			if err != nil {
				return err
			}
			renderMutationSuccess(cmd, "Local ChatGPT browser profile removed",
				presentation.Field{Label: "state", Value: status.State},
				presentation.Field{Label: "server sessions", Value: "unchanged"},
			)
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm deletion of the CodeMCP-owned local ChatGPT browser profile")
	cmd.Flags().BoolVar(&force, "force", false, "close same-process CodeMCP browser ownership before local logout")
	return cmd
}

func chatGPTWebDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use: "doctor", Short: "Check ChatGPT Web authentication and connector readiness", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := application.NewChatGPTWebService().Doctor(cmd.Context())
			if err != nil {
				return err
			}
			presenter := commandPresenter(cmd)
			presenter.Frame("ChatGPT Web doctor")
			for _, check := range result.Checks {
				kind := presentation.StatusSuccess
				if !check.OK {
					kind = presentation.StatusWarning
				}
				presenter.StateSection(kind, check.Message)
			}
			presenter.NestedFields(
				presentation.Field{Label: "state", Value: result.Status.State},
				presentation.Field{Label: "connector", Value: result.Status.ConnectorName},
				presentation.Field{Label: "live verified", Value: result.LiveVerified},
			)
			presenter.Complete("Doctor complete")
			return nil
		},
	}
}

func renderChatGPTWebStatus(presenter *presentation.Presenter, status application.ChatGPTWebStatus) {
	presenter.Frame("ChatGPT Web integration")
	kind := presentation.StatusInfo
	switch status.State {
	case chatgptweb.StateReady:
		kind = presentation.StatusSuccess
	case chatgptweb.StateBrowserUnavailable, chatgptweb.StateNeedsLogin, chatgptweb.StateConnectorUnavailable, chatgptweb.StateDegraded:
		kind = presentation.StatusWarning
	}
	presenter.StateSection(kind, "ChatGPT Web is "+string(status.State))
	fields := []presentation.Field{
		{Label: "enabled", Value: status.Enabled},
		{Label: "browser", Value: status.BrowserState},
		{Label: "authenticated", Value: status.Authenticated},
		{Label: "connector", Value: status.ConnectorName},
		{Label: "connector available", Value: status.ConnectorAvailable},
		{Label: "max agents", Value: status.MaxAgents},
	}
	if status.BrowserFamily != "" {
		fields = append(fields, presentation.Field{Label: "browser family", Value: status.BrowserFamily})
	}
	if status.BrowserTransport != "" {
		fields = append(fields, presentation.Field{Label: "browser transport", Value: status.BrowserTransport})
	}
	if strings.TrimSpace(status.Reason) != "" {
		fields = append(fields, presentation.Field{Label: "reason", Value: status.Reason})
	}
	presenter.NestedFields(fields...)
	presenter.Complete("Status complete")
}
