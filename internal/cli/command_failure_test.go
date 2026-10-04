package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/cli/presentation"
	installpkg "go.mewis.me/codemcp/internal/install"
	managed "go.mewis.me/codemcp/internal/service"
	updatepkg "go.mewis.me/codemcp/internal/update"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

func newFailureFixture(t *testing.T, title string, run func(*cobra.Command) error) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	caps := presentation.Capabilities{
		StdoutTTY: true, StderrTTY: true, Width: 100, Unicode: true, RawUnicode: true, Interactive: true,
	}
	writer := presentation.WrapWriter(&output, caps)
	root := &cobra.Command{
		Use:           "cm",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			prepareCommandPresentation(cmd)
		},
	}
	addLoggingFlags(root)
	addTerminalPresentationFlags(root)
	child := &cobra.Command{
		Use:   "fixture",
		Short: title,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd)
		},
	}
	setCommandPresentationTitle(child, title)
	root.AddCommand(child)
	root.SetOut(writer)
	root.SetErr(writer)
	root.SetArgs([]string{"fixture"})
	return root, &output
}

func TestActionableFailureStaysInsideCommandWorkflow(t *testing.T) {
	const tunnelID = "tunnel_6a9462c95f008191a665c3330bcd8368"
	root, output := newFailureFixture(t, "Select managed OpenAI tunnel", func(cmd *cobra.Command) error {
		commandProgressSession(cmd).Success("tunnel.fetch", "Fetching managed tunnel", "Fetched managed tunnel")
		return &application.ManagedTunnelRuntimeKeyRequiredError{TunnelID: tunnelID}
	})
	if err := executeCommand(root); err == nil {
		t.Fatal("expected failure")
	}
	text := output.String()
	for _, want := range []string{
		"┌  Select managed OpenAI tunnel",
		"◆  Fetched managed tunnel",
		"×  Runtime API key required",
		"│  ▸ Actions",
		"│  │  Generate automatically\n│  │    cm tunnel use " + tunnelID + " --auto-runtime-key",
		"│  │  Use an existing runtime key\n│  │    cm tunnel use " + tunnelID + " --runtime-api-key <key>",
		"│  │  More options — cm tunnel use --help",
		"└  Failed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure workflow missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, "Command failed") {
		t.Fatalf("primary failure escaped shared workflow: %q", text)
	}
	if strings.Contains(text, "Usage:") {
		t.Fatalf("domain failure unexpectedly printed Cobra usage: %q", text)
	}
	if strings.Count(text, "┌  ") != 1 || strings.Count(text, "└  Failed") != 1 {
		t.Fatalf("failure workflow is not bounded exactly once: %q", text)
	}
	failed := strings.Index(text, "└  Failed")
	if failed < 0 || strings.TrimSpace(text[failed+len("└  Failed"):]) != "" {
		t.Fatalf("primary failure rendered after frame close: %q", text)
	}
}

func TestUnknownCommandSuggestionUsesCanonicalFailureGrammar(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	var output bytes.Buffer
	caps := presentation.Capabilities{
		StdoutTTY: true, StderrTTY: true, Width: 100, Unicode: true, RawUnicode: true, Interactive: true,
	}
	writer := presentation.WrapWriter(&output, caps)
	root := newRootCommand()
	root.SetOut(writer)
	root.SetErr(writer)
	root.SetArgs([]string{"sttus"})
	if err := executeCommand(root); err == nil {
		t.Fatal("expected unknown command failure")
	}
	text := output.String()
	for _, want := range []string{
		"×  Unknown command \"sttus\"",
		"│  ▸ Suggestions",
		"│  │    cm status",
		"└  Failed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("unknown command output missing %q: %q", want, text)
		}
	}
	for _, forbidden := range []string{"Did you mean this?", "\tstatus", "Usage:"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unknown command leaked raw Cobra suggestion %q: %q", forbidden, text)
		}
	}
}

