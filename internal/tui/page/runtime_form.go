package page

import (
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/tui/component"
)

type installFormData struct {
	NoAlias       bool
	Force         bool
	MigrateLegacy bool
}

type updateFormData struct {
	TargetVersion string
	NoRestart     bool
}

func newInstallEditor() (component.Editor, *installFormData) {
	data := &installFormData{MigrateLegacy: true}
	editor := component.NewEditor("install", component.EditorSection{ID: "install", Title: "Managed Install", Description: "Install this binary into the managed layout and optionally configure the cgm alias.", Form: component.NewEditorForm(component.Group(
		component.Switch("Skip cgm alias", &data.NoAlias, "YES", "NO"),
		component.Switch("Allow development build", &data.Force, "YES", "NO"),
		component.Switch("Clean verified legacy installations", &data.MigrateLegacy, "YES", "NO"),
	))})
	return editor, data
}

func (data *installFormData) Options() application.InstallCurrentOptions {
	if data == nil {
		return application.InstallCurrentOptions{}
	}
	return application.InstallCurrentOptions{NoAlias: data.NoAlias, Force: data.Force, MigrateLegacy: data.MigrateLegacy}
}

func newUpdateEditor() (component.Editor, *updateFormData) {
	data := &updateFormData{}
	editor := component.NewEditor("update", component.EditorSection{ID: "update", Title: "Update", Description: "Verify and apply a release to the managed installation.", Form: component.NewEditorForm(component.Group(
		component.Input("Target version (blank = latest; explicit may downgrade)", &data.TargetVersion),
		component.Switch("Skip managed runtime restart", &data.NoRestart, "YES", "NO"),
	))})
	return editor, data
}

func (data *updateFormData) Options() application.UpdateApplyOptions {
	if data == nil {
		return application.UpdateApplyOptions{}
	}
	return application.UpdateApplyOptions{TargetVersion: strings.TrimSpace(data.TargetVersion), NoRestart: data.NoRestart}
}
