package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
)

func TestScopedSettingCoverageMatchesCanonicalRegistry(t *testing.T) {
	root := newRootCommand()
	for _, spec := range config.Settings() {
		if spec.InternalOnly {
			continue
		}
		if len(spec.ScopedCommands) == 0 && strings.TrimSpace(spec.ScopedExemption) == "" {
			t.Fatalf("setting %q has no scoped command mapping or explicit exemption", spec.Key)
		}
		for _, path := range spec.ScopedCommands {
			command, remaining, err := root.Find(strings.Fields(path))
			if err != nil || command == nil || len(remaining) != 0 || command.CommandPath() != "cm "+path {
				t.Errorf("setting %q scoped command %q missing: command=%v remaining=%v err=%v", spec.Key, path, scopedCommandPath(command), remaining, err)
				continue
			}
			if !scopedCommandDeclares(command, spec.Key) {
				t.Errorf("setting %q scoped command %q is missing canonical annotation %q", spec.Key, path, scopedSettingsAnnotation)
			}
		}
	}
}

func TestGeneratedScopedSettingCommandsDeclareCanonicalKeys(t *testing.T) {
	root := newRootCommand()
	walkScopedCommands(root, func(command *cobra.Command) {
		for _, key := range strings.Split(command.Annotations[scopedSettingsAnnotation], ",") {
			if key = strings.TrimSpace(key); key != "" {
				if _, ok := config.SettingByKey(key); !ok {
					t.Errorf("command %q declares unknown canonical setting %q", command.CommandPath(), key)
				}
			}
		}
	})
}

func TestScopedSettingGrammarMatchesCanonicalCapabilities(t *testing.T) {
	for _, spec := range config.Settings() {
		if spec.InternalOnly || strings.TrimSpace(spec.ScopedExemption) != "" {
			continue
		}
		commands := strings.Join(spec.ScopedCommands, "\n")
		if spec.Writable && len(spec.ScopedCommands) == 0 {
			t.Errorf("writable setting %q has no scoped mutation facade", spec.Key)
		}
		if spec.Secret && spec.Clearable && !strings.Contains(commands, " remove") {
			t.Errorf("clearable managed secret %q has no scoped remove facade: %v", spec.Key, spec.ScopedCommands)
		}
		if spec.Secret && spec.Verifiable && !strings.Contains(commands, " verify") {
			t.Errorf("verifiable managed secret %q has no scoped verify facade: %v", spec.Key, spec.ScopedCommands)
		}
		if spec.Secret && spec.Rotatable && !strings.Contains(commands, " create") && !strings.Contains(commands, " rotate") {
			t.Errorf("rotatable managed secret %q has no scoped create/rotate facade: %v", spec.Key, spec.ScopedCommands)
		}
		if spec.Writable && spec.Kind == config.FieldBool && spec.Selector == nil && len(spec.ScopedCommands) < 2 {
			t.Errorf("writable boolean %q should expose domain-native positive/negative facades: %v", spec.Key, spec.ScopedCommands)
		}
	}
}

