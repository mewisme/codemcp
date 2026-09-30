package cli

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
)

func llmCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "llm", Short: "Manage LLM providers and models"}
	cmd.AddCommand(
		llmStatusCommand(),
		llmUseCommand(),
		llmModelsCommand(),
		llmProbeCommand(),
		llmProviderCommand(),
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
	var flags llmModelQueryFlags
	var jsonOutput bool
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
			query, err := flags.query(cmd)
			if err != nil {
				return err
			}
			result, err := llmService().ModelCatalog(cmd.Context(), id, query)
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
	addLLMModelQueryFlags(cmd, &flags)
	addJSONResultFlag(cmd, &jsonOutput)
	return cmd
}

type llmModelQueryFlags struct {
	search, minPromptPrice, maxPromptPrice, minCompletionPrice, maxCompletionPrice string
	rank, window, recommendFor, rangeValue                                         string
	ids, authors, capabilities, parameters, inputs, outputs                        []string
	families, formats, quantizations, sorts                                        []string
	free, paid, all, count, refresh                                                bool
	minContext, maxContext, offset, limit                                          int
	minSize, maxSize, minParameters, maxParameters                                 int64
	createdAfter, createdBefore, modifiedAfter, modifiedBefore                     string
}

func addLLMModelQueryFlags(cmd *cobra.Command, flags *llmModelQueryFlags) {
	values := cmd.Flags()
	values.StringVar(&flags.search, "search", "", "Search model ID, name or author")
	values.StringArrayVar(&flags.ids, "id", nil, "Filter by exact model ID (repeatable)")
	values.StringArrayVar(&flags.authors, "author", nil, "Filter by model author/vendor (repeatable)")
	values.BoolVar(&flags.free, "free", false, "Show only models known to be free")
	values.BoolVar(&flags.paid, "paid", false, "Show only models known to be paid")
	values.IntVar(&flags.minContext, "min-context", 0, "Minimum context length")
	values.IntVar(&flags.maxContext, "max-context", 0, "Maximum context length")
	values.StringVar(&flags.minPromptPrice, "min-prompt-price", "", "Minimum prompt token price")
	values.StringVar(&flags.maxPromptPrice, "max-prompt-price", "", "Maximum prompt token price")
	values.StringVar(&flags.minCompletionPrice, "min-completion-price", "", "Minimum completion token price")
	values.StringVar(&flags.maxCompletionPrice, "max-completion-price", "", "Maximum completion token price")
	values.StringArrayVar(&flags.capabilities, "capability", nil, "Filter by capability (repeatable)")
	values.StringArrayVar(&flags.parameters, "parameter", nil, "Filter by supported parameter (repeatable)")
	values.StringArrayVar(&flags.inputs, "input", nil, "Filter by input modality (repeatable)")
	values.StringArrayVar(&flags.outputs, "output", nil, "Filter by output modality (repeatable)")
	values.StringArrayVar(&flags.families, "family", nil, "Filter Ollama family/families (repeatable)")
	values.StringArrayVar(&flags.formats, "format", nil, "Filter Ollama format (repeatable)")
	values.StringArrayVar(&flags.quantizations, "quantization", nil, "Filter Ollama quantization level (repeatable)")
	values.Int64Var(&flags.minParameters, "min-parameters", 0, "Minimum parsed Ollama parameter count")
	values.Int64Var(&flags.maxParameters, "max-parameters", 0, "Maximum parsed Ollama parameter count")
	values.Int64Var(&flags.minSize, "min-size", 0, "Minimum Ollama model size in bytes")
	values.Int64Var(&flags.maxSize, "max-size", 0, "Maximum Ollama model size in bytes")
	values.StringVar(&flags.createdAfter, "created-after", "", "Minimum model creation time (RFC3339)")
	values.StringVar(&flags.createdBefore, "created-before", "", "Maximum model creation time (RFC3339)")
	values.StringVar(&flags.modifiedAfter, "modified-after", "", "Minimum model modification time (RFC3339)")
	values.StringVar(&flags.modifiedBefore, "modified-before", "", "Maximum model modification time (RFC3339)")
	values.StringArrayVar(&flags.sorts, "sort", nil, "Sort by field[:asc|desc] (repeatable)")
	values.StringVar(&flags.rank, "rank", "", "Rank models when the selected provider exposes ranking metadata")
	values.StringVar(&flags.window, "window", "", "Usage rank window: day, week or month")
	values.StringVar(&flags.recommendFor, "recommend-for", "", "Rank models for an explicit task classification when supported")
	values.IntVar(&flags.offset, "offset", 0, "Zero-based result offset")
	values.IntVar(&flags.limit, "limit", 0, "Maximum models to return")
	values.StringVar(&flags.rangeValue, "range", "", "1-based inclusive result range start:end")
	values.BoolVar(&flags.count, "count", false, "Return only the matched model count")
	values.BoolVar(&flags.all, "all", false, "Return all matched models from the bounded provider catalog")
	values.BoolVar(&flags.refresh, "refresh", false, "Refresh the remote model catalog")
	_ = cmd.RegisterFlagCompletionFunc("sort", completeStaticFlag(
		"id:asc", "id:desc", "name:asc", "name:desc", "context:asc", "context:desc",
		"prompt-price:asc", "prompt-price:desc", "completion-price:asc", "completion-price:desc",
		"created:asc", "created:desc", "modified:asc", "modified:desc", "size:asc", "size:desc",
		"parameter-size:asc", "parameter-size:desc",
	))
	_ = cmd.RegisterFlagCompletionFunc("rank", completeStaticFlag("usage", "trending", "intelligence", "coding", "agentic"))
	_ = cmd.RegisterFlagCompletionFunc("window", completeStaticFlag("day", "week", "month"))
	_ = cmd.RegisterFlagCompletionFunc("capability", completeStaticFlag("structured-output", "tools", "reasoning", "web-search"))
	_ = cmd.RegisterFlagCompletionFunc("parameter", completeStaticFlag("tools", "tool_choice", "reasoning", "structured_outputs", "response_format", "web_search"))
	_ = cmd.RegisterFlagCompletionFunc("input", completeStaticFlag("text", "image", "audio", "video", "file"))
	_ = cmd.RegisterFlagCompletionFunc("output", completeStaticFlag("text", "image", "audio"))
}

