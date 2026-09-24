package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

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
			if text, ok := value.(string); ok {
				fmt.Fprintln(commandResultWriter(cmd), text)
				return nil
			}
			text, err := compactConfigValue(value)
			if err != nil {
				return err
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
		presenter.FrameEnd("Done")
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
