package page

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"

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
