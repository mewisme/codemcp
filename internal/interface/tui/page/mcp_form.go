package page

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/huh/v2"

	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/upstream"
)

type mcpServerFormData struct {
	ID                string
	Name              string
	Transport         string
	Enabled           bool
	URL               string
	Headers           string
	SensitiveHeaders  string
	BearerTokenEnvVar string
	AuthType          string
	AuthScope         string
	Command           string
	Args              string
	CWD               string
	Env               string
	SensitiveEnv      string
	ToolPrefix        string
	Expose            string
	Tools             string
	DisabledTools     string
	IdleTimeout       string
	existingHeaders   map[string]string
	existingEnv       map[string]string
}

type mcpOAuthFormData struct {
	Issuer             string
	ClientID           string
	ClientSecretEnvVar string
	ClientMetadataURL  string
	ExtraScope         string
	OpenBrowser        bool
}

func newMCPServerFormData(server upstream.Server, create bool) mcpServerFormData {
	data := mcpServerFormData{
		ID: server.ID, Name: server.Name, Transport: server.Transport, Enabled: server.Enabled, URL: server.URL,
		BearerTokenEnvVar: server.BearerTokenEnvVar, AuthType: server.Auth.Type, AuthScope: server.Auth.Scope,
		Command: server.Command, Args: strings.Join(server.Args, "\n"), CWD: server.CWD,
		ToolPrefix: server.ToolPrefix, Expose: server.Expose, Tools: strings.Join(server.Tools, "\n"), DisabledTools: strings.Join(server.DisabledTools, "\n"),
		existingHeaders: upstream.CloneStringMap(server.Headers), existingEnv: upstream.CloneStringMap(server.Env),
	}
	if create {
		if strings.TrimSpace(server.ID) == "" {
			data.Enabled = true
		}
		if data.Transport == "" {
			data.Transport = "http"
		}
		if data.Expose == "" {
			data.Expose = "all"
		}
	}
	if data.AuthType == "" {
		if data.Transport == "http" {
			data.AuthType = "auto"
		} else {
			data.AuthType = "none"
		}
	}
	if server.IdleTimeoutSec > 0 {
		data.IdleTimeout = strconv.Itoa(server.IdleTimeoutSec)
	} else {
		data.IdleTimeout = "600"
	}
	data.Headers = assignmentText(nonSensitiveMap(server.Headers))
	data.Env = assignmentText(nonSensitiveMap(server.Env))
	return data
}

func newUpstreamServerEditor(server upstream.Server, create bool) (component.Editor, *mcpServerFormData) {
	data := newMCPServerFormData(server, create)
	generalFields := []huh.Field{}
	if create {
		generalFields = append(generalFields, component.Input("Server ID", &data.ID).Validate(requiredValue("server id")))
	}
	generalFields = append(generalFields,
		component.Input("Display name", &data.Name),
		component.Select("Transport", &data.Transport, huh.NewOption("HTTP", "http"), huh.NewOption("stdio", "stdio")),
		component.Switch("Enabled", &data.Enabled, "ENABLED", "DISABLED"),
	)
	connection := component.NewEditorForm(
		component.Group(
			component.Input("HTTP MCP URL", &data.URL),
			component.Text("Non-sensitive headers (KEY=VALUE, one per line)", &data.Headers),
			component.PasswordInput("Sensitive headers JSON (optional; blank keeps existing)", &data.SensitiveHeaders),
			component.Input("Bearer token environment variable", &data.BearerTokenEnvVar),
		).WithHideFunc(func() bool { return data.Transport != "http" }),
		component.Group(
			component.Input("Command", &data.Command),
			component.Text("Arguments (one per line)", &data.Args),
			newMCPWorkingDirectoryField(&data.CWD),
			component.Text("Non-sensitive environment (KEY=VALUE, one per line)", &data.Env),
			component.PasswordInput("Sensitive environment JSON (optional; blank keeps existing)", &data.SensitiveEnv),
		).WithHideFunc(func() bool { return data.Transport != "stdio" }),
	)
	authentication := component.NewEditorForm(component.Group(
		component.Select("Auth mode", &data.AuthType, huh.NewOption("Auto", "auto"), huh.NewOption("OAuth", "oauth"), huh.NewOption("None", "none")),
		component.Input("OAuth scope", &data.AuthScope),
	))
	tools := component.NewEditorForm(component.Group(
		component.Input("Tool prefix", &data.ToolPrefix),
		component.Select("Expose", &data.Expose, huh.NewOption("All", "all"), huh.NewOption("Allowlist", "allowlist"), huh.NewOption("Metadata only", "meta_only"), huh.NewOption("None", "none")),
		component.Text("Allowlisted tools (one per line)", &data.Tools),
		component.Text("Disabled tools (one per line)", &data.DisabledTools),
		component.Input("Idle timeout (seconds)", &data.IdleTimeout).Validate(validatePositiveInt("idle timeout")),
	))
	primary := "save"
	if create {
		primary = "create"
	}
	editor := component.NewEditor(primary,
		component.EditorSection{ID: "general", Title: "General", Description: "Identity, transport, and availability.", Form: component.NewEditorForm(component.Group(generalFields...))},
		component.EditorSection{ID: "connection", Title: "Connection", Description: "HTTP connection or stdio process settings. Inactive transport values are preserved.", Form: connection},
		component.EditorSection{ID: "authentication", Title: "Authentication", Description: "HTTP authentication settings. stdio servers keep these values inactive.", Form: authentication},
		component.EditorSection{ID: "tools", Title: "Tools", Description: "Tool naming, exposure policy, allowlists, and idle timeout.", Form: tools},
	)
	return editor, &data
}

