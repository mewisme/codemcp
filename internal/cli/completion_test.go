package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/configformat"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestConfigCompletionIncludesKeysAndTypedValues(t *testing.T) {
	keys, directive := completeConfigSet(nil, nil, "per")
	if directive != cobra.ShellCompDirectiveNoFileComp || !hasCompletion(keys, "permissions.allow_dirs") {
		t.Fatalf("key completions = %#v directive=%v", keys, directive)
	}
	values, directive := completeConfigSet(nil, []string{"auth.mcp_enabled"}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp || !hasCompletion(values, "true") || !hasCompletion(values, "false") {
		t.Fatalf("bool completions = %#v directive=%v", values, directive)
	}
	dirs, directive := completeConfigSet(nil, []string{"permissions.allow_dirs"}, "")
	if len(dirs) != 0 || directive != cobra.ShellCompDirectiveFilterDirs {
		t.Fatalf("directory completion = %#v directive=%v", dirs, directive)
	}
	selection, _ := completeConfigSelection(nil, nil, "tunnel")
	if !hasCompletion(selection, "tunnel") || !hasCompletion(selection, "tunnel.enabled") || !hasCompletion(selection, "tunnel.organization_id") {
		t.Fatalf("selection completions = %#v", selection)
	}
}

func TestDynamicEntityAndSessionCompletionUsesSelectedConfigRoot(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	registered, err := workspace.NewManager(workspace.DefaultStorePath()).Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	manager := upstream.NewManager(upstream.NewStore(upstream.Path()))
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(upstream.Server{ID: "docs", Name: "Docs MCP", Transport: "http", URL: "https://example.invalid/mcp", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	journal, err := runtimeevent.NewJournal(root, runtimeevent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(runtimeevent.Event{Time: time.Now().UTC(), RunID: "run_completion_test", PID: 42, Level: "info", Name: "server.ready", Message: "Server ready"}); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCommand()
	workspaces, _ := workspaceCompletions(cmd, "ws_")
	if !hasCompletion(workspaces, registered.ID) {
		t.Fatalf("workspace completions = %#v", workspaces)
	}
	upstreams, _ := completeUpstreamID(cmd, nil, "do")
	if !hasCompletion(upstreams, "docs") {
		t.Fatalf("upstream completions = %#v", upstreams)
	}
	sessions, _ := completeSessionID(cmd, nil, "run_completion")
	if !hasCompletion(sessions, "run_completion_test") {
		t.Fatalf("session completions = %#v", sessions)
	}
}

func TestCompletionScriptsRegisterOnlyCM(t *testing.T) {
	root := newRootCommand()
	if root.Name() != "cm" {
		t.Fatalf("root name=%q", root.Name())
	}
	for _, test := range []struct {
		shell string
		want  []string
	}{
		{shell: "bash", want: []string{"__start_cm", " cm"}},
		{shell: "zsh", want: []string{"#compdef cm", "compdef _cm cm"}},
		{shell: "fish", want: []string{"complete -c cm"}},
		{shell: "powershell", want: []string{"-CommandName 'cm'"}},
	} {
		script, err := generateCompletion(root, test.shell, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range test.want {
			if !strings.Contains(script, want) {
				t.Fatalf("%s completion missing %q", test.shell, want)
			}
		}
		for _, legacy := range []string{"chatgpt-mcp", "cgm", "cmcp"} {
			if strings.Contains(script, legacy) {
				t.Fatalf("%s completion unexpectedly registers legacy executable %q", test.shell, legacy)
			}
		}
	}
}

func TestCompletionGoRunHooksUseDirectSourceInvocation(t *testing.T) {
	for _, test := range []struct {
		shell string
		start string
	}{{shell: "bash", start: "__start_cm"}, {shell: "zsh", start: "_cm"}} {
		script := goRunCompletion(test.shell, "cm")
		for _, want := range []string{"go run .", test.start, "__cm_go_run_completion"} {
			if !strings.Contains(script, want) {
				t.Fatalf("%s go-run completion missing %q", test.shell, want)
			}
		}
	}
	cmd := completionCommand()
	cmd.SetArgs([]string{"fish", "--go-run"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "supported for bash and zsh") {
		t.Fatalf("fish --go-run err=%v", err)
	}
}

func hasCompletion(values []string, want string) bool {
	for _, value := range values {
		candidate, _, _ := strings.Cut(value, "\t")
		if candidate == want {
			return true
		}
	}
	return false
}
