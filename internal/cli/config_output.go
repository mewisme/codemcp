package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
)

const redactedValue = config.RedactedValue

type configOutputOptions struct {
	json bool
}

func addConfigOutputFlags(cmd *cobra.Command, options *configOutputOptions) {
	addJSONResultFlag(cmd, &options.json)
}

func redactedConfigTree(cfg config.Config) (map[string]any, error) {
	return config.RedactedTree(cfg)
}

func getConfigTreeValue(tree map[string]any, key string) (any, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return tree, nil
	}
	var current any = tree
	for _, part := range strings.Split(key, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config key has no children: %s", key)
		}
		next, exists := object[part]
		if !exists {
			return nil, fmt.Errorf("unsupported config key: %s", key)
		}
		current = next
	}
	return current, nil
}

func wrapConfigTreeValue(key string, value any) any {
	key = strings.TrimSpace(key)
	if key == "" {
		return value
	}
	parts := strings.Split(key, ".")
	wrapped := value
	for i := len(parts) - 1; i >= 0; i-- {
		wrapped = map[string]any{parts[i]: wrapped}
	}
	return wrapped
}

func printConfigSelection(cmd *cobra.Command, cfg config.Config, key string, listMode bool, options configOutputOptions) error {
	tree, err := redactedConfigTree(cfg)
	if err != nil {
		return err
	}
	value, err := getConfigTreeValue(tree, key)
	if err != nil {
		return err
	}
	if options.json {
		return writeResultJSON(cmd, wrapConfigTreeValue(key, value))
	}
	if !listMode && strings.TrimSpace(key) != "" {
		if _, ok := value.(map[string]any); !ok {
			text, err := compactConfigValue(value)
			if err != nil {
				return err
			}
			if stringValue, ok := value.(string); ok {
				text = stringValue
			}
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter := commandPresenter(cmd)
				presenter.Frame("Configuration")
				presenter.Fields(presentation.Field{Label: strings.TrimSpace(key), Value: text})
				presenter.Complete("Done")
				return nil
			}
			fmt.Fprintln(commandResultWriter(cmd), text)
			return nil
		}
	}
	lines := make([]string, 0)
	flattenConfigTree(strings.TrimSpace(key), value, &lines)
	if commandResultModeFor(cmd) == resultModeHuman {
		rows := make([]presentation.Row, 0, len(lines))
		for _, line := range lines {
			name, value, ok := strings.Cut(line, " = ")
			if !ok {
				rows = append(rows, presentation.Row{line, ""})
				continue
			}
			rows = append(rows, presentation.Row{name, value})
		}
		presenter := commandPresenter(cmd)
		presenter.Frame("Configuration")
		section := strings.TrimSpace(key)
		if section == "" {
			section = "Values"
		}
		presenter.Section(section)
		presenter.Rows([]string{"Key", "Value"}, rows...)
		presenter.Complete("Done")
		return nil
	}
	for _, line := range lines {
		fmt.Fprintln(commandResultWriter(cmd), line)
	}
	return nil
}

func flattenConfigTree(prefix string, value any, lines *[]string) {
	if object, ok := value.(map[string]any); ok {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenConfigTree(next, object[key], lines)
		}
		return
	}
	text, err := compactConfigValue(value)
	if err != nil {
		text = fmt.Sprint(value)
	}
	*lines = append(*lines, prefix+" = "+text)
}

func compactConfigValue(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSpace(buffer.String()), nil
}

func printSettingSelection(cmd *cobra.Command, service *application.SettingService, key string, listMode bool, options configOutputOptions) error {
	key = strings.TrimSpace(key)
	if !listMode && key != "" {
		result, err := service.Read(cmd.Context(), key)
		if err == nil {
			if options.json {
				return writeResultJSON(cmd, map[string]any{result.Spec.Key: settingOutputValue(result)})
			}
			if commandResultModeFor(cmd) == resultModeHuman {
				presenter := commandPresenter(cmd)
				presenter.Frame("Configuration")
				presenter.Fields(presentation.Field{Label: result.Spec.Key, Value: result.Value})
				presenter.Complete("Done")
				return nil
			}
			fmt.Fprintln(commandResultWriter(cmd), result.Value)
			return nil
		}
		if spec, ok := config.SettingByKey(key); ok && !spec.InternalOnly {
			return err
		}
		if _, ok := config.MatchSettingSelector(key); ok {
			return err
		}
		if len(config.SettingsByPrefix(key)) == 0 {
			return err
		}
	}

	results, err := service.List(cmd.Context(), key)
	if err != nil {
		return err
	}
	values := make(map[string]any, len(results))
	for _, result := range results {
		values[result.Spec.Key] = settingOutputValue(result)
	}
	if options.json {
		return writeResultJSON(cmd, values)
	}

	keys := make([]string, 0, len(values))
	for settingKey := range values {
		keys = append(keys, settingKey)
	}
	sort.Strings(keys)
	if commandResultModeFor(cmd) == resultModeHuman {
		scopes := make([]presentation.Entity, 0)
		for _, settingKey := range keys {
			scope, _, found := strings.Cut(settingKey, ".")
			if !found {
				scope = settingKey
			}
			if len(scopes) == 0 || scopes[len(scopes)-1].Title != scope {
				scopes = append(scopes, presentation.Entity{Title: scope})
			}
			scopes[len(scopes)-1].Fields = append(scopes[len(scopes)-1].Fields, presentation.Field{
				Label: settingKey,
				Value: settingDisplayValue(values[settingKey]),
			})
		}
		commandPresenter(cmd).Render(presentation.Design{
			Title:      "Configuration",
			Completion: "Done",
			Blocks: []presentation.DesignBlock{
				presentation.EntityList{Title: fmt.Sprintf("Settings · %d", len(scopes)), Items: scopes},
			},
		})
		return nil
	}
	for _, settingKey := range keys {
		fmt.Fprintf(commandResultWriter(cmd), "%s = %s\n", settingKey, settingDisplayValue(values[settingKey]))
	}
	return nil
}

func printSettingDiff(cmd *cobra.Command, service *application.SettingService, prefix string, options configOutputOptions) error {
	diffs, err := service.Diff(cmd.Context(), strings.TrimSpace(prefix))
	if err != nil {
		return err
	}
	values := make(map[string]any, len(diffs))
	for _, diff := range diffs {
		values[diff.Spec.Key] = map[string]any{
			"current":  settingOutputValue(diff.SettingResult),
			"baseline": diff.Baseline,
		}
	}
	if options.json {
		return writeResultJSON(cmd, values)
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if commandResultModeFor(cmd) == resultModeHuman {
		rows := make([]presentation.Row, 0, len(keys))
		for _, key := range keys {
			value := values[key].(map[string]any)
			rows = append(rows, presentation.Row{key, settingDisplayValue(value["current"]), settingDisplayValue(value["baseline"])})
		}
		presenter := commandPresenter(cmd)
		presenter.Frame("Configuration diff")
		presenter.Section(fmt.Sprintf("Changed %d settings", len(keys)))
		presenter.Rows([]string{"Setting", "Current", "Baseline"}, rows...)
		presenter.Complete("Done")
		return nil
	}
	for _, key := range keys {
		value := values[key].(map[string]any)
		fmt.Fprintf(commandResultWriter(cmd), "%s = %s (default: %s)\n", key, settingDisplayValue(value["current"]), settingDisplayValue(value["baseline"]))
	}
	return nil
}

func settingOutputValue(result application.SettingResult) any {
	if result.Configured != nil && !result.Spec.Secret {
		return *result.Configured
	}
	return result.Value
}

func settingDisplayValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	text, err := compactConfigValue(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return text
}
