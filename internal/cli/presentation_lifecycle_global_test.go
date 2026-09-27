package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
)

func TestPresentationFamilyRepresentativesCoverTopLevelCommands(t *testing.T) {
	root := newRootCommand()
	representatives := map[string]string{
		"_service":     "_service run",
		"admin":        "admin enable",
		"agent":        "agent completion list",
		"auth":         "auth status",
		"completion":   "completion",
		"config":       "config path",
		"down":         "down",
		"doctor":       "doctor",
		"execution":    "execution list",
		"init":         "init",
		"install":      "install",
		"instructions": "instructions get",
		"integration":  "integration rtk disable",
		"logs":         "logs path",
		"mcp":          "mcp stdio",
		"notification": "notification desktop disable",
		"permissions":  "permissions allow dir add",
		"prompt":       "prompt list",
		"process":      "process list",
		"request":      "request list",
		"restart":      "restart",
		"serve":        "serve",
		"server":       "server enable",
		"shell":        "shell path",
		"status":       "status",
		"telemetry":    "telemetry status",
		"telegram":     "telegram token status",
		"tui":          "tui",
		"tunnel":       "tunnel status",
		"tools":        "tools list",
		"uninit":       "uninit",
		"up":           "up",
		"upgrade":      "upgrade",
		"upstream":     "upstream server list",
		"version":      "version",
		"workspace":    "workspace list",
	}

	var missing []string
	for _, family := range root.Commands() {
		path, ok := representatives[family.Name()]
		if !ok {
			missing = append(missing, family.Name())
			continue
		}
		cmd := commandByRelativePath(root, path)
		if cmd == nil || !cmd.Runnable() {
			t.Errorf("family %q representative %q is not runnable", family.Name(), path)
			continue
		}
		if commandPresentationTitle(cmd) == "" && !commandPresentationExempt(cmd) {
			t.Errorf("family %q representative %q lacks presentation contract", family.Name(), path)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("top-level CLI families missing presentation representative: %v", missing)
	}
	if !root.Runnable() || commandPresentationTitle(root) == "" {
		t.Fatalf("root command lacks human presentation contract")
	}
}

func TestRepresentativeWorkflowStructures(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		run        func(*cobra.Command) error
		completion string
		want       []string
	}{
		{
			name:  "read",
			title: "Read fixture",
			run: func(cmd *cobra.Command) error {
				p := commandPresenter(cmd)
				p.Section("Result")
				p.Fields(presentation.Field{Label: "state", Value: "ready"})
				commandProgressSession(cmd).SetCompletion("Done")
				return nil
			},
			completion: "Done",
			want:       []string{"◆  Result\n│\n│  state — ready"},
		},
		{
			name:  "list",
			title: "List fixture",
			run: func(cmd *cobra.Command) error {
				p := commandPresenter(cmd)
				p.Section("Items · 1")
				p.Subsection("item_one")
				p.NestedFields(presentation.Field{Label: "name", Value: "One"})
				commandProgressSession(cmd).SetCompletion("Done")
				return nil
			},
			completion: "Done",
			want:       []string{"◆  Items · 1\n│\n│  ◆ item_one\n│  │  name — One"},
		},
		{
			name:  "detail",
			title: "Detail fixture",
			run: func(cmd *cobra.Command) error {
				p := commandPresenter(cmd)
				p.Subsection("item_one")
				p.NestedFields(presentation.Field{Label: "name", Value: "One"})
				commandProgressSession(cmd).SetCompletion("Done")
				return nil
			},
			completion: "Done",
			want:       []string{"│  ◆ item_one\n│  │  name — One"},
		},
		{
			name:  "mutation",
			title: "Mutation fixture",
			run: func(cmd *cobra.Command) error {
				renderEntityMutationSuccess(cmd, "Entity updated", "item_one", presentation.Field{Label: "state", Value: "ready"})
				return nil
			},
			completion: "Done",
			want:       []string{"✓  Entity updated\n│\n│  ◆ item_one\n│  │  state — ready"},
		},
		{
			name:  "empty-state",
			title: "Empty fixture",
			run: func(cmd *cobra.Command) error {
				commandPresenter(cmd).StateSection(presentation.StatusInactive, "No items")
				commandProgressSession(cmd).SetCompletion("Done")
				return nil
			},
			completion: "Done",
			want:       []string{"◇  No items"},
		},
		{
			name:  "long-running",
			title: "Long-running fixture",
			run: func(cmd *cobra.Command) error {
				session := commandProgressSession(cmd)
				session.Update(presentation.ProgressPhase{ID: "runtime", Label: "Starting runtime", State: presentation.ProgressRunning})
				session.Success("runtime", "Starting runtime", "Runtime ready")
				commandPresenter(cmd).Fields(presentation.Field{Label: "pid", Value: 42})
				session.SetCompletion("Done")
				return nil
			},
			completion: "Done",
			want:       []string{"◆  Runtime ready", "│  pid — 42"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, output := newFailureFixture(t, test.title, test.run)
			if err := executeCommand(root); err != nil {
				t.Fatal(err)
			}
			text := output.String()
			assertSingleHumanWorkflow(t, text, test.title, test.completion)
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Fatalf("workflow missing %q: %q", want, text)
				}
			}
			if strings.Contains(text, "\n│\n│\n") {
				t.Fatalf("workflow contains duplicate empty rail states: %q", text)
			}
		})
	}
}

