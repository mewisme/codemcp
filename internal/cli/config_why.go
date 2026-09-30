package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type configWhyEntry struct {
	Spec        config.FieldSpec
	Baseline    string
	HasBaseline bool
}

func configWhyCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "why [key]",
		Short: "Describe canonical setting ownership, lifecycle, and baseline",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := ""
			if len(args) > 0 {
				key = strings.TrimSpace(args[0])
			}
			span := tracepkg.Start(cmd.Context(), "CONFIG", "config.why.lookup", "Looking up canonical setting metadata", tracepkg.String("key", key), tracepkg.Bool("root", key == ""))
			entries, err := configWhyEntries(cmd, key)
			if err != nil {
				span.FailMessage("Canonical setting metadata lookup failed", err, tracepkg.String("key", key))
				return err
			}
			span.EndMessage("Canonical setting metadata resolved", tracepkg.String("key", key), tracepkg.Int("entry_count", len(entries)))
			if jsonOutput {
				return writeResultJSON(cmd, configWhyJSONEntries(entries))
			}

			markdown := configWhyMarkdown(entries)
			capabilities := commandTerminalCapabilities(cmd)
			renderSpan := tracepkg.Start(
				cmd.Context(),
				"CONFIG",
				"config.why.render",
				"Rendering canonical setting metadata",
				tracepkg.String("mode", configWhyRenderMode(cmd)),
				tracepkg.String("key", key),
				tracepkg.Int("entry_count", len(entries)),
				tracepkg.Int("markdown_bytes", len(markdown)),
				tracepkg.Int("terminal_width", capabilities.Width),
				tracepkg.Bool("terminal", capabilities.Interactive),
			)
			presenter := commandPresenter(cmd)
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter.Frame("Config why")
			}
			if err := presenter.Markdown(markdown); err != nil {
				renderSpan.FailMessage("Canonical setting metadata render failed", err, tracepkg.String("key", key))
				return err
			}
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter.Complete("Done")
			}
			renderSpan.EndMessage(
				"Canonical setting metadata rendered",
				tracepkg.String("mode", configWhyRenderMode(cmd)),
				tracepkg.String("key", key),
				tracepkg.Int("entry_count", len(entries)),
				tracepkg.Int("markdown_bytes", len(markdown)),
				tracepkg.Int("terminal_width", capabilities.Width),
				tracepkg.Bool("terminal", capabilities.Interactive),
			)
			return nil
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	cmd.ValidArgsFunction = completeConfigWhySelection
	return cmd
}

func configDiffCommand() *cobra.Command {
	options := configOutputOptions{}
	cmd := &cobra.Command{
		Use:   "diff [prefix]",
		Short: "Show settings that differ from their canonical baseline",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) > 0 {
				prefix = strings.TrimSpace(args[0])
			}
			return printSettingDiff(cmd, application.NewSettingService(), prefix, options)
		},
	}
	addConfigOutputFlags(cmd, &options)
	cmd.ValidArgsFunction = completeConfigSelection
	return cmd
}

