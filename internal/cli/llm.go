package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
)

const maxProtectedLLMInputBytes = 16 * 1024

func llmCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "llm", Short: "Manage LLM providers and models"}
	cmd.AddCommand(
		llmStatusCommand(),
		llmUseCommand(),
		llmModelsCommand(),
		llmProbeCommand(),
		llmProviderCommand(),
		llmCoreProviderCommand("openrouter", "openrouter"),
		llmCoreProviderCommand("ollama", "ollama"),
	)
	return cmd
}

func llmService() *application.LLMService {
	return application.NewLLMService(config.RootPath())
}

func llmStatusCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show LLM provider status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := llmService().Status(cmd.Context())
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result)
			}
			renderLLMStatus(cmd, result)
			return nil
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmUseCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:               "use <provider_id>",
		Short:             "Select the active LLM provider",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeLLMProviderIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := llmService()
			if _, err := service.SelectProvider(cmd.Context(), args[0]); err != nil {
				return err
			}
			result, err := service.ProviderResult(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return renderLLMProviderMutation(cmd, result, "LLM provider selected")
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmModelsCommand() *cobra.Command {
	return llmModelsCommandForProvider("")
}

func llmModelsCommandForProvider(fixed string) *cobra.Command {
	var refresh, jsonOutput bool
	use := "models [provider_id]"
	args := cobra.MaximumNArgs(1)
	if fixed != "" {
		use = "models"
		args = cobra.NoArgs
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: "List models for an LLM provider",
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := fixed
			if id == "" {
				var err error
				id, err = llmProviderArgumentOrActive(cmd, args)
				if err != nil {
					return err
				}
			}
			result, err := llmService().ModelCatalog(cmd.Context(), id, refresh)
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result)
			}
			renderLLMModels(cmd, result)
			return nil
		},
	}
	if fixed == "" {
		cmd.ValidArgsFunction = completeLLMProviderIDs
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Refresh the remote model catalog")
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmProbeCommand() *cobra.Command {
	return llmProbeCommandForProvider("")
}

func llmProbeCommandForProvider(fixed string) *cobra.Command {
	var jsonOutput bool
	use := "probe [provider_id]"
	args := cobra.MaximumNArgs(1)
	if fixed != "" {
		use = "probe"
		args = cobra.NoArgs
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: "Probe LLM provider readiness",
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := fixed
			if id == "" {
				var err error
				id, err = llmProviderArgumentOrActive(cmd, args)
				if err != nil {
					return err
				}
			}
			result, err := llmService().Probe(cmd.Context(), id)
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result)
			}
			presenter := commandPresenter(cmd)
			presenter.Frame("LLM probe")
			presenter.StateSection(presentation.StatusSuccess, "Provider ready")
			presenter.Fields(
				presentation.Field{Label: "provider", Value: result.ProviderID},
				presentation.Field{Label: "model", Value: result.Model},
				presentation.Field{Label: "readiness", Value: result.Readiness},
			)
			presenter.Complete("Done")
			return nil
		},
	}
	if fixed == "" {
		cmd.ValidArgsFunction = completeLLMProviderIDs
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmProviderCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "provider", Short: "Manage LLM providers"}
	cmd.AddCommand(
		llmProviderListCommand(),
		llmProviderShowCommand("", "show <provider_id>"),
		llmProviderAddCommand(),
		llmProviderConfigureCommand(),
		llmProviderRemoveCommand(),
		llmProviderKeyCommand(),
	)
	return cmd
}

func llmProviderListCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List LLM providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := llmService().Providers(cmd.Context())
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result)
			}
			renderLLMProviderList(cmd, result)
			return nil
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmProviderShowCommand(fixed string, use string) *cobra.Command {
	var jsonOutput bool
	if use == "" {
		use = "status"
	}
	args := cobra.ExactArgs(1)
	if fixed != "" {
		args = cobra.NoArgs
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: "Show LLM provider details",
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := fixed
			if id == "" {
				id = args[0]
			}
			result, err := llmService().ProviderResult(cmd.Context(), id)
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result)
			}
			renderLLMProvider(cmd, result)
			return nil
		},
	}
	if fixed == "" {
		cmd.ValidArgsFunction = completeLLMProviderIDs
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

type llmProviderFlags struct {
	name      string
	protocol  string
	baseURL   string
	model     string
	auth      string
	discovery string
}

func llmProviderAddCommand() *cobra.Command {
	var flags llmProviderFlags
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "add <provider_id>",
		Short: "Add a custom LLM provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(flags.baseURL) == "" {
				return errors.New("--base-url is required")
			}
			config := application.NewCustomLLMProviderConfig(flags.name, flags.protocol, flags.baseURL, flags.model, flags.auth, flags.discovery)
			provider, err := llmService().AddCustomProvider(cmd.Context(), args[0], config)
			if err != nil {
				return err
			}
			result, err := llmService().ProviderResult(cmd.Context(), string(provider.ID))
			if err != nil {
				return err
			}
			return renderLLMProviderMutation(cmd, result, "LLM provider added")
		},
	}
	addLLMProviderFlags(cmd, &flags, true)
	_ = cmd.MarkFlagRequired("protocol")
	_ = cmd.MarkFlagRequired("base-url")
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmProviderConfigureCommand() *cobra.Command {
	var flags llmProviderFlags
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:               "configure <provider_id>",
		Short:             "Configure a custom LLM provider",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCustomLLMProviderIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := llmService()
			existing, err := service.Provider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			name, protocol, baseURL, model, authMode, discovery := existing.Name, fmt.Sprint(existing.Protocol), existing.BaseURL, existing.Model, fmt.Sprint(existing.AuthMode), fmt.Sprint(existing.Discovery)
			if cmd.Flags().Changed("name") {
				name = flags.name
			}
			if cmd.Flags().Changed("protocol") {
				protocol = flags.protocol
			}
			if cmd.Flags().Changed("base-url") {
				baseURL = flags.baseURL
			}
			if cmd.Flags().Changed("model") {
				model = flags.model
			}
			if cmd.Flags().Changed("auth") {
				authMode = flags.auth
			}
			if cmd.Flags().Changed("discovery") {
				discovery = flags.discovery
			}
			config := application.NewCustomLLMProviderConfig(name, protocol, baseURL, model, authMode, discovery)
			provider, err := service.ConfigureCustomProvider(cmd.Context(), args[0], config)
			if err != nil {
				return err
			}
			result, err := service.ProviderResult(cmd.Context(), string(provider.ID))
			if err != nil {
				return err
			}
			return renderLLMProviderMutation(cmd, result, "LLM provider configured")
		},
	}
	addLLMProviderFlags(cmd, &flags, false)
	_ = cmd.RegisterFlagCompletionFunc("model", completeConfiguredLLMModel)
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func addLLMProviderFlags(cmd *cobra.Command, flags *llmProviderFlags, defaults bool) {
	protocol, auth, discovery := "", "", ""
	if defaults {
		protocol, auth, discovery = "openai", "none", "none"
	}
	cmd.Flags().StringVar(&flags.name, "name", "", "Provider display name")
	cmd.Flags().StringVar(&flags.protocol, "protocol", protocol, "Provider protocol: openai or anthropic")
	cmd.Flags().StringVar(&flags.baseURL, "base-url", "", "Provider API base URL")
	cmd.Flags().StringVar(&flags.model, "model", "", "Provider model ID")
	cmd.Flags().StringVar(&flags.auth, "auth", auth, "Authentication mode: none, bearer, or x-api-key")
	cmd.Flags().StringVar(&flags.discovery, "discovery", discovery, "Model discovery: none, openai-models, or ollama-tags")
	_ = cmd.RegisterFlagCompletionFunc("protocol", completeStatic("openai", "anthropic"))
	_ = cmd.RegisterFlagCompletionFunc("auth", completeStatic("none", "bearer", "x-api-key"))
	_ = cmd.RegisterFlagCompletionFunc("discovery", completeStatic("none", "openai-models", "ollama-tags"))
}

func llmProviderRemoveCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:               "remove <provider_id>",
		Short:             "Remove a custom LLM provider",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCustomLLMProviderIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := llmService()
			result, err := service.RemoveProviderResult(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result)
			}
			renderEntityMutationSuccess(cmd, "LLM provider removed", args[0])
			return nil
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmProviderKeyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Manage an LLM provider API key"}
	cmd.AddCommand(llmCredentialSetCommand(""), llmCredentialClearCommand(""))
	return cmd
}