func newMCPWorkingDirectoryField(value *string) *component.PathField {
	return component.NewPathField("Working directory", value, component.PathFieldOptions{Kind: component.PathKindDirectory, AllowMissing: true})
}

func newMCPOAuthEditor() (component.Editor, *mcpOAuthFormData) {
	data := mcpOAuthFormData{OpenBrowser: true}
	editor := component.NewEditor("authorize", component.EditorSection{
		ID: "authorization", Title: "Authorization", Description: "Optional OAuth discovery and client overrides. Leave values blank to use server defaults.",
		Form: component.NewEditorForm(component.Group(
			component.Input("Issuer override", &data.Issuer),
			component.Input("Pre-registered client ID", &data.ClientID),
			component.Input("Client secret environment variable", &data.ClientSecretEnvVar),
			component.Input("Client metadata URL", &data.ClientMetadataURL),
			component.Input("Additional scopes", &data.ExtraScope),
			component.Switch("Open authorization URL in browser", &data.OpenBrowser, "YES", "NO"),
		)),
	})
	return editor, &data
}

func mcpServerFormSnapshot(data *mcpServerFormData) string {
	if data == nil {
		return ""
	}
	encoded, _ := json.Marshal(data)
	return string(encoded)
}

func serverFromMCPForm(data *mcpServerFormData, existing upstream.Server, create bool) (upstream.Server, error) {
	if data == nil {
		return upstream.Server{}, fmt.Errorf("MCP server form is unavailable")
	}
	server := existing
	if create {
		server = upstream.Server{ID: strings.TrimSpace(data.ID), Name: strings.TrimSpace(data.ID), Enabled: true, Expose: "all"}
	}
	server.ID = strings.TrimSpace(data.ID)
	server.Name = strings.TrimSpace(data.Name)
	server.Transport = strings.TrimSpace(data.Transport)
	server.Enabled = data.Enabled
	server.URL = strings.TrimSpace(data.URL)
	server.BearerTokenEnvVar = strings.TrimSpace(data.BearerTokenEnvVar)
	server.Auth.Type = strings.TrimSpace(data.AuthType)
	server.Auth.Scope = strings.TrimSpace(data.AuthScope)
	server.Command = strings.TrimSpace(data.Command)
	server.Args = splitLines(data.Args)
	server.CWD = strings.TrimSpace(data.CWD)
	server.ToolPrefix = strings.TrimSpace(data.ToolPrefix)
	server.Expose = strings.TrimSpace(data.Expose)
	server.Tools = splitLines(data.Tools)
	server.DisabledTools = splitLines(data.DisabledTools)
	idleTimeout, err := strconv.Atoi(strings.TrimSpace(data.IdleTimeout))
	if err != nil || idleTimeout <= 0 {
		return upstream.Server{}, fmt.Errorf("idle timeout must be a positive integer")
	}
	server.IdleTimeoutSec = idleTimeout
	server.Headers, err = mergeAssignmentForm(data.Headers, data.SensitiveHeaders, data.existingHeaders, "header")
	if err != nil {
		return upstream.Server{}, err
	}
	server.Env, err = mergeAssignmentForm(data.Env, data.SensitiveEnv, data.existingEnv, "env")
	if err != nil {
		return upstream.Server{}, err
	}
	return upstream.NormalizeServer(server)
}

func mergeAssignmentForm(plainText, sensitiveJSON string, existing map[string]string, label string) (map[string]string, error) {
	plain, err := upstream.ParseAssignments(splitLines(plainText), label)
	if err != nil {
		return nil, err
	}
	for key := range plain {
		if upstream.SensitiveConfigKey(key) {
			return nil, fmt.Errorf("sensitive %s %s must be entered in the masked JSON field", label, key)
		}
	}
	sensitive := sensitiveMap(existing)
	if strings.TrimSpace(sensitiveJSON) != "" {
		sensitive = map[string]string{}
		if err := json.Unmarshal([]byte(sensitiveJSON), &sensitive); err != nil {
			return nil, fmt.Errorf("decode sensitive %s JSON: %w", label, err)
		}
		for key := range sensitive {
			if !upstream.SensitiveConfigKey(key) {
				return nil, fmt.Errorf("non-sensitive %s %s belongs in the regular assignments field", label, key)
			}
		}
	}
	for key, value := range sensitive {
		if value == "" {
			delete(plain, key)
			continue
		}
		plain[key] = value
	}
	return plain, nil
}

func nonSensitiveMap(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		if !upstream.SensitiveConfigKey(key) {
			result[key] = value
		}
	}
	return result
}

func sensitiveMap(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		if upstream.SensitiveConfigKey(key) {
			result[key] = value
		}
	}
	return result
}

func assignmentText(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values[key])
	}
	return strings.Join(lines, "\n")
}

func splitLines(value string) []string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func validatePositiveInt(label string) func(string) error {
	return func(value string) error {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed <= 0 {
			return fmt.Errorf("%s must be a positive integer", label)
		}
		return nil
	}
}