func configWhyEntries(cmd *cobra.Command, key string) ([]configWhyEntry, error) {
	service := application.NewSettingService()
	if key != "" {
		if _, ok := config.SettingByKey(key); ok {
			entry, err := service.Why(cmd.Context(), key)
			if err != nil {
				return nil, err
			}
			return []configWhyEntry{{Spec: entry.Spec, Baseline: entry.Baseline, HasBaseline: entry.HasBaseline}}, nil
		}
		if _, ok := config.MatchSettingSelector(key); ok {
			entry, err := service.Why(cmd.Context(), key)
			if err != nil {
				return nil, err
			}
			return []configWhyEntry{{Spec: entry.Spec, Baseline: entry.Baseline, HasBaseline: entry.HasBaseline}}, nil
		}
	}

	specs := config.Settings()
	if key != "" {
		specs = config.SettingsByPrefix(key)
		if len(specs) == 0 {
			return nil, fmt.Errorf("unsupported setting: %s", key)
		}
	}
	entries := make([]configWhyEntry, 0, len(specs))
	for _, spec := range specs {
		if spec.InternalOnly {
			continue
		}
		entry := configWhyEntry{Spec: spec}
		if spec.Selector == nil {
			why, err := service.Why(cmd.Context(), spec.Key)
			if err != nil {
				return nil, err
			}
			entry.Baseline, entry.HasBaseline = why.Baseline, why.HasBaseline
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func configWhyJSONEntries(entries []configWhyEntry) []map[string]any {
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		spec := entry.Spec
		item := map[string]any{
			"key":               spec.Key,
			"label":             spec.Label,
			"description":       spec.Description,
			"type":              spec.Kind,
			"value_role":        spec.ValueRole,
			"domain":            spec.Domain,
			"application_owner": spec.ApplicationOwner,
			"readable":          spec.Readable,
			"writable":          spec.Writable,
			"secret":            spec.Secret,
			"clearable":         spec.Clearable,
			"default_reset":     spec.DefaultReset,
			"rotatable":         spec.Rotatable,
			"revealable":        spec.Revealable,
			"verifiable":        spec.Verifiable,
			"accepts":           config.AcceptedValueHint(spec),
		}
		if entry.HasBaseline {
			item["baseline"] = entry.Baseline
		}
		if spec.ConfiguredStateKey != "" {
			item["configured_state_key"] = spec.ConfiguredStateKey
		}
		if len(spec.Options) > 0 {
			item["options"] = append([]string(nil), spec.Options...)
		}
		if len(spec.ScopedCommands) > 0 {
			item["scoped_commands"] = append([]string(nil), spec.ScopedCommands...)
		}
		if spec.ScopedExemption != "" {
			item["scoped_exemption"] = spec.ScopedExemption
		}
		if spec.Guidance != "" {
			item["guidance"] = spec.Guidance
		}
		if len(spec.Related) > 0 {
			item["related"] = append([]string(nil), spec.Related...)
		}
		if spec.Selector != nil {
			item["selector"] = map[string]any{
				"template":        spec.Selector.Template,
				"resource":        spec.Selector.Resource,
				"inventory_owner": spec.Selector.InventoryOwner,
				"id_encoding":     spec.Selector.IDEncoding,
			}
		}
		result = append(result, item)
	}
	return result
}

func configWhyMarkdown(entries []configWhyEntry) string {
	var builder strings.Builder
	for index, entry := range entries {
		spec := entry.Spec
		if index > 0 {
			builder.WriteString("\n")
		}
		fmt.Fprintf(&builder, "# %s\n\n", spec.Key)
		if spec.Label != "" {
			fmt.Fprintf(&builder, "**%s**\n\n", spec.Label)
		}
		if spec.Description != "" {
			builder.WriteString(spec.Description + "\n\n")
		}
		fmt.Fprintf(&builder, "- **Type:** `%s`\n", spec.Kind)
		fmt.Fprintf(&builder, "- **Value role:** `%s`\n", spec.ValueRole)
		fmt.Fprintf(&builder, "- **Domain:** `%s`\n", spec.Domain)
		fmt.Fprintf(&builder, "- **Application owner:** `%s`\n", spec.ApplicationOwner)
		fmt.Fprintf(&builder, "- **Readable:** `%t`\n", spec.Readable)
		fmt.Fprintf(&builder, "- **Writable:** `%t`\n", spec.Writable)
		if accepts := config.AcceptedValueHint(spec); accepts != "" {
			fmt.Fprintf(&builder, "- **Accepts:** `%s`\n", accepts)
		}
		if spec.Secret {
			builder.WriteString("- **Secret:** `true` (write-only)\n")
		}
		if spec.ConfiguredStateKey != "" {
			fmt.Fprintf(&builder, "- **Configured-state key:** `%s`\n", spec.ConfiguredStateKey)
		}
		if entry.HasBaseline {
			fmt.Fprintf(&builder, "- **Baseline:** `%s`\n", entry.Baseline)
		}
		lifecycle := make([]string, 0, 5)
		if spec.Clearable {
			lifecycle = append(lifecycle, "unset")
		}
		if spec.DefaultReset {
			lifecycle = append(lifecycle, "default-reset")
		}
		if spec.Rotatable {
			lifecycle = append(lifecycle, "rotate")
		}
		if spec.Revealable {
			lifecycle = append(lifecycle, "reveal")
		}
		if spec.Verifiable {
			lifecycle = append(lifecycle, "verify")
		}
		if len(lifecycle) > 0 {
			fmt.Fprintf(&builder, "- **Lifecycle:** `%s`\n", strings.Join(lifecycle, ", "))
		}
		if len(spec.Options) > 0 {
			fmt.Fprintf(&builder, "- **Values:** `%s`\n", strings.Join(spec.Options, "`, `"))
		}
		if len(spec.ScopedCommands) > 0 {
			fmt.Fprintf(&builder, "- **Scoped commands:** `%s`\n", strings.Join(spec.ScopedCommands, "`, `"))
		} else if spec.ScopedExemption != "" {
			fmt.Fprintf(&builder, "- **Scoped CLI:** %s\n", spec.ScopedExemption)
		}
		if spec.Selector != nil {
			fmt.Fprintf(&builder, "- **Dynamic selector:** `%s`\n", spec.Selector.Template)
			fmt.Fprintf(&builder, "- **Resource inventory:** %s\n", spec.Selector.InventoryOwner)
		}
		if spec.Guidance != "" {
			builder.WriteString("\n**Guidance:** " + spec.Guidance + "\n")
		}
		if len(spec.Related) > 0 {
			builder.WriteString("\n**Related:**\n")
			for _, related := range spec.Related {
				fmt.Fprintf(&builder, "- `%s`\n", related)
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

func configWhyRenderMode(cmd *cobra.Command) string {
	switch commandResultModeFor(cmd) {
	case resultModeHuman:
		return "human"
	case resultModeJSON:
		return "json"
	default:
		return "plain"
	}
}
