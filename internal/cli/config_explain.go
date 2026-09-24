package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func configExplainCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "explain [key]",
		Short: "Explain a configuration key or subtree from the config schema",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := ""
			if len(args) > 0 {
				key = args[0]
			}
			lookupSpan := tracepkg.Start(cmd.Context(), "CONFIG", "config.explain.schema", "Looking up config schema explanation", tracepkg.String("key", key), tracepkg.Bool("root", key == ""))
			explanation, err := config.Explain(key)
			if err != nil {
				lookupSpan.FailMessage("Config schema explanation lookup failed", err, tracepkg.String("key", key))
				return err
			}
			lookupSpan.EndMessage("Config schema explanation resolved", tracepkg.String("key", key), tracepkg.Bool("branch", explanation.Branch), tracepkg.Int("child_count", len(explanation.Children)))
			if jsonOutput {
				renderSpan := tracepkg.Start(cmd.Context(), "CONFIG", "config.explain.render", "Rendering config explanation", tracepkg.String("mode", "json"), tracepkg.String("key", key), tracepkg.Int("child_count", len(explanation.Children)))
				if err := writeResultJSON(cmd, explanation); err != nil {
					renderSpan.FailMessage("Config explanation JSON render failed", err, tracepkg.String("mode", "json"), tracepkg.String("key", key), tracepkg.Int("child_count", len(explanation.Children)))
					return err
				}
				renderSpan.EndMessage("Config explanation rendered", tracepkg.String("mode", "json"), tracepkg.String("key", key), tracepkg.Int("child_count", len(explanation.Children)))
				return nil
			}
			markdown := configExplanationMarkdown(explanation)
			capabilities := commandTerminalCapabilities(cmd)
			width := capabilities.Width
			terminal := capabilities.Interactive
			style := "dark"
			if !capabilities.Color {
				style = "ascii"
			}
			renderSpan := tracepkg.Start(cmd.Context(), "CONFIG", "config.explain.render", "Rendering config explanation Markdown", tracepkg.String("mode", "markdown"), tracepkg.String("key", key), tracepkg.Int("child_count", len(explanation.Children)), tracepkg.Int("markdown_bytes", len(markdown)), tracepkg.Int("terminal_width", width), tracepkg.Bool("terminal", terminal), tracepkg.String("style", style))
			presenter := commandPresenter(cmd)
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter.Frame("Config explanation")
			}
			if err := presenter.Markdown(markdown); err != nil {
				renderSpan.FailMessage("Config explanation Markdown render failed", err, tracepkg.String("mode", "markdown"), tracepkg.String("key", key), tracepkg.Int("child_count", len(explanation.Children)), tracepkg.Int("markdown_bytes", len(markdown)), tracepkg.Int("terminal_width", width), tracepkg.Bool("terminal", terminal), tracepkg.String("style", style))
				return err
			}
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter.FrameEnd("Done")
			}
			renderSpan.EndMessage("Config explanation Markdown rendered", tracepkg.String("mode", "markdown"), tracepkg.String("key", key), tracepkg.Int("child_count", len(explanation.Children)), tracepkg.Int("markdown_bytes", len(markdown)), tracepkg.Int("terminal_width", width), tracepkg.Bool("terminal", terminal), tracepkg.String("style", style))
			return nil
		},
	}
	addJSONResultFlag(cmd, &jsonOutput)
	cmd.ValidArgsFunction = completeConfigSelection
	return cmd
}

func configExplanationMarkdown(explanation config.Explanation) string {
	var builder strings.Builder
	writeConfigExplanationMarkdown(&builder, explanation, 1)
	return strings.TrimSpace(builder.String())
}

func writeConfigExplanationMarkdown(builder *strings.Builder, explanation config.Explanation, depth int) {
	title := explanation.Key
	if title == "" {
		title = explanation.Label
	}
	if title != "" {
		builder.WriteString(strings.Repeat("#", min(depth, 6)) + " " + title + "\n\n")
	}
	if explanation.Label != "" && explanation.Label != title {
		builder.WriteString("**" + explanation.Label + "**\n\n")
	}
	if explanation.Description != "" {
		builder.WriteString(explanation.Description + "\n\n")
	}
	if explanation.Details != "" {
		builder.WriteString(explanation.Details + "\n\n")
	}
	if !explanation.Branch {
		writeConfigFieldMetadataMarkdown(builder, explanation)
		return
	}
	for _, child := range explanation.Children {
		writeConfigExplanationMarkdown(builder, child, depth+1)
	}
}

func writeConfigFieldMetadataMarkdown(builder *strings.Builder, explanation config.Explanation) {
	fmt.Fprintf(builder, "- **Type:** `%s`\n", explanation.Kind)
	fmt.Fprintf(builder, "- **Default:** `%s`\n", formatExplainDefault(explanation.Default))
	fmt.Fprintf(builder, "- **Editable:** `%t`\n", explanation.Editable)
	if explanation.Sensitive {
		builder.WriteString("- **Sensitive:** `true`\n")
	}
	if len(explanation.Values) > 0 {
		builder.WriteString("- **Values:**\n")
		for _, value := range explanation.Values {
			if value.Description == "" {
				fmt.Fprintf(builder, "  - `%s`\n", value.Value)
			} else {
				fmt.Fprintf(builder, "  - `%s` — %s\n", value.Value, value.Description)
			}
		}
	}
	if explanation.Guidance != "" {
		builder.WriteString("\n**Guidance:** " + explanation.Guidance + "\n")
	}
	if len(explanation.Related) > 0 {
		builder.WriteString("\n**Related:**\n")
		for _, key := range explanation.Related {
			fmt.Fprintf(builder, "- `%s`\n", key)
		}
	}
	builder.WriteString("\n")
}

func formatExplainDefault(value string) string {
	if value == "" {
		return "<empty>"
	}
	return value
}
