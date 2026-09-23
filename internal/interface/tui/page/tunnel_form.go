package page

import (
	"fmt"
	"strings"

	"charm.land/huh/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/tunnel"
)

type tunnelRuntimeFormData struct {
	Enabled        bool
	ID             string
	RuntimeAPIKey  string
	ControlPlane   string
	OrganizationID string
}

type tunnelAdminFormData struct {
	AdminKey  string
	ScopeKind string
	ScopeID   string
}

type managedTunnelFormData struct {
	Name            string
	Description     string
	OrganizationIDs string
	WorkspaceIDs    string
	TenantIDs       string
	Configure       bool
	RuntimeAPIKey   string
	Enable          bool
}

type managedConfigureFormData struct {
	RuntimeKeyMode string
	RuntimeAPIKey  string
	ProjectID      string
}

func newTunnelRuntimeEditor(dashboard application.TunnelDashboard) (component.Editor, *tunnelRuntimeFormData) {
	data := &tunnelRuntimeFormData{Enabled: dashboard.Config.Enabled, ID: dashboard.Config.ID, ControlPlane: dashboard.Config.ControlPlaneBaseURL, OrganizationID: dashboard.Config.OrganizationID}
	enabledTitle := "Enabled (at least one MCP transport must remain enabled)"
	if !dashboard.MCPHTTPEnabled {
		enabledTitle = "Enabled (required while MCP HTTP is disabled)"
	}
	enabled := component.Switch(enabledTitle, &data.Enabled, "ENABLED", "DISABLED")
	if !dashboard.MCPHTTPEnabled {
		enabled.Validate(func(value bool) error {
			if !value {
				return fmt.Errorf("tunnel must remain enabled while MCP HTTP is disabled")
			}
			return nil
		})
	}
	editor := component.NewEditor("save", component.EditorSection{
		ID: "runtime", Title: "Runtime", Description: "Configure the selected runtime tunnel. Blank runtime API key keeps the current secret.",
		Form: component.NewEditorForm(component.Group(
			enabled,
			component.Input("Tunnel ID", &data.ID),
			component.PasswordInput("Runtime API key", &data.RuntimeAPIKey).Placeholder("Blank keeps the current key."),
			component.Input("Control plane base URL", &data.ControlPlane),
			component.Input("Organization ID", &data.OrganizationID),
		)),
	})
	return editor, data
}
func newTunnelAdminEditor(status application.TunnelAdminStatus) (component.Editor, *tunnelAdminFormData) {
	data := &tunnelAdminFormData{ScopeKind: "auto"}
	scope := status.Scope
	switch {
	case scope.OrganizationID != "":
		data.ScopeKind, data.ScopeID = "organization", scope.OrganizationID
	case scope.WorkspaceID != "":
		data.ScopeKind, data.ScopeID = "workspace", scope.WorkspaceID
	case scope.TenantID != "":
		data.ScopeKind, data.ScopeID = "tenant", scope.TenantID
	}
	editor := component.NewEditor("verify", component.EditorSection{
		ID: "admin-key", Title: "Admin Key", Description: "Store and verify an OpenAI admin key with Tunnels Manage access.",
		Form: component.NewEditorForm(component.Group(
			component.PasswordInput("OpenAI admin API key (Tunnels Manage)", &data.AdminKey),
			component.Select("Verification scope", &data.ScopeKind,
				huh.NewOption("Auto (reuse or derive)", "auto"), huh.NewOption("Organization", "organization"), huh.NewOption("Workspace", "workspace"), huh.NewOption("Tenant", "tenant"),
			),
			component.Input("Scope ID (ignored for Auto)", &data.ScopeID),
		)),
	})
	return editor, data
}
func newManagedTunnelEditor(metadata tunnel.Metadata, create bool) (component.Editor, *managedTunnelFormData) {
	data := &managedTunnelFormData{
		Name: metadata.Name, Description: metadata.Description,
		OrganizationIDs: strings.Join(metadata.OrganizationIDs, "\n"), WorkspaceIDs: strings.Join(metadata.WorkspaceIDs, "\n"), TenantIDs: strings.Join(metadata.TenantIDs, "\n"),
	}
	name := component.Input("Name", &data.Name).Validate(requiredValue("tunnel name"))
	description := component.Text("Description", &data.Description).Validate(requiredValue("tunnel description"))
	if !create {
		description = component.Text("Description", &data.Description)
	}
	primary := "save"
	if create {
		primary = "create"
	}
	editor := component.NewEditor(primary,
		component.EditorSection{ID: "general", Title: "General", Description: "Name and description for the managed tunnel.", Form: component.NewEditorForm(component.Group(name, description))},
		component.EditorSection{ID: "scope", Title: "Scope", Description: "Optional organization, workspace, and tenant IDs. Enter one ID per line.", Form: component.NewEditorForm(component.Group(
			component.Text("Organization IDs (one per line)", &data.OrganizationIDs),
			component.Text("Workspace IDs (one per line)", &data.WorkspaceIDs),
			component.Text("Tenant IDs (one per line)", &data.TenantIDs),
		))},
		component.EditorSection{ID: "runtime", Title: "Runtime", Description: "Optionally select this tunnel for the local runtime after saving. Blank runtime key reuses the current secret.", Form: component.NewEditorForm(component.Group(
			component.Switch("Configure cm to use this tunnel", &data.Configure, "YES", "NO"),
			component.PasswordInput("Runtime API key", &data.RuntimeAPIKey).Placeholder("Blank reuses the current runtime key."),
			component.Switch("Enable tunnel after configure", &data.Enable, "ENABLED", "DISABLED"),
		))},
	)
	return editor, data
}