func completeStaticFlag(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func (flags llmModelQueryFlags) query(cmd *cobra.Command) (application.LLMModelQuery, error) {
	if flags.free && flags.paid {
		return application.LLMModelQuery{}, errors.New("--free and --paid are mutually exclusive")
	}
	query := application.LLMModelQuery{
		Search: flags.search, ExactIDs: flags.ids, Authors: flags.authors,
		MinPromptPrice: flags.minPromptPrice, MaxPromptPrice: flags.maxPromptPrice,
		MinCompletionPrice: flags.minCompletionPrice, MaxCompletionPrice: flags.maxCompletionPrice,
		Capabilities: flags.capabilities, Parameters: flags.parameters, InputModalities: flags.inputs, OutputModalities: flags.outputs,
		Ollama: application.LLMOllamaModelQuery{Families: flags.families, Formats: flags.formats, Quantizations: flags.quantizations},
		Rank:   flags.rank, RankWindow: flags.window, RecommendFor: flags.recommendFor,
		Offset: flags.offset, Limit: flags.limit, All: flags.all, CountOnly: flags.count, Refresh: flags.refresh,
	}
	if flags.free || flags.paid {
		free := flags.free
		query.Free = &free
	}
	if cmd.Flags().Changed("min-context") {
		value := flags.minContext
		query.MinContext = &value
	}
	if cmd.Flags().Changed("max-context") {
		value := flags.maxContext
		query.MaxContext = &value
	}
	if cmd.Flags().Changed("min-parameters") {
		value := flags.minParameters
		query.Ollama.MinParameterCount = &value
	}
	if cmd.Flags().Changed("max-parameters") {
		value := flags.maxParameters
		query.Ollama.MaxParameterCount = &value
	}
	if cmd.Flags().Changed("min-size") {
		value := flags.minSize
		query.Ollama.MinSizeBytes = &value
	}
	if cmd.Flags().Changed("max-size") {
		value := flags.maxSize
		query.Ollama.MaxSizeBytes = &value
	}
	for _, raw := range flags.sorts {
		value, err := application.ParseLLMModelSort(raw)
		if err != nil {
			return application.LLMModelQuery{}, err
		}
		query.Sort = append(query.Sort, value)
	}
	if strings.TrimSpace(flags.rangeValue) != "" {
		value, err := application.ParseLLMModelRange(flags.rangeValue)
		if err != nil {
			return application.LLMModelQuery{}, err
		}
		query.Range = value
	}
	for _, target := range []struct {
		raw string
		set func(*time.Time)
	}{
		{flags.createdAfter, func(value *time.Time) { query.CreatedAfter = value }},
		{flags.createdBefore, func(value *time.Time) { query.CreatedBefore = value }},
		{flags.modifiedAfter, func(value *time.Time) { query.ModifiedAfter = value }},
		{flags.modifiedBefore, func(value *time.Time) { query.ModifiedBefore = value }},
	} {
		if strings.TrimSpace(target.raw) == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, strings.TrimSpace(target.raw))
		if err != nil {
			return application.LLMModelQuery{}, fmt.Errorf("invalid RFC3339 model time %q", target.raw)
		}
		value = value.UTC()
		target.set(&value)
	}
	return query, nil
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
	var confirm bool
	cmd := &cobra.Command{
		Use:               "remove <provider_id>",
		Short:             "Remove a custom LLM provider",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCustomLLMProviderIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDestructiveConfirmation(confirm, "LLM provider removal"); err != nil {
				return err
			}
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
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm removal of the custom LLM provider")
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
	use := "set <provider_id> [api-key]"
	args := cobra.RangeArgs(1, 2)
	if fixed != "" {
		use = "set [api-key]"
		args = cobra.MaximumNArgs(1)
	}
	cmd := &cobra.Command{
		Use: use, Short: "Set an LLM provider API key",
		Long: "Set an LLM provider API key.\n\n" + protectedArgumentWarning,
		Args: args,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := fixed
			secretIndex := 0
			if id == "" {
				id = args[0]
				secretIndex = 1
			}
			explicitSet := len(args) > secretIndex
			explicit := ""
			if explicitSet {
				explicit = args[secretIndex]
			}
			secret, err := readProtectedInput(cmd, protectedInputOptions{Label: "API key", Explicit: explicit, ExplicitSet: explicitSet, FromEnv: fromEnv})
			if err != nil {
				return err
			}
			defer zeroProtectedString(&secret)
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
	cmd.AddCommand(llmModelsCommandForProvider(id))
	if id == "ollama" {
		cmd.AddCommand(llmOllamaModeCommand())
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
	presenter.Fields(
		presentation.Field{Label: "provider", Value: result.ProviderID},
		presentation.Field{Label: "catalog", Value: result.TotalCatalog},
		presentation.Field{Label: "matched", Value: result.Matched},
		presentation.Field{Label: "returned", Value: result.Returned},
		presentation.Field{Label: "offset", Value: result.Offset},
		presentation.Field{Label: "limit", Value: result.Limit},
		presentation.Field{Label: "has more", Value: result.HasMore},
		presentation.Field{Label: "refreshed", Value: result.Refreshed},
	)
	if result.RankSource != "" {
		presenter.Fields(presentation.Field{Label: "rank source", Value: result.RankSource}, presentation.Field{Label: "rank window", Value: result.RankWindow}, presentation.Field{Label: "rank basis", Value: result.RankBasis})
	}
	if result.RecommendationBasis != "" {
		presenter.Fields(presentation.Field{Label: "recommendation source", Value: result.RecommendationSource}, presentation.Field{Label: "recommendation basis", Value: result.RecommendationBasis})
	}
	if len(result.Models) == 0 {
		presenter.Complete("Done")
		return
	}
	rows := make([]presentation.Row, 0, len(result.Models))
	for _, model := range result.Models {
		contextValue, freeValue := "-", "-"
		if model.ContextLengthKnown {
			contextValue = strconv.Itoa(model.ContextLength)
		}
		if model.FreeKnown {
			freeValue = strconv.FormatBool(model.Free)
		}
		rank := ""
		if model.Rank != nil {
			rank = fmt.Sprintf("#%d %s", model.Rank.Position, model.Rank.Kind)
		}
		if model.Recommendation != nil {
			rank = fmt.Sprintf("#%d recommend", model.Recommendation.Position)
		}
		rows = append(rows, presentation.Row{model.ID, model.Name, contextValue, freeValue, model.PromptPrice, model.CompletionPrice, rank})
	}
	presenter.Section(fmt.Sprintf("Models · %d", len(rows)))
	presenter.Rows([]string{"ID", "Name", "Context", "Free", "Prompt", "Completion", "Rank"}, rows...)
	presenter.Complete("Done")
}
