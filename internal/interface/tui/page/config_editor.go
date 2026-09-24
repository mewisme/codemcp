package page

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

func (page *ConfigPage) initConfigEditor() error {
	if page == nil || page.action == "" || page.editor != nil {
		return nil
	}
	page.err, page.notice = nil, ""
	switch {
	case page.action == "edit" && page.resourceID != "":
		spec, ok := config.FieldByKey(page.resourceID)
		if !ok {
			return fmt.Errorf("unknown config field: %s", page.resourceID)
		}
		if !spec.Editable {
			return fmt.Errorf("%s is read-only", spec.Key)
		}
		editor, data, err := newConfigFieldEditor(page.overview.Config, spec)
		if err != nil {
			return err
		}
		page.command, page.targetKey, page.editor, page.fieldForm = ConfigEdit, spec.Key, &editor, data
	case page.section == "storage" && page.action == "export":
		editor, data := newConfigBundleEditor(true)
		page.command, page.editor, page.bundleForm = ConfigExport, &editor, data
	case page.section == "storage" && page.action == "import":
		editor, data := newConfigBundleEditor(false)
		page.command, page.editor, page.bundleForm = ConfigImport, &editor, data
	default:
		return fmt.Errorf("unsupported config editor route")
	}
	page.resizeConfigEditor()
	return nil
}

func (page *ConfigPage) submitConfigEditor() tea.Cmd {
	if page == nil || page.editor == nil || page.editor.Submitting() {
		return nil
	}
	if err := page.editor.Validate(); err != nil {
		page.editor.SetFeedback("", err)
		return nil
	}
	switch page.command {
	case ConfigEdit:
		key, raw := page.targetKey, configFieldFormValue(page.fieldForm)
		page.editor.SetSubmitting(true)
		return page.startOperation(ConfigEdit, "Saving configuration", func(ctx context.Context) configOperationMsg {
			result, err := application.SetConfigField(ctx, key, raw)
			return configOperationMsg{command: ConfigEdit, mutation: result, err: err}
		})
	case ConfigExport:
		if page.bundleForm == nil {
			return nil
		}
		data := *page.bundleForm
		page.editor.SetSubmitting(true)
		return page.startOperation(ConfigExport, "Exporting configuration envelope", func(context.Context) configOperationMsg {
			result, err := application.ExportConfig(data.Path, data.Force)
			return configOperationMsg{command: ConfigExport, path: result.Path, files: result.Files, err: err}
		})
	case ConfigImport:
		page.confirm = component.NewConfirmButtons("Import", "Cancel", false)
		page.overlay = configOverlayConfirm
		return nil
	default:
		page.editor.SetFeedback("", fmt.Errorf("unsupported config editor action: %s", page.command))
		return nil
	}
}

func (page *ConfigPage) updateConfigImportConfirm(msg tea.KeyPressMsg) tea.Cmd {
	if page == nil || page.overlay != configOverlayConfirm || page.command != ConfigImport {
		return nil
	}
	switch msg.String() {
	case "esc":
		page.overlay = configOverlayNone
		page.confirm = component.ConfirmButtons{}
		return nil
	case "enter":
		if !page.confirm.AffirmativeSelected() {
			page.overlay = configOverlayNone
			page.confirm = component.ConfirmButtons{}
			return nil
		}
		if page.bundleForm == nil || page.editor == nil {
			page.overlay = configOverlayNone
			return nil
		}
		data := *page.bundleForm
		page.confirm = component.ConfirmButtons{}
		page.editor.SetSubmitting(true)
		return page.startOperation(ConfigImport, "Importing configuration envelope", func(ctx context.Context) configOperationMsg {
			result, err := application.ImportConfig(ctx, data.Path, data.Force)
			return configOperationMsg{command: ConfigImport, files: result.Files, err: err}
		})
	default:
		return page.confirm.Update(msg)
	}
}

func (page *ConfigPage) configEditorParentNavigation() tea.Cmd {
	if page == nil {
		return nil
	}
	path := []string{"config"}
	if page.command == ConfigEdit && page.targetKey != "" {
		path = []string{"config", page.targetKey}
	} else if page.command == ConfigExport || page.command == ConfigImport {
		path = []string{"config", "storage"}
	}
	return func() tea.Msg { return NavigateMsg{Path: path, Replace: true} }
}

func (page *ConfigPage) configEditorView(width, height int) string {
	if page == nil || page.editor == nil {
		return ""
	}
	page.width, page.height = width, height
	page.resizeConfigEditor()
	return page.editor.View()
}

func (page *ConfigPage) resizeConfigEditor() {
	if page == nil || page.editor == nil || page.width <= 0 || page.height <= 0 {
		return
	}
	page.editor.Resize(page.width, page.height)
}

func (page *ConfigPage) configEditorMouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil || page.editor == nil {
		return nil
	}
	return page.editor.MouseTargets(originX, originY, z)
}

func (page *ConfigPage) configImportConfirmBody(width int) string {
	bodyWidth := component.ModalContentWidth(width)
	return strings.Join([]string{
		component.WrapContent(component.Title("Import configuration envelope?"), bodyWidth), "",
		component.WrapContent(component.Muted("Current configuration/state may be replaced. Managed secrets are not imported and existing target secrets are preserved."), bodyWidth), "",
		page.confirm.View(), component.WrapContent(component.Muted("Enter confirm · Esc keep editing"), bodyWidth),
	}, "\n")
}
