package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

const helpAliasPathsAnnotation = "cm.help.alias-paths"

type helpCommandMetadata struct {
	CanonicalPath string
	AliasPaths    []string
}

func configureHelpPresentation(root *cobra.Command) {
	if root == nil {
		return
	}
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		renderCommandHelp(cmd)
	})
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		renderCommandUsage(cmd)
		return nil
	})
}

// setCommandHelpAliasPaths is the presentation hook for command-path alias
// metadata. Alias ownership stays outside the help renderer.
func setCommandHelpAliasPaths(cmd *cobra.Command, aliases ...string) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	values := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if alias = strings.TrimSpace(alias); alias != "" {
			values = append(values, alias)
		}
	}
	cmd.Annotations[helpAliasPathsAnnotation] = strings.Join(values, "\x1f")
}

func renderCommandHelp(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()
	cmd.InitDefaultHelpCmd()

	presenter := commandPresenter(cmd)
	presenter.Frame(cmd.CommandPath())

	description := strings.TrimSpace(cmd.Long)
	if description == "" {
		description = strings.TrimSpace(cmd.Short)
	}
	if description != "" {
		presenter.Section(description)
	}

	renderHelpMetadata(presenter, commandHelpMetadata(cmd))
	renderHelpUsage(presenter, cmd)
	renderHelpCommands(presenter, cmd)
	renderHelpFlags(presenter, "Flags", cmd.NonInheritedFlags())
	renderHelpFlags(presenter, "Global Flags", cmd.InheritedFlags())

	if example := strings.TrimSpace(cmd.Example); example != "" {
		presenter.Section("Examples")
		for _, line := range strings.Split(example, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				presenter.List(line)
			}
		}
	}

	if cmd.HasAvailableSubCommands() {
		presenter.Note("More", fmt.Sprintf("Use %q for more information about a command.", cmd.CommandPath()+" [command] --help"))
	}
	presenter.FrameEnd("")
}

func renderCommandUsage(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()
	presenter := commandPresenter(cmd)
	presenter.Frame(cmd.CommandPath())
	renderHelpUsage(presenter, cmd)
	presenter.FrameEnd("")
}

func renderHelpMetadata(presenter *presentation.Presenter, metadata helpCommandMetadata) {
	if presenter == nil || len(metadata.AliasPaths) == 0 {
		return
	}
	presenter.Section("Command")
	presenter.Fields(
		presentation.Field{Label: "canonical", Value: metadata.CanonicalPath},
		presentation.Field{Label: "aliases", Value: strings.Join(metadata.AliasPaths, ", ")},
	)
}

func renderHelpUsage(presenter *presentation.Presenter, cmd *cobra.Command) {
	if presenter == nil || cmd == nil {
		return
	}
	lines := helpUsageLines(cmd)
	if len(lines) == 0 {
		return
	}
	presenter.Section("Usage")
	presenter.List(lines...)
}

func renderHelpCommands(presenter *presentation.Presenter, cmd *cobra.Command) {
	if presenter == nil || cmd == nil || !cmd.HasAvailableSubCommands() {
		return
	}
	rows := make([]presentation.Row, 0, len(cmd.Commands()))
	for _, child := range cmd.Commands() {
		if !child.IsAvailableCommand() && child.Name() != "help" {
			continue
		}
		rows = append(rows, presentation.Row{child.Name(), strings.TrimSpace(child.Short)})
	}
	if len(rows) == 0 {
		return
	}
	presenter.Section("Commands")
	presenter.Rows([]string{"Command", "Description"}, rows...)
}

func renderHelpFlags(presenter *presentation.Presenter, title string, flags *pflag.FlagSet) {
	if presenter == nil || flags == nil || !flags.HasAvailableFlags() {
		return
	}
	fields := make([]presentation.Field, 0, flags.NFlag())
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}
		usage := strings.TrimSpace(flag.Usage)
		if suffix := helpFlagDefault(flag); suffix != "" {
			usage += suffix
		}
		if flag.Deprecated != "" {
			usage += " (deprecated: " + flag.Deprecated + ")"
		}
		fields = append(fields, presentation.Field{Label: helpFlagLabel(flag), Value: usage})
	})
	if len(fields) == 0 {
		return
	}
	presenter.Section(title)
	presenter.Fields(fields...)
}

func helpUsageLines(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	lines := make([]string, 0, 2)
	if cmd.Runnable() || !cmd.HasAvailableSubCommands() {
		lines = append(lines, cmd.UseLine())
	}
	if cmd.HasAvailableSubCommands() {
		lines = append(lines, cmd.CommandPath()+" [command]")
	}
	return lines
}

func commandHelpMetadata(cmd *cobra.Command) helpCommandMetadata {
	if cmd == nil {
		return helpCommandMetadata{}
	}
	metadata := helpCommandMetadata{CanonicalPath: cmd.CommandPath()}
	parentPath := ""
	if cmd.Parent() != nil {
		parentPath = cmd.Parent().CommandPath()
	}
	seen := map[string]bool{metadata.CanonicalPath: true}
	for _, alias := range cmd.Aliases {
		path := strings.TrimSpace(strings.TrimSpace(parentPath) + " " + strings.TrimSpace(alias))
		if path != "" && !seen[path] {
			seen[path] = true
			metadata.AliasPaths = append(metadata.AliasPaths, path)
		}
	}
	if cmd.Annotations != nil {
		for _, alias := range strings.Split(cmd.Annotations[helpAliasPathsAnnotation], "\x1f") {
			alias = strings.TrimSpace(alias)
			if alias != "" && !seen[alias] {
				seen[alias] = true
				metadata.AliasPaths = append(metadata.AliasPaths, alias)
			}
		}
	}
	return metadata
}

func helpFlagLabel(flag *pflag.Flag) string {
	if flag == nil {
		return ""
	}
	label := "--" + flag.Name
	if flag.Shorthand != "" {
		label = "-" + flag.Shorthand + ", " + label
	}
	typeName := flag.Value.Type()
	if typeName != "bool" {
		switch typeName {
		case "stringSlice", "stringArray":
			typeName = "strings"
		}
		if flag.NoOptDefVal != "" {
			typeName += "[=" + strconv.Quote(flag.NoOptDefVal) + "]"
		}
		label += " " + typeName
	}
	return label
}

func helpFlagDefault(flag *pflag.Flag) string {
	if flag == nil {
		return ""
	}
	value := strings.TrimSpace(flag.DefValue)
	if value == "" || value == "false" || value == "0" || value == "[]" {
		return ""
	}
	if flag.Value.Type() == "string" {
		value = strconv.Quote(value)
	}
	return " (default " + value + ")"
}