func TestRuntimeFailureUsesCanonicalFailedWorkflow(t *testing.T) {
	root, output := newFailureFixture(t, "Read runtime logs", func(*cobra.Command) error {
		return &runtimeUnavailableError{Operation: "read logs"}
	})
	if err := executeCommand(root); err == nil {
		t.Fatal("expected runtime failure")
	}
	text := output.String()
	assertSingleHumanWorkflow(t, text, "Read runtime logs", "Failed")
	for _, want := range []string{"×  CodeMCP runtime is not running", "│  ◆ Actions", "│  │  Start the managed runtime — cm up"} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime failure missing %q: %q", want, text)
		}
	}
}

func TestTunnelListExactLifecycleContract(t *testing.T) {
	defer configformat.SetRootPath("")
	rootPath := filepath.Join(t.TempDir(), "config")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels" || r.URL.Query().Get("workspace_id") != "ws_admin" {
			t.Fatalf("request=%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer admin-test" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"tunnels":[{"id":"tunnel_one","name":"One","workspace_ids":["ws_admin"]}]}`))
	}))
	defer server.Close()
	if err := configformat.SetRootPath(rootPath); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Admin.Key = "admin-test"
	cfg.Tunnel.Admin.WorkspaceID = "ws_admin"
	cfg.Tunnel.ControlPlaneBaseURL = server.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	text, err := executeInteractiveLifecycleCommand(rootPath, "tunnel", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertSingleHumanWorkflow(t, text, "Managed OpenAI tunnels", "Done")
	assertOrderedLifecycle(t, text,
		"┌  Managed OpenAI tunnels",
		"◆  Loaded managed tunnels · 1",
		"│  ◆ tunnel_one",
		"│  │  name — One",
		"│  │  workspaces — ws_admin",
		"└  Done",
	)
	if strings.Count(text, "Loaded managed tunnels") != 1 {
		t.Fatalf("managed tunnel loading terminal phase duplicated: %q", text)
	}
	if !strings.Contains(text, "◆  Loaded managed tunnels · 1\n│\n│  ◆ tunnel_one") {
		t.Fatalf("managed tunnel list lost the single block spacer: %q", text)
	}
}

func TestTunnelUseExactLifecycleContract(t *testing.T) {
	defer configformat.SetRootPath("")
	rootPath := filepath.Join(t.TempDir(), "config")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tunnels/tunnel_one" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer admin-test" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"id":"tunnel_one","name":"One","workspace_ids":["ws_admin"]}`))
	}))
	defer server.Close()
	if err := configformat.SetRootPath(rootPath); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.MCPEnabled = false
	cfg.Auth.AdminEnabled = false
	cfg.Server.AllowUnauthenticatedLoopback = true
	cfg.Tunnel.Admin.Key = "admin-test"
	cfg.Tunnel.Admin.WorkspaceID = "ws_admin"
	cfg.Tunnel.Admin.ReadAccess = true
	cfg.Tunnel.ControlPlaneBaseURL = server.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	text, err := executeInteractiveLifecycleCommand(rootPath, "tunnel", "use", "tunnel_one", "--runtime-api-key", "runtime-test")
	if err != nil {
		t.Fatal(err)
	}
	assertSingleHumanWorkflow(t, text, "Select managed OpenAI tunnel", "Done")
	assertOrderedLifecycle(t, text,
		"┌  Select managed OpenAI tunnel",
		"◆  Fetched managed tunnel",
		"✓  Managed tunnel selected",
		"│  ◆ tunnel_one",
		"│  │  name — One",
		"│  │  runtime — configured",
		"│  │  enabled — true",
		"└  Done",
	)
	if strings.Contains(text, "│  ◆ id — tunnel_one") || strings.Count(text, "Managed tunnel selected") != 1 {
		t.Fatalf("managed tunnel entity/result duplicated: %q", text)
	}
	if !strings.Contains(text, "✓  Managed tunnel selected\n│\n│  ◆ tunnel_one") {
		t.Fatalf("managed tunnel result lost the single entity spacer: %q", text)
	}
}

func executeInteractiveLifecycleCommand(rootPath string, args ...string) (string, error) {
	var output bytes.Buffer
	caps := presentation.Capabilities{
		StdoutTTY: true, StderrTTY: true, Width: 100, Unicode: true, RawUnicode: true, Interactive: true,
	}
	writer := presentation.WrapWriter(&output, caps)
	cmd := newRootCommand()
	cmd.SetOut(writer)
	cmd.SetErr(writer)
	cmd.SetArgs(append([]string{"--config-dir", rootPath}, args...))
	err := executeCommand(cmd)
	return output.String(), err
}

func assertSingleHumanWorkflow(t *testing.T, text, title, completion string) {
	t.Helper()
	if !strings.HasPrefix(text, "┌  "+title+"\n") {
		t.Fatalf("workflow does not start with canonical frame %q: %q", title, text)
	}
	if strings.Count(text, "┌  ") != 1 {
		t.Fatalf("workflow starts more than one frame: %q", text)
	}
	end := "└"
	if completion != "" {
		end += "  " + completion
	}
	if strings.Count(text, end) != 1 {
		t.Fatalf("workflow does not close exactly once with %q: %q", end, text)
	}
	index := strings.LastIndex(text, end)
	if index < 0 || strings.TrimSpace(text[index+len(end):]) != "" {
		t.Fatalf("visible output escaped after frame closure: %q", text)
	}
}

func assertOrderedLifecycle(t *testing.T, text string, values ...string) {
	t.Helper()
	previous := -1
	for _, value := range values {
		index := strings.Index(text, value)
		if index < 0 || index <= previous {
			t.Fatalf("lifecycle item %q missing or out of order in %q", value, text)
		}
		previous = index
	}
}