func TestScopedAndUniversalStaticSettingParity(t *testing.T) {
	allowDirA := t.TempDir()
	allowDirB := t.TempDir()
	tests := []struct {
		name       string
		key        string
		value      string
		scopedArgs []string
		want       string
	}{
		{name: "server", key: "server.port", value: "43123", scopedArgs: []string{"server", "port", "43123"}, want: "43123"},
		{name: "admin", key: "admin.port", value: "43124", scopedArgs: []string{"admin", "port", "43124"}, want: "43124"},
		{name: "auth", key: "auth.mcp_legacy_bearer", value: "false", scopedArgs: []string{"auth", "mcp", "legacy", "bearer", "disable"}, want: "false"},
		{name: "permissions", key: "permissions.allow_dirs", value: allowDirA + "," + allowDirB, scopedArgs: []string{"permissions", "allow", "dir", "add", allowDirA}, want: allowDirA + "," + allowDirB},
		{name: "shell", key: "shell.path", value: "/opt/scoped-a,/opt/scoped-b", scopedArgs: []string{"shell", "path", "/opt/scoped-a,/opt/scoped-b"}, want: "/opt/scoped-a,/opt/scoped-b"},
		{name: "ponytail", key: "integrations.ponytail.mode", value: "lite", scopedArgs: []string{"integration", "ponytail", "mode", "lite"}, want: "lite"},
		{name: "caveman", key: "integrations.caveman.mode", value: "wenyan-lite", scopedArgs: []string{"integration", "caveman", "mode", "wenyan-lite"}, want: "wenyan-lite"},
		{name: "rtk", key: "integrations.rtk.path", value: "/opt/rtk", scopedArgs: []string{"integration", "rtk", "path", "/opt/rtk"}, want: "/opt/rtk"},
		{name: "codegraph", key: "integrations.codegraph.path", value: "/opt/codegraph", scopedArgs: []string{"integration", "codegraph", "path", "/opt/codegraph"}, want: "/opt/codegraph"},
		{name: "tunnel", key: "tunnel.id", value: "tun_scoped", scopedArgs: []string{"tunnel", "configure", "--id", "tun_scoped"}, want: "tun_scoped"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			genericRoot := filepath.Join(t.TempDir(), "generic")
			scopedRoot := filepath.Join(t.TempDir(), "scoped")
			initializeScopedConfigRoot(t, genericRoot)
			initializeScopedConfigRoot(t, scopedRoot)
			if _, err := executeRequestCommandError(genericRoot, []string{"config", "set", test.key, test.value}); err != nil {
				t.Fatal(err)
			}
			if test.name == "permissions" {
				if _, err := executeRequestCommandError(scopedRoot, test.scopedArgs); err != nil {
					t.Fatal(err)
				}
				if _, err := executeRequestCommandError(scopedRoot, []string{"permissions", "allow", "dir", "add", allowDirB}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := executeRequestCommandError(scopedRoot, test.scopedArgs); err != nil {
				t.Fatal(err)
			}
			generic := readScopedSettingAtRoot(t, genericRoot, test.key)
			scoped := readScopedSettingAtRoot(t, scopedRoot, test.key)
			if generic != scoped || generic != test.want {
				t.Fatalf("%s parity generic=%q scoped=%q want=%q", test.key, generic, scoped, test.want)
			}
		})
	}
}

func TestScopedAndUniversalUpstreamSettingParity(t *testing.T) {
	genericRoot := filepath.Join(t.TempDir(), "generic")
	scopedRoot := filepath.Join(t.TempDir(), "scoped")
	for _, root := range []string{genericRoot, scopedRoot} {
		initializeScopedConfigRoot(t, root)
		withScopedConfigRoot(t, root, func() {
			service, err := application.LoadUpstreamService(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Create(t.Context(), upstream.Server{ID: "docs.v2", Name: "Docs", Enabled: true, Transport: "stdio", Command: "node"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := executeRequestCommandError(genericRoot, []string{"config", "set", "upstream.servers[docs.v2].command", "bun"}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeRequestCommandError(scopedRoot, []string{"upstream", "server", "configure", "docs.v2", "--command", "bun"}); err != nil {
		t.Fatal(err)
	}
	generic := readScopedSettingAtRoot(t, genericRoot, "upstream.servers[docs.v2].command")
	scoped := readScopedSettingAtRoot(t, scopedRoot, "upstream.servers[docs.v2].command")
	if generic != "bun" || scoped != generic {
		t.Fatalf("upstream command parity generic=%q scoped=%q", generic, scoped)
	}
}

func TestTunnelAdminGenericScopedAndFlagParity(t *testing.T) {
	genericRoot := filepath.Join(t.TempDir(), "generic")
	scopedRoot := filepath.Join(t.TempDir(), "scoped")
	flagRoot := filepath.Join(t.TempDir(), "flag")
	for _, root := range []string{genericRoot, scopedRoot, flagRoot} {
		initializeScopedConfigRoot(t, root)
	}
	for _, args := range [][]string{
		{"config", "set", "tunnel.admin.key", "admin-secret"},
		{"config", "set", "tunnel.admin.workspace_id", "ws_admin"},
		{"config", "set", "tunnel.admin.enabled", "false"},
	} {
		if _, err := executeRequestCommandError(genericRoot, args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"tunnel", "admin", "key", "set", "admin-secret"},
		{"tunnel", "admin", "workspace", "set", "ws_admin"},
		{"tunnel", "admin", "disable"},
	} {
		if _, err := executeRequestCommandError(scopedRoot, args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"tunnel", "admin", "key", "set", "--admin-key", "admin-secret", "--workspace-id", "ws_admin"},
		{"tunnel", "admin", "disable"},
	} {
		if _, err := executeRequestCommandError(flagRoot, args); err != nil {
			t.Fatal(err)
		}
	}

	generic := scopedAdminState(t, genericRoot)
	scoped := scopedAdminState(t, scopedRoot)
	flagged := scopedAdminState(t, flagRoot)
	if generic != scoped || generic != flagged {
		t.Fatalf("tunnel admin parity mismatch: generic=%#v scoped=%#v flag=%#v", generic, scoped, flagged)
	}
}

func TestScopedMutationReloadsRunningRuntimeExactlyOnce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	initializeScopedConfigRoot(t, root)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/reload" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(runtimecontrol.ReloadResult{PID: os.Getpid(), ServerEnabled: true, ServerPort: 43123})
	}))
	defer server.Close()
	writeScopedRuntimeState(t, root, runtimecontrol.State{PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root})

	if _, err := executeRequestCommandError(root, []string{"server", "port", "43123"}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("runtime reload calls=%d want=1", got)
	}
}

func TestScopedConfigFacadesDoNotBypassCanonicalSettingAuthority(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"application.SetConfigField(",
		"application.SetAuthEnabled(",
		"application.RotateAuthToken(",
		"application.SetTunnelAdminKey(",
		"application.SetTunnelAdminScope(",
		"application.SetTunnelAdminEnabled(",
		"application.RemoveTunnelAdminKey(",
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Fatalf("%s bypasses canonical setting authority via %s", entry.Name(), token)
			}
		}
	}
}

func TestTunnelAdminScopedLifecycleFromFreshConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	initializeScopedConfigRoot(t, root)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" {
			t.Fatalf("request=%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("workspace_id") == "" && r.URL.Query().Get("organization_id") == "" {
			t.Fatalf("missing admin scope query: %s", r.URL.RawQuery)
		}
		requests.Add(1)
		_, _ = w.Write([]byte("{\"tunnels\":[]}"))
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"config", "set", "tunnel.control_plane_base_url", server.URL},
		{"tunnel", "admin", "workspace", "set", "ws_admin"},
	} {
		if _, err := executeRequestCommandError(root, args); err != nil {
			t.Fatal(err)
		}
	}
	output, err := executeRequestCommandError(root, []string{"tunnel", "admin", "key", "set", "admin-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "admin-secret") {
		t.Fatalf("admin key leaked from scoped setup output: %q", output)
	}
	if requests.Load() != 0 {
		t.Fatalf("offline admin setup contacted control plane: %d", requests.Load())
	}
	assertScopedAdminState(t, root, func(admin configTunnelAdminView) {
		if !admin.Enabled || !admin.KeyConfigured || admin.WorkspaceID != "ws_admin" || admin.Verified || admin.ReadAccess || admin.ManageAccess {
			t.Fatalf("offline admin state=%#v", admin)
		}
	})

	if _, err := executeRequestCommandError(root, []string{"tunnel", "admin", "verify"}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("explicit verification requests=%d want=1", requests.Load())
	}
	assertScopedAdminState(t, root, func(admin configTunnelAdminView) {
		if !admin.Verified || !admin.ReadAccess || !admin.ManageAccess {
			t.Fatalf("verified admin state=%#v", admin)
		}
	})

	if _, err := executeRequestCommandError(root, []string{"tunnel", "admin", "organization", "set", "org_admin"}); err != nil {
		t.Fatal(err)
	}
	assertScopedAdminState(t, root, func(admin configTunnelAdminView) {
		if admin.OrganizationID != "org_admin" || admin.WorkspaceID != "" || admin.TenantID != "" || admin.Verified || admin.ReadAccess || admin.ManageAccess {
			t.Fatalf("scope replacement retained stale state=%#v", admin)
		}
	})
	if requests.Load() != 1 {
		t.Fatalf("scope replacement contacted control plane: %d", requests.Load())
	}

	if _, err := executeRequestCommandError(root, []string{"tunnel", "admin", "disable"}); err != nil {
		t.Fatal(err)
	}
	assertScopedAdminState(t, root, func(admin configTunnelAdminView) {
		if admin.Enabled || !admin.KeyConfigured || admin.OrganizationID != "org_admin" {
			t.Fatalf("disabled admin state lost configured inputs=%#v", admin)
		}
	})
	if _, err := executeRequestCommandError(root, []string{"tunnel", "admin", "enable"}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeRequestCommandError(root, []string{"tunnel", "admin", "verify"}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("re-verification requests=%d want=2", requests.Load())
	}
	statusOutput, err := executeRequestCommandError(root, []string{"tunnel", "admin", "key", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(statusOutput, "admin-secret") || !strings.Contains(statusOutput, "<redacted>") {
		t.Fatalf("admin status output is not secret-safe: %q", statusOutput)
	}
}

func TestTunnelAdminCurrentCLISurfacesUseNestedCanonicalNamespace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	initializeScopedConfigRoot(t, root)
	for _, args := range [][]string{
		{"config", "list", "tunnel.admin"},
		{"config", "why", "tunnel.admin"},
		{"tunnel", "admin", "--help"},
	} {
		output, err := executeRequestCommandError(root, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(output, "tunnel.admin_") {
			t.Fatalf("%v exposed flat tunnel admin namespace: %q", args, output)
		}
	}
	completions, _ := completeConfigSet(nil, nil, "tunnel.admin")
	for _, value := range completions {
		if strings.Contains(value, "tunnel.admin_") {
			t.Fatalf("config completion exposed flat tunnel admin namespace: %q", value)
		}
	}
}

type configTunnelAdminView struct {
	Enabled        bool
	KeyConfigured  bool
	OrganizationID string
	WorkspaceID    string
	TenantID       string
	Verified       bool
	ReadAccess     bool
	ManageAccess   bool
}

func assertScopedAdminState(t *testing.T, root string, assert func(configTunnelAdminView)) {
	t.Helper()
	assert(scopedAdminState(t, root))
}

func scopedAdminState(t *testing.T, root string) configTunnelAdminView {
	t.Helper()
	var view configTunnelAdminView
	withScopedConfigRoot(t, root, func() {
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		view = configTunnelAdminView{
			Enabled:        tunnel.AdminEnabled(cfg.Tunnel),
			KeyConfigured:  strings.TrimSpace(cfg.Tunnel.Admin.Key) != "",
			OrganizationID: cfg.Tunnel.Admin.OrganizationID,
			WorkspaceID:    cfg.Tunnel.Admin.WorkspaceID,
			TenantID:       cfg.Tunnel.Admin.TenantID,
			Verified:       cfg.Tunnel.Admin.Verified,
			ReadAccess:     cfg.Tunnel.Admin.ReadAccess,
			ManageAccess:   cfg.Tunnel.Admin.ManageAccess,
		}
	})
	return view
}

func initializeScopedConfigRoot(t *testing.T, root string) {
	t.Helper()
	withScopedConfigRoot(t, root, func() {
		cfg := config.Default()
		cfg.Auth.MCPTokenHash = "mcp-configured-hash"
		cfg.Auth.AdminTokenHash = "admin-configured-hash"
		if err := config.Save(cfg); err != nil {
			t.Fatal(err)
		}
	})
}

func readScopedSettingAtRoot(t *testing.T, root, key string) string {
	t.Helper()
	var value string
	withScopedConfigRoot(t, root, func() {
		result, err := application.NewSettingService().Read(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		value = result.Value
	})
	return value
}

func withScopedConfigRoot(t *testing.T, root string, fn func()) {
	t.Helper()
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := configformat.SetRootPath(previous); err != nil {
			t.Fatal(err)
		}
	}()
	fn()
}

func writeScopedRuntimeState(t *testing.T, root string, state runtimecontrol.State) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runtimecontrol.FileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func walkScopedCommands(command *cobra.Command, visit func(*cobra.Command)) {
	if command == nil {
		return
	}
	visit(command)
	for _, child := range command.Commands() {
		walkScopedCommands(child, visit)
	}
}

func scopedCommandPath(command *cobra.Command) string {
	if command == nil {
		return "<nil>"
	}
	return command.CommandPath()
}

func scopedCommandDeclares(command *cobra.Command, key string) bool {
	if command == nil {
		return false
	}
	for _, declared := range strings.Split(command.Annotations[scopedSettingsAnnotation], ",") {
		if strings.TrimSpace(declared) == key {
			return true
		}
	}
	return false
}