func llmCredentialSetCommand(fixed string) *cobra.Command {
	var jsonOutput bool
	var fromEnv string
	use := "set <provider_id>"
	args := cobra.ExactArgs(1)
	if fixed != "" {
		use = "set"
		args = cobra.NoArgs
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: "Set an LLM provider API key from protected input",
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := fixed
			if id == "" {
				id = args[0]
			}
			secret, err := readProtectedLLMInput(cmd, "API key", fromEnv)
			if err != nil {
				return err
			}
			defer zeroString(&secret)
			service := llmService()
			if err := service.SetCredential(cmd.Context(), id, secret); err != nil {
				return err
			}
			result, err := service.CredentialResult(cmd.Context(), id)
			if err != nil {
				return err
			}
			return renderLLMCredentialResult(cmd, result, "LLM provider credential updated")
		},
	}
	if fixed == "" {
		cmd.ValidArgsFunction = completeLLMProviderIDs
	}
	cmd.Flags().StringVar(&fromEnv, "from-env", "", "Read the API key from this environment variable")
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmCredentialClearCommand(fixed string) *cobra.Command {
	var jsonOutput bool
	use := "clear <provider_id>"
	args := cobra.ExactArgs(1)
	if fixed != "" {
		use = "clear"
		args = cobra.NoArgs
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: "Clear an LLM provider API key",
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := fixed
			if id == "" {
				id = args[0]
			}
			service := llmService()
			if err := service.ClearCredential(cmd.Context(), id); err != nil {
				return err
			}
			result, err := service.CredentialResult(cmd.Context(), id)
			if err != nil {
				return err
			}
			return renderLLMCredentialResult(cmd, result, "LLM provider credential cleared")
		},
	}
	if fixed == "" {
		cmd.ValidArgsFunction = completeLLMProviderIDs
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmCoreProviderCommand(name, id string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "Manage the " + name + " LLM provider"}
	key := &cobra.Command{Use: "key", Short: "Manage the provider API key"}
	key.AddCommand(llmCredentialSetCommand(id), llmCredentialClearCommand(id))
	cmd.AddCommand(
		llmProviderShowCommand(id, "status"),
		llmUseCommandForProvider(id),
		llmProviderModelCommand(id),
		key,
	)
	if id == "openrouter" {
		cmd.AddCommand(llmOpenRouterModelsCommand())
	} else {
		cmd.AddCommand(llmModelsCommandForProvider(id), llmOllamaModeCommand())
	}
	return cmd
}

func llmProviderArgumentOrActive(cmd *cobra.Command, args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return args[0], nil
	}
	status, err := llmService().Status(cmd.Context())
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(status.ActiveProvider)) == "" {
		return "", errors.New("active LLM provider is unavailable")
	}
	return string(status.ActiveProvider), nil
}