func TestFailureBeforeNormalPresentationUsesFallbackFrame(t *testing.T) {
	var output bytes.Buffer
	caps := presentation.Capabilities{
		StdoutTTY: true, StderrTTY: true, Width: 100, Unicode: true, RawUnicode: true, Interactive: true,
	}
	writer := presentation.WrapWriter(&output, caps)
	root := &cobra.Command{Use: "cm", SilenceErrors: true, SilenceUsage: true}
	addLoggingFlags(root)
	addTerminalPresentationFlags(root)
	child := &cobra.Command{
		Use:   "broken",
		Short: "Broken command",
		Args: func(*cobra.Command, []string) error {
			return errors.New("invalid input")
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	setCommandPresentationTitle(child, "Broken command")
	root.AddCommand(child)
	root.SetOut(writer)
	root.SetErr(writer)
	root.SetArgs([]string{"broken"})

	if err := executeCommand(root); err == nil {
		t.Fatal("expected pre-run failure")
	}
	text := output.String()
	for _, want := range []string{"┌  Broken command", "×  invalid input", "└  Failed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fallback failure missing %q: %q", want, text)
		}
	}
	for _, forbidden := range []string{"Usage:", "Command failed", "└  Done"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("fallback failure contains %q: %q", forbidden, text)
		}
	}
}

func TestUnknownFailureIsSanitizedWithoutInventedFix(t *testing.T) {
	root, output := newFailureFixture(t, "Unknown operation", func(*cobra.Command) error {
		return errors.New("remote request failed: Authorization: Bearer secret-marker and https://example.test/path?token=query-secret")
	})
	if err := executeCommand(root); err == nil {
		t.Fatal("expected failure")
	}
	text := output.String()
	if !strings.Contains(text, "×  remote request failed: Authorization: <redacted>") {
		t.Fatalf("sanitized unknown error missing: %q", text)
	}
	for _, secret := range []string{"secret-marker", "query-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("unknown failure leaked %q: %q", secret, text)
		}
	}
	if strings.Contains(text, "command — cm ") || strings.Contains(text, "◆ More options") {
		t.Fatalf("unknown failure invented remediation: %q", text)
	}
	if !strings.Contains(text, "└  Failed") {
		t.Fatalf("unknown failure did not close failed: %q", text)
	}
}

func TestActionableFailureNeverInterpolatesUnsafeIdentifier(t *testing.T) {
	failure := classifyCommandFailure(&cobra.Command{Use: "fixture"}, &application.ManagedTunnelRuntimeKeyRequiredError{TunnelID: "tun_bad; rm -rf /"})
	joined := ""
	for _, action := range failure.Actions {
		joined += action.Command + "\n"
	}
	if strings.Contains(joined, "rm -rf") || strings.Contains(joined, "tun_bad;") {
		t.Fatalf("unsafe identifier leaked into remediation: %q", joined)
	}
	if !strings.Contains(joined, "<tunnel_id>") {
		t.Fatalf("unsafe identifier did not fall back to placeholder: %q", joined)
	}
}

func TestJSONDiagnosticFailureHasNoHumanRemediationRails(t *testing.T) {
	var output bytes.Buffer
	root := newRootCommand()
	root.SetOut(&output)
	root.SetErr(&output)
	child := &cobra.Command{Use: "json-failure", RunE: func(*cobra.Command, []string) error {
		return &application.ManagedTunnelRuntimeKeyRequiredError{TunnelID: "tunnel_safe"}
	}}
	root.AddCommand(child)
	setCommandPresentationTitle(child, "JSON failure")
	root.SetArgs(testCommandArgs(t, "--log-format=json", "json-failure"))
	if err := executeCommand(root); err == nil {
		t.Fatal("expected JSON diagnostic failure")
	}
	text := strings.TrimSpace(output.String())
	if text == "" {
		t.Fatal("expected structured diagnostic output")
	}
	for _, forbidden := range []string{"┌", "│", "└", "Generate automatically", "--auto-runtime-key", "--runtime-api-key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("JSON failure leaked human workflow content %q: %q", forbidden, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("JSON diagnostic line is invalid: %q: %v", line, err)
		}
	}
}

func TestJSONResultFailureKeepsStdoutMachineClean(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var asJSON bool
	root := newRootCommand()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	child := &cobra.Command{Use: "result-failure", RunE: func(*cobra.Command, []string) error {
		return &application.ManagedTunnelRuntimeKeyRequiredError{TunnelID: "tunnel_safe"}
	}}
	addJSONResultFlag(child, &asJSON)
	root.AddCommand(child)
	setCommandPresentationTitle(child, "Result failure")
	root.SetArgs(testCommandArgs(t, "result-failure", "--json"))
	if err := executeCommand(root); err == nil {
		t.Fatal("expected JSON result failure")
	}
	if stdout.Len() != 0 {
		t.Fatalf("JSON result stdout was polluted: %q", stdout.String())
	}
	text := stderr.String()
	if !strings.Contains(text, "Command failed") {
		t.Fatalf("diagnostic stderr missing failure: %q", text)
	}
	for _, forbidden := range []string{"┌", "│", "└", "--auto-runtime-key", "--runtime-api-key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("machine failure leaked human remediation %q: %q", forbidden, text)
		}
	}
}