func newManagedConfigureEditor(runtimeKeyConfigured bool) (component.Editor, *managedConfigureFormData) {
	mode := "auto"
	options := []huh.Option[string]{huh.NewOption("Auto generate with admin key", "auto"), huh.NewOption("Enter runtime key manually", "manual")}
	if runtimeKeyConfigured {
		mode = "reuse"
		options = append([]huh.Option[string]{huh.NewOption("Reuse current runtime key", "reuse")}, options...)
	}
	data := &managedConfigureFormData{RuntimeKeyMode: mode}
	editor := component.NewEditor("use", component.EditorSection{
		ID: "runtime", Title: "Runtime", Description: "Use this managed tunnel and enable it for the local runtime.",
		Form: component.NewEditorForm(
			component.Group(component.Select("Runtime credential", &data.RuntimeKeyMode, options...)),
			component.Group(component.Input("OpenAI project ID (optional)", &data.ProjectID).Placeholder("Blank uses the only active project or Default project.")).WithHideFunc(func() bool { return data.RuntimeKeyMode != "auto" }),
			component.Group(component.PasswordInput("Runtime API key", &data.RuntimeAPIKey).Placeholder("Read + Use key.").Validate(func(value string) error {
				if data.RuntimeKeyMode == "manual" && strings.TrimSpace(value) == "" {
					return fmt.Errorf("runtime API key is required")
				}
				return nil
			})).WithHideFunc(func() bool { return data.RuntimeKeyMode != "manual" }),
		),
	})
	return editor, data
}

func runtimeInputFromForm(data *tunnelRuntimeFormData) application.TunnelRuntimeInput {
	input := application.TunnelRuntimeInput{Enabled: &data.Enabled, ID: &data.ID, ControlPlaneBaseURL: &data.ControlPlane, OrganizationID: &data.OrganizationID}
	if strings.TrimSpace(data.RuntimeAPIKey) != "" {
		input.APIKey = &data.RuntimeAPIKey
	}
	return input
}

func adminInputFromForm(data *tunnelAdminFormData) application.TunnelAdminKeyInput {
	input := application.TunnelAdminKeyInput{Key: data.AdminKey}
	if data.ScopeKind == "auto" {
		return input
	}
	scope := tunnel.AdminScope{}
	switch data.ScopeKind {
	case "organization":
		scope.OrganizationID = data.ScopeID
	case "workspace":
		scope.WorkspaceID = data.ScopeID
	case "tenant":
		scope.TenantID = data.ScopeID
	}
	input.Scope = &scope
	return input
}

func managedCreateInput(data *managedTunnelFormData) (tunnel.CreateRequest, application.ManagedTunnelOptions) {
	request := tunnel.CreateRequest{
		Name: strings.TrimSpace(data.Name), Description: strings.TrimSpace(data.Description),
		OrganizationIDs: application.NormalizeTunnelIDs(splitLines(data.OrganizationIDs)), WorkspaceIDs: application.NormalizeTunnelIDs(splitLines(data.WorkspaceIDs)), TenantIDs: application.NormalizeTunnelIDs(splitLines(data.TenantIDs)),
	}
	return request, application.ManagedTunnelOptions{Configure: data.Configure, RuntimeAPIKey: data.RuntimeAPIKey, Enable: data.Enable}
}

func managedUpdateInput(data *managedTunnelFormData) (tunnel.UpdateRequest, application.ManagedTunnelOptions) {
	name, description := strings.TrimSpace(data.Name), data.Description
	organizations, workspaces, tenants := application.NormalizeTunnelIDs(splitLines(data.OrganizationIDs)), application.NormalizeTunnelIDs(splitLines(data.WorkspaceIDs)), application.NormalizeTunnelIDs(splitLines(data.TenantIDs))
	request := tunnel.UpdateRequest{Name: &name, Description: &description, OrganizationIDs: &organizations, WorkspaceIDs: &workspaces, TenantIDs: &tenants}
	return request, application.ManagedTunnelOptions{Configure: data.Configure, RuntimeAPIKey: data.RuntimeAPIKey, Enable: data.Enable}
}
