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
			}
		}
	}
}

func TestGeneratedScopedSettingCommandsDeclareCanonicalKeys(t *testing.T) {
	root := newRootCommand()
	covered := map[string]bool{}
	walkScopedCommands(root, func(command *cobra.Command) {
		for _, key := range strings.Split(command.Annotations[scopedSettingsAnnotation], ",") {
			if key = strings.TrimSpace(key); key != "" {
				covered[key] = true
			}
		}
	})
	for _, key := range []string{
		"server.enabled", "server.expose.mode", "server.expose.interfaces", "server.port", "server.allow_insecure_http", "server.allow_unauthenticated_loopback",
		"admin.enabled", "admin.port", "auth.mcp_legacy_bearer", "permissions.allow_dirs", "shell.path",
		"integrations.ponytail.active", "integrations.ponytail.mode", "integrations.caveman.active", "integrations.caveman.mode",
		"integrations.rtk.enabled", "integrations.rtk.path", "integrations.codegraph.enabled", "integrations.codegraph.path",
	} {
		if !covered[key] {
			t.Errorf("scoped setting %q does not declare canonical key annotation", key)
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