func TestRepresentativeFailureClassifiersCoverCurrentDomains(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		err     error
		title   string
		command string
	}{
		{name: "runtime", path: "up", err: application.ErrNotInitialized, title: "CodeMCP is not initialized", command: "cm init"},
		{name: "config", path: "init", err: application.ErrConfigurationExists, title: "Configuration already exists", command: "cm init --force"},
		{name: "tunnel-admin", path: "tunnel list", err: application.ErrTunnelAdminDisabled, title: "Tunnel administration is disabled", command: "cm tunnel admin enable"},
		{name: "tunnel-runtime", path: "tunnel use", err: &application.ManagedTunnelRuntimeKeyRequiredError{TunnelID: "tunnel_test"}, title: "Runtime API key required", command: "cm tunnel use tunnel_test --auto-runtime-key"},
		{name: "upstream-oauth", path: "upstream server status", err: &upstream.OAuthLoginRequiredError{ServerID: "docs"}, title: "Upstream OAuth authorization required", command: "cm upstream server auth login docs"},
		{name: "workspace", path: "workspace show", err: workspace.ErrNotFound, title: "Workspace not found", command: "cm workspace list"},
		{name: "approval", path: "request view", err: approval.ErrRequestNotFound, title: "Approval request could not be resolved", command: "cm request list"},
		{name: "install", path: "install", err: installpkg.ErrDevelopmentBuild, title: "Development build requires explicit installation", command: "cm install --force"},
		{name: "update", path: "upgrade", err: updatepkg.ErrDevelopmentUpdate, title: "Development builds cannot self-update", command: "cm install --help"},
		{name: "runtime-owner", path: "down", err: &managed.RuntimeOwnerConflictError{Kind: managed.RuntimeOwnerSystem, Action: "down"}, title: "Managed runtime ownership conflict", command: "cm down --system"},
		{name: "runtime-unavailable", path: "logs", err: &runtimeUnavailableError{Operation: "follow logs", Cause: errors.New("offline")}, title: "CodeMCP runtime is not running", command: "cm up"},
		{name: "mcp-credential", path: "mcp http", err: errMCPAuthCredentialMissing, title: "MCP authentication credential required", command: "cm auth mcp create"},
		{name: "logs-confirm", path: "logs clear", err: errLogsClearConfirmationRequired, title: "Clearing runtime logs requires confirmation", command: "cm logs clear --force"},
		{name: "upstream-missing", path: "upstream server show", err: &upstreamServerNotFoundError{ServerID: "docs"}, title: "Upstream server not found", command: "cm upstream server list"},
		{name: "upstream-exists", path: "upstream server add", err: &upstreamServerExistsError{ServerID: "docs"}, title: "Upstream server already exists", command: "cm upstream server configure docs --help"},
		{name: "workspace-active", path: "workspace purge", err: workspace.ErrAlreadyActive, title: "Workspace is already active", command: "cm workspace list"},
		{name: "approval-expired", path: "request approve", err: approval.ErrCapabilityExpired, title: "Approval authorization expired", command: "cm request list"},
		{name: "install-metadata", path: "upgrade", err: installpkg.ErrMetadataNotFound, title: "Managed installation metadata not found", command: "cm install"},
		{name: "update-version-mismatch", path: "upgrade", err: updatepkg.ErrCurrentVersionMismatch, title: "Running version does not match the managed installation", command: "cm status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: strings.ReplaceAll(test.path, " ", "-")}
			failure := classifyCommandFailure(cmd, test.err)
			if failure.Title != test.title {
				t.Fatalf("title=%q want=%q", failure.Title, test.title)
			}
			found := false
			for _, action := range failure.Actions {
				if action.Command == test.command {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing remediation %q in %#v", test.command, failure.Actions)
			}
		})
	}
}