func llmProviderModelCommand(id string) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "model <model_id>",
		Short: "Set the provider model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := llmService().SetProviderModel(cmd.Context(), id, args[0])
			if err != nil {
				return err
			}
			return renderLLMProviderMutation(cmd, result, "LLM provider model updated")
		},
		ValidArgsFunction: func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			prepareCompletionConfigRoot(cmd)
			provider, err := llmService().Provider(cmd.Context(), id)
			if err != nil || strings.TrimSpace(provider.Model) == "" {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return filterCompletions([]string{provider.Model}, toComplete), cobra.ShellCompDirectiveNoFileComp
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmOpenRouterModelsCommand() *cobra.Command {
	var freeOnly, jsonOutput bool
	var search string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List OpenRouter models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := llmService().OpenRouterModels(cmd.Context(), application.LLMModelQuery{Search: search, FreeOnly: freeOnly, Limit: 200})
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result)
			}
			renderOpenRouterModels(cmd, result)
			return nil
		},
	}
	cmd.Flags().BoolVar(&freeOnly, "free", false, "Show only free models")
	cmd.Flags().StringVar(&search, "search", "", "Filter models by ID or name")
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmOllamaModeCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:               "mode <local|cloud>",
		Short:             "Switch Ollama between local and Cloud defaults",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeStatic("local", "cloud"),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := llmService().SetOllamaModeValue(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return renderLLMProviderMutation(cmd, result, "Ollama mode updated")
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func llmUseCommandForProvider(id string) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "use",
		Short: "Select this LLM provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := llmService()
			if _, err := service.SelectProvider(cmd.Context(), id); err != nil {
				return err
			}
			result, err := service.ProviderResult(cmd.Context(), id)
			if err != nil {
				return err
			}
			return renderLLMProviderMutation(cmd, result, "LLM provider selected")
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

func completeLLMProviderIDs(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return llmProviderCompletions(cmd, toComplete, false)
}

func completeCustomLLMProviderIDs(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return llmProviderCompletions(cmd, toComplete, true)
}

func llmProviderCompletions(cmd *cobra.Command, prefix string, customOnly bool) ([]string, cobra.ShellCompDirective) {
	prepareCompletionConfigRoot(cmd)
	providers, err := llmService().Providers(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	values := make([]string, 0, len(providers))
	for _, provider := range providers {
		if customOnly && provider.Core {
			continue
		}
		values = append(values, string(provider.ID))
	}
	sort.Strings(values)
	return filterCompletions(values, prefix), cobra.ShellCompDirectiveNoFileComp
}

func completeConfiguredLLMModel(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prepareCompletionConfigRoot(cmd)
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	provider, err := llmService().Provider(cmd.Context(), args[0])
	if err != nil || strings.TrimSpace(provider.Model) == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return filterCompletions([]string{provider.Model}, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func readProtectedLLMInput(cmd *cobra.Command, label, fromEnv string) (string, error) {
	fromEnv = strings.TrimSpace(fromEnv)
	if fromEnv != "" {
		value, ok := os.LookupEnv(fromEnv)
		if !ok {
			return "", fmt.Errorf("environment variable %q is not set", fromEnv)
		}
		if len(value) > maxProtectedLLMInputBytes {
			return "", fmt.Errorf("environment variable %q exceeds size limit", fromEnv)
		}
		secret := strings.TrimSpace(value)
		if secret == "" {
			return "", fmt.Errorf("environment variable %q is empty", fromEnv)
		}
		return secret, nil
	}
	input := cmd.InOrStdin()
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		var value []byte
		err := commandProgressSession(cmd).WithInput(func(presenter *presentation.Presenter) {
			presenter.Prompt(label)
		}, func() error {
			var readErr error
			value, readErr = term.ReadPassword(int(file.Fd()))
			return readErr
		})
		if err != nil {
			return "", errors.New("read protected input")
		}
		if len(value) > maxProtectedLLMInputBytes {
			return "", errors.New("protected input exceeds size limit")
		}
		secret := strings.TrimSpace(string(value))
		for i := range value {
			value[i] = 0
		}
		if secret == "" {
			return "", errors.New("API key is required")
		}
		return secret, nil
	}
	reader := bufio.NewReader(io.LimitReader(input, maxProtectedLLMInputBytes+2))
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("read protected input")
	}
	if len(value) > maxProtectedLLMInputBytes {
		return "", errors.New("protected input exceeds size limit")
	}
	secret := strings.TrimSpace(value)
	if secret == "" {
		return "", errors.New("API key is required")
	}
	return secret, nil
}

func zeroString(value *string) {
	if value != nil {
		*value = ""
	}
}

func renderLLMStatus(cmd *cobra.Command, result application.LLMStatusResult) {
	presenter := commandPresenter(cmd)
	presenter.Frame("LLM")
	presenter.Fields(
		presentation.Field{Label: "active provider", Value: result.ActiveProvider},
		presentation.Field{Label: "configured", Value: result.Active.Configured},
		presentation.Field{Label: "readiness", Value: result.Active.Readiness},
		presentation.Field{Label: "model", Value: result.Active.Model},
	)
	if result.Active.Reason != "" {
		presenter.Fields(presentation.Field{Label: "reason", Value: result.Active.Reason})
	}
	rows := make([]presentation.Row, 0, len(result.Providers))
	for _, provider := range result.Providers {
		selected := ""
		if provider.Selected {
			selected = "*"
		}
		rows = append(rows, presentation.Row{string(provider.ID), selected, string(provider.Protocol), provider.Model, string(provider.Readiness), fmt.Sprint(provider.Configured)})
	}
	presenter.Section(fmt.Sprintf("Providers · %d", len(rows)))
	presenter.Rows([]string{"ID", "Active", "Protocol", "Model", "Readiness", "Configured"}, rows...)
	presenter.Complete("Done")
}

func renderLLMProviderList(cmd *cobra.Command, providers []application.LLMProviderResult) {
	presenter := commandPresenter(cmd)
	presenter.Frame("LLM providers")
	rows := make([]presentation.Row, 0, len(providers))
	for _, provider := range providers {
		selected := ""
		if provider.Selected {
			selected = "*"
		}
		rows = append(rows, presentation.Row{string(provider.ID), provider.Name, selected, string(provider.Protocol), provider.Model, string(provider.Readiness)})
	}
	presenter.Rows([]string{"ID", "Name", "Active", "Protocol", "Model", "Readiness"}, rows...)
	presenter.Complete("Done")
}

func renderLLMProvider(cmd *cobra.Command, provider application.LLMProviderResult) {
	presenter := commandPresenter(cmd)
	presenter.Frame("LLM provider")
	presenter.Subsection(string(provider.ID))
	presenter.NestedFields(llmProviderFields(provider)...)
	presenter.Complete("Done")
}

func llmProviderFields(provider application.LLMProviderResult) []presentation.Field {
	fields := []presentation.Field{
		{Label: "name", Value: provider.Name}, {Label: "protocol", Value: provider.Protocol}, {Label: "base URL", Value: provider.BaseURL},
		{Label: "model", Value: provider.Model}, {Label: "auth", Value: provider.AuthMode}, {Label: "discovery", Value: provider.Discovery},
		{Label: "core", Value: provider.Core}, {Label: "active", Value: provider.Selected}, {Label: "configured", Value: provider.Configured},
		{Label: "readiness", Value: provider.Readiness}, {Label: "API key configured", Value: provider.Credential.Configured},
	}
	if provider.Credential.Preview != "" {
		fields = append(fields, presentation.Field{Label: "API key", Value: provider.Credential.Preview})
	}
	if provider.Reason != "" {
		fields = append(fields, presentation.Field{Label: "reason", Value: provider.Reason})
	}
	return fields
}

func renderLLMProviderMutation(cmd *cobra.Command, provider application.LLMProviderResult, message string) error {
	if commandResultModeFor(cmd) == resultModeJSON {
		return writeResultJSON(cmd, provider)
	}
	renderEntityMutationSuccess(cmd, message, string(provider.ID), llmProviderFields(provider)...)
	return nil
}

func renderLLMCredentialResult(cmd *cobra.Command, result application.LLMCredentialResult, message string) error {
	if commandResultModeFor(cmd) == resultModeJSON {
		return writeResultJSON(cmd, result)
	}
	fields := []presentation.Field{{Label: "configured", Value: result.Configured}}
	if result.Preview != "" {
		fields = append(fields, presentation.Field{Label: "API key", Value: result.Preview})
	}
	renderEntityMutationSuccess(cmd, message, string(result.ProviderID), fields...)
	return nil
}

func renderLLMModels(cmd *cobra.Command, result application.LLMModelCatalogResult) {
	presenter := commandPresenter(cmd)
	presenter.Frame("LLM models")
	presenter.Fields(presentation.Field{Label: "provider", Value: result.ProviderID}, presentation.Field{Label: "refreshed", Value: result.Refreshed})
	rows := make([]presentation.Row, 0, len(result.Models))
	for _, model := range result.Models {
		rows = append(rows, presentation.Row{model.ID, model.Name, fmt.Sprint(model.ContextLength), fmt.Sprint(model.Free), fmt.Sprint(model.SupportsStructuredOutput)})
	}
	presenter.Section(fmt.Sprintf("Models · %d", len(rows)))
	presenter.Rows([]string{"ID", "Name", "Context", "Free", "Structured"}, rows...)
	presenter.Complete("Done")
}

func renderOpenRouterModels(cmd *cobra.Command, result application.LLMModelPage) {
	presenter := commandPresenter(cmd)
	presenter.Frame("OpenRouter models")
	presenter.Fields(
		presentation.Field{Label: "provider", Value: result.ProviderID},
		presentation.Field{Label: "total", Value: result.Total},
		presentation.Field{Label: "truncated", Value: result.Truncated},
	)
	rows := make([]presentation.Row, 0, len(result.Models))
	for _, model := range result.Models {
		rows = append(rows, presentation.Row{model.ID, model.Name, fmt.Sprint(model.ContextLength), fmt.Sprint(model.Free), fmt.Sprint(model.SupportsStructuredOutput)})
	}
	presenter.Section(fmt.Sprintf("Models · %d", len(rows)))
	presenter.Rows([]string{"ID", "Name", "Context", "Free", "Structured"}, rows...)
	presenter.Complete("Done")
}
