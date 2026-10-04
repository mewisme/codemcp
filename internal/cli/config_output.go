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
	json      bool
	noAccepts bool
}

func addConfigOutputFlags(cmd *cobra.Command, options *configOutputOptions) {
	addJSONResultFlag(cmd, &options.json)
}

func addConfigListOutputFlags(cmd *cobra.Command, options *configOutputOptions) {
	addConfigOutputFlags(cmd, options)
	cmd.Flags().BoolVar(&options.noAccepts, "no-accepts", false, "hide accepted-value hints from human list output")
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
				presenter.Fields(presentation.Field{Label: strings.TrimSpace(key), Value: text})
				return nil
			}
			return writePlainResultLine(cmd, text)
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
		section := strings.TrimSpace(key)
		if section == "" {
			section = "Values"
		}
		presenter.Section(section)
		presenter.Table([]string{"Key", "Value"}, rows, presentation.TableOptions{
			Border: presentation.TableBare,
			Layout: presentation.TableAdaptive,
			Depth:  1,
		})
		return nil
	}
	for _, line := range lines {
		if err := writePlainResultLine(cmd, line); err != nil {
			return err
		}
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
				presenter.Fields(presentation.Field{Label: result.Spec.Key, Value: result.Value})
				return nil
			}
			return writePlainResultLine(cmd, result.Value)
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
	resultByKey := make(map[string]application.SettingResult, len(results))
	for _, result := range results {
		values[result.Spec.Key] = settingOutputValue(result)
		resultByKey[result.Spec.Key] = result
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
		presenter := commandPresenter(cmd)
		headers := []string{"Key", "Value"}
		if !options.noAccepts {
			headers = append(headers, "Accepts")
		}
		type scopedRows struct {
			scope string
			rows  []presentation.Row
		}
		groups := make([]scopedRows, 0)
		allRows := make([]presentation.Row, 0, len(keys))
		for _, settingKey := range keys {
			scope, _, found := strings.Cut(settingKey, ".")
			if !found {
				scope = settingKey
			}
			row := presentation.Row{settingKey, settingDisplayValue(values[settingKey])}
			if !options.noAccepts {
				row = append(row, config.AcceptedValueHint(resultByKey[settingKey].Spec))
			}
			if len(groups) == 0 || groups[len(groups)-1].scope != scope {
				groups = append(groups, scopedRows{scope: scope})
			}
			groups[len(groups)-1].rows = append(groups[len(groups)-1].rows, row)
			allRows = append(allRows, row)
		}
		widths := presentation.AlignedRowWidths(headers, allRows...)
		for _, group := range groups {
			presenter.Subsection(group.scope)
			presenter.Table(headers, group.rows, presentation.TableOptions{
				Border: presentation.TableBare,
				Layout: presentation.TableAdaptive,
				Depth:  2,
				Widths: widths,
			})
		}
		return nil
	}
	for _, settingKey := range keys {
		if err := writePlainResultf(cmd, "%s = %s\n", settingKey, settingDisplayValue(values[settingKey])); err != nil {
			return err
		}
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
		presenter.Section(fmt.Sprintf("Changed %d settings", len(keys)))
		presenter.Table([]string{"Setting", "Current", "Baseline"}, rows, presentation.TableOptions{
			Border: presentation.TableBare,
			Layout: presentation.TableAdaptive,
			Depth:  1,
		})
		return nil
	}
	for _, key := range keys {
		value := values[key].(map[string]any)
		if err := writePlainResultf(cmd, "%s = %s (default: %s)\n", key, settingDisplayValue(value["current"]), settingDisplayValue(value["baseline"])); err != nil {
			return err
		}
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
