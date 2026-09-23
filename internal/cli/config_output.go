package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
)

const redactedValue = config.RedactedValue

type configOutputOptions struct {
	format string
	json   bool
	yaml   bool
	toml   bool
}

func addConfigOutputFlags(cmd *cobra.Command, options *configOutputOptions) {
	cmd.Flags().StringVar(&options.format, "format", "", "output format: json, yaml, or toml")
	cmd.Flags().BoolVar(&options.json, "json", false, "output JSON")
	cmd.Flags().BoolVar(&options.yaml, "yaml", false, "output YAML")
	cmd.Flags().BoolVar(&options.toml, "toml", false, "output TOML")
}

func resolveConfigOutputFormat(options configOutputOptions) (configformat.Format, bool, error) {
	selected := make([]configformat.Format, 0, 4)
	if strings.TrimSpace(options.format) != "" {
		format, err := configformat.Parse(options.format)
		if err != nil {
			return "", false, err
		}
		selected = append(selected, format)
	}
	if options.json {
		selected = append(selected, configformat.JSON)
	}
	if options.yaml {
		selected = append(selected, configformat.YAML)
	}
	if options.toml {
		selected = append(selected, configformat.TOML)
	}
	if len(selected) == 0 {
		return "", false, nil
	}
	format := selected[0]
	for _, current := range selected[1:] {
		if current != format {
			return "", false, errors.New("only one output format may be selected")
		}
	}
	return format, true, nil
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
	format, structured, err := resolveConfigOutputFormat(options)
	if err != nil {
		return err
	}
	if structured {
		data, err := configformat.EncodeGeneric(format, wrapConfigTreeValue(key, value))
		if err != nil {
			return err
		}
		cmd.Print(string(data))
		return nil
	}
	if !listMode && strings.TrimSpace(key) != "" {
		if _, ok := value.(map[string]any); !ok {
			if text, ok := value.(string); ok {
				cmd.Println(text)
				return nil
			}
			text, err := compactConfigValue(value)
			if err != nil {
				return err
			}
			cmd.Println(text)
			return nil
		}
	}
	lines := make([]string, 0)
	flattenConfigTree(strings.TrimSpace(key), value, &lines)
	for _, line := range lines {
		cmd.Println(line)
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
