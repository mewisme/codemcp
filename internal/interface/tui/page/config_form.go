package page

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/huh/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type configFieldFormData struct {
	Raw  string
	Bool bool
	Enum string
	Key  string
	Kind config.FieldKind
}

type configBundleFormData struct {
	Path  string
	Force bool
}

type configPatchFormData struct {
	Changes string
}

type telegramSetupFormData struct {
	Token  string
	UserID string
}

func newConfigFieldEditor(cfg config.Config, spec config.FieldSpec) (component.Editor, *configFieldFormData, error) {
	if !spec.Editable {
		return component.Editor{}, nil, fmt.Errorf("%s is read-only", spec.Key)
	}
	raw, err := config.RawValue(cfg, spec.Key)
	if err != nil {
		return component.Editor{}, nil, err
	}
	data := &configFieldFormData{Raw: raw, Key: spec.Key, Kind: spec.Kind}
	validate := func(value string) error {
		next := cfg
		return config.SetValueValidated(&next, spec.Key, value)
	}
	var field huh.Field
	switch spec.Kind {
	case config.FieldBool:
		data.Bool = strings.EqualFold(raw, "true")
		field = component.Switch(spec.Key, &data.Bool, "TRUE", "FALSE").Description(spec.Description)
	case config.FieldEnum:
		data.Enum = raw
		options := make([]huh.Option[string], 0, len(spec.Options))
		for _, option := range spec.Options {
			options = append(options, huh.NewOption(option, option))
		}
		field = component.Select(spec.Key, &data.Enum, options...).Description(spec.Description)
	case config.FieldList:
		data.Raw = strings.ReplaceAll(raw, ",", "\n")
		field = component.Text(spec.Key+" (one per line)", &data.Raw).Description(spec.Description).Validate(validate)
	case config.FieldInt, config.FieldString:
		field = component.Input(spec.Key, &data.Raw).Placeholder(spec.Description).Validate(validate)
	default:
		return component.Editor{}, nil, fmt.Errorf("unsupported config field type: %s", spec.Kind)
	}
	editor := component.NewEditor("save", component.EditorSection{ID: "field", Title: "Value", Description: spec.Description, Form: component.NewEditorForm(component.Group(field))})
	return editor, data, nil
}

func configFieldFormValue(data *configFieldFormData) string {
	if data == nil {
		return ""
	}
	switch data.Kind {
	case config.FieldBool:
		if data.Bool {
			return "true"
		}
		return "false"
	case config.FieldEnum:
		return data.Enum
	default:
		return data.Raw
	}
}

func newConfigBundleEditor(export bool) (component.Editor, *configBundleFormData) {
	data := &configBundleFormData{Path: "codemcp-config.json"}
	var pathField huh.Field
	primary, description := "import", "Import a versioned JSON configuration envelope. Managed secrets are excluded."
	if export {
		primary, description = "export", "Export portable non-secret configuration/state to a JSON envelope. The destination may not exist yet."
		pathField = component.Input("Envelope file", &data.Path).Validate(validateConfigBundlePath)
	} else {
		pathField = component.NewPathField("Envelope file", &data.Path, component.PathFieldOptions{Kind: component.PathKindFile, Validate: validateConfigBundlePath})
	}
	forceLabel := "Overwrite destination if it exists"
	if !export {
		forceLabel = "Replace existing configuration/state"
	}
	editor := component.NewEditor(primary, component.EditorSection{ID: "bundle", Title: "JSON envelope", Description: description, Form: component.NewEditorForm(component.Group(
		pathField,
		component.Switch(forceLabel, &data.Force, "YES", "NO"),
	))})
	return editor, data
}

func validateConfigBundlePath(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("envelope file is required")
	}
	if filepath.Clean(value) == "." {
		return fmt.Errorf("envelope file must name a file")
	}
	return nil
}

func newConfigPatchEditor() (component.Editor, *configPatchFormData) {
	data := &configPatchFormData{Changes: "[\n  {\"Key\": \"server.enabled\", \"Value\": \"true\"}\n]"}
	field := component.Text("Setting changes JSON", &data.Changes).Description("Array of canonical setting changes. Use Unset=true to clear a setting.").Validate(func(value string) error {
		_, err := parseConfigPatchChanges(value)
		return err
	})
	editor := component.NewEditor("apply", component.EditorSection{ID: "patch", Title: "Configuration patch", Description: "Validate the whole batch before any setting is persisted.", Form: component.NewEditorForm(component.Group(field))})
	return editor, data
}

func parseConfigPatchChanges(value string) ([]application.SettingChange, error) {
	var changes []application.SettingChange
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &changes); err != nil {
		return nil, fmt.Errorf("invalid setting changes JSON: %w", err)
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("at least one setting change is required")
	}
	return changes, nil
}

func newTelegramSetupEditor() (component.Editor, *telegramSetupFormData) {
	data := &telegramSetupFormData{}
	token := component.PasswordInput("Bot token", &data.Token).Placeholder("Telegram bot token").Validate(func(value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("bot token is required")
		}
		return nil
	})
	userID := component.Input("Authorized user ID", &data.UserID).Placeholder("Numeric Telegram user ID").Validate(func(value string) error {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("authorized user ID must be a positive integer")
		}
		return nil
	})
	editor := component.NewEditor("setup", component.EditorSection{ID: "telegram", Title: "Telegram bot", Description: "The bot token stays masked in the TUI and is persisted through the canonical managed-secret setting authority.", Form: component.NewEditorForm(component.Group(token, userID))})
	return editor, data
}
