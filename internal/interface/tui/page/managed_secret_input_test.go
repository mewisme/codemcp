package page

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/tunnel"
)

func TestGenericConfigEditorCannotExposeManagedSecretAsPlainText(t *testing.T) {
	for _, spec := range config.Fields() {
		if !spec.Secret {
			continue
		}
		if spec.Editable {
			t.Fatalf("managed secret %s is exposed through the generic editable config field path", spec.Key)
		}
		if _, _, err := newConfigFieldEditor(config.Default(), spec); err == nil {
			t.Fatalf("managed secret %s unexpectedly opened in the generic config editor", spec.Key)
		}
	}
}

func TestTunnelManagedSecretEditorsNeverRenderRawValues(t *testing.T) {
	const secret = "tui-managed-secret-must-not-render"

	runtimeEditor, runtimeData := newTunnelRuntimeEditor(application.TunnelDashboard{})
	runtimeData.RuntimeAPIKey = secret
	runtimeEditor.Resize(100, 30)
	if view := runtimeEditor.View(); strings.Contains(view, secret) {
		t.Fatalf("runtime secret rendered in clear text: %q", view)
	}

	adminEditor, adminData := newTunnelAdminEditor(application.TunnelAdminStatus{})
	adminData.AdminKey = secret
	adminEditor.Resize(100, 30)
	if view := adminEditor.View(); strings.Contains(view, secret) {
		t.Fatalf("admin secret rendered in clear text: %q", view)
	}

	managedEditor, managedData := newManagedTunnelEditor(tunnel.Metadata{}, true)
	managedData.RuntimeAPIKey = secret
	form, ok := managedEditor.SectionForm(2)
	if !ok {
		t.Fatal("managed tunnel runtime form missing")
	}
	form.Resize(100, 20)
	if view := form.View(); strings.Contains(view, secret) {
		t.Fatalf("managed tunnel runtime secret rendered in clear text: %q", view)
	}

	configureEditor, configureData := newManagedConfigureEditor(false)
	configureData.RuntimeKeyMode = "manual"
	configureData.RuntimeAPIKey = secret
	form, ok = configureEditor.SectionForm(0)
	if !ok {
		t.Fatal("managed configure form missing")
	}
	form.Resize(100, 20)
	if view := form.View(); strings.Contains(view, secret) {
		t.Fatalf("managed configure secret rendered in clear text: %q", view)
	}
}
