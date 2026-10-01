package cli

import (
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	"go.mewis.me/codemcp/internal/workspace"
)

func completeConfigSelection(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeConfigSettingSelection(cmd, args, toComplete, false)
}

func completeConfigWhySelection(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeConfigSettingSelection(cmd, args, toComplete, true)
}

func completeConfigSettingSelection(cmd *cobra.Command, args []string, toComplete string, includeWriteOnly bool) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]string{}
	for _, spec := range configPresentationSpecs() {
		if spec.InternalOnly || (!includeWriteOnly && !spec.Readable && !spec.Secret) {
			continue
		}
		if spec.Selector != nil {
			for _, key := range completeDynamicSettingKeys(cmd, spec) {
				seen[key] = spec.Description
			}
			continue
		}
		seen[spec.Key] = spec.Description
		parts := strings.Split(spec.Key, ".")
		for index := 1; index < len(parts); index++ {
			prefix := strings.Join(parts[:index], ".")
			if _, ok := seen[prefix]; !ok {
				seen[prefix] = "configuration subtree"
			}
		}
	}
	values := make([]string, 0, len(seen))
	for key, description := range seen {
		values = append(values, key+"\t"+description)
	}
	sort.Strings(values)
	return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func configPresentationSpecs() []config.FieldSpec {
	configuredKeys := map[string]struct{}{}
	settings := config.Settings()
	for _, spec := range settings {
		if spec.Secret && spec.ConfiguredStateKey != "" {
			configuredKeys[spec.ConfiguredStateKey] = struct{}{}
		}
	}
	result := make([]config.FieldSpec, 0, len(settings))
	for _, spec := range settings {
		if _, surrogate := configuredKeys[spec.Key]; surrogate {
			continue
		}
		result = append(result, spec)
	}
	return result
}

func completeConfigSet(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeConfigOperation(cmd, toComplete, func(spec config.FieldSpec) bool { return spec.Writable })
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	key := args[0]
	spec, ok := config.SettingByKey(key)
	if !ok {
		if match, matched := config.MatchSettingSelector(key); matched {
			spec, ok = match.Spec, true
		}
	}
	if ok && spec.Kind == config.FieldEnum {
		return filterCompletions(spec.Options, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	if ok && spec.Kind == config.FieldBool {
		return filterCompletions([]string{"true", "false"}, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	switch key {
	case "http.exposure":
		return filterCompletions([]string{"none", "all", "0.0.0.0"}, toComplete), cobra.ShellCompDirectiveNoFileComp
	case "http.exposure.interfaces":
		interfaces, err := net.Interfaces()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		values := make([]string, 0, len(interfaces))
		for _, item := range interfaces {
			values = append(values, item.Name)
		}
		sort.Strings(values)
		return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	case "permissions.allow_dirs", "shell.path":
		return nil, cobra.ShellCompDirectiveFilterDirs
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeConfigUnset(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeConfigOperation(cmd, toComplete, func(spec config.FieldSpec) bool { return spec.Clearable || spec.DefaultReset })
}

func completeConfigRotate(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeConfigOperation(cmd, toComplete, func(spec config.FieldSpec) bool { return spec.Rotatable })
}

func completeConfigReveal(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeConfigOperation(cmd, toComplete, func(spec config.FieldSpec) bool { return spec.Revealable })
}

func completeConfigVerify(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeConfigOperation(cmd, toComplete, func(spec config.FieldSpec) bool { return spec.Verifiable })
}

func completeConfigOperation(cmd *cobra.Command, toComplete string, include func(config.FieldSpec) bool) ([]string, cobra.ShellCompDirective) {
	values := make([]string, 0)
	for _, spec := range config.Settings() {
		if spec.InternalOnly || !include(spec) {
			continue
		}
		if spec.Selector == nil {
			values = append(values, spec.Key+"\t"+spec.Description)
			continue
		}
		for _, key := range completeDynamicSettingKeys(cmd, spec) {
			values = append(values, key+"\t"+spec.Description)
		}
	}
	sort.Strings(values)
	return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeDynamicSettingKeys(cmd *cobra.Command, spec config.FieldSpec) []string {
	if cmd == nil || spec.Selector == nil {
		return nil
	}
	prepareCompletionConfigRoot(cmd)
	ctx := cmd.Context()
	ids := make([]string, 0)
	switch spec.Selector.Resource {
	case "upstream.server":
		service, err := application.LoadUpstreamService(ctx)
		if err != nil {
			return nil
		}
		result, err := service.List(ctx)
		if err != nil {
			return nil
		}
		for _, item := range result.Value {
			ids = append(ids, item.ID)
		}
	case "tunnel.managed":
		items, err := application.ListManagedTunnels(ctx)
		if err != nil {
			return nil
		}
		for _, item := range items {
			ids = append(ids, item.ID)
		}
	}
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, strings.Replace(spec.Key, "<id>", url.PathEscape(id), 1))
	}
	return values
}

func completeWorkspaceID(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return workspaceCompletions(cmd, toComplete)
}

func completeWorkspaceContainerID(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return workspaceContainerCompletions(cmd, toComplete)
}

func completeWorkspaceContainerThenName(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return workspaceContainerCompletions(cmd, toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func completeWorkspaceContainerThenWorkspaces(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return workspaceContainerCompletions(cmd, toComplete)
	}
	return workspaceCompletions(cmd, toComplete)
}

func completeWorkspaceThenDirectory(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return workspaceCompletions(cmd, toComplete)
	}
	if len(args) == 1 {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func completeDirectory(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveFilterDirs
}

func workspaceCompletions(cmd *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	prepareCompletionConfigRoot(cmd)
	items, err := workspace.NewManager(workspace.DefaultStorePath()).List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, item.ID+"\t"+item.Path)
	}
	return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func workspaceContainerCompletions(cmd *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	prepareCompletionConfigRoot(cmd)
	items, err := workspace.NewManager(workspace.DefaultStorePath()).ListContainers()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, item.ID+"\t"+item.Name)
	}
	return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeUpstreamID(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	prepareCompletionConfigRoot(cmd)
	manager, err := loadUpstreamManager()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	servers := manager.List()
	values := make([]string, 0, len(servers))
	for _, server := range servers {
		description := server.Name
		if strings.TrimSpace(description) == "" {
			description = server.Transport
		}
		values = append(values, server.ID+"\t"+description)
	}
	return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeSessionID(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prepareCompletionConfigRoot(cmd)
	events, err := runtimeevent.Read(config.RootPath(), runtimeevent.Query{Tail: 500})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	values := []string{}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.RunID == "" || seen[event.RunID] {
			continue
		}
		seen[event.RunID] = true
		description := event.Time.Local().Format("2006-01-02 15:04:05")
		if event.Managed {
			description += " managed/" + event.ServiceScope
		} else {
			description += " foreground"
		}
		values = append(values, event.RunID+"\t"+description)
		if len(values) >= 20 {
			break
		}
	}
	return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeStatic(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterCompletions(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func prepareCompletionConfigRoot(cmd *cobra.Command) {
	_ = configureConfigDir(cmd)
}

func filterCompletions(values []string, prefix string) []string {
	if prefix == "" {
		return values
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		candidate, _, _ := strings.Cut(value, "\t")
		if strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix)) {
			result = append(result, value)
		}
	}
	return result
}
