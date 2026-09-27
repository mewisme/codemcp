package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/capability"
)

func TestRepresentativeCLIAdapterOperationContracts(t *testing.T) {
	tests := []struct {
		category      string
		path          string
		id            capability.ID
		kind          capability.Kind
		risk          capability.MutationRisk
		confirmation  capability.ConfirmationMode
		authorization capability.AuthorizationClass
		readOnly      bool
		destructive   bool
		machine       bool
	}{
		{category: "read", path: "status", id: capability.StatusOverview, kind: capability.KindQuery, risk: capability.RiskNone, confirmation: capability.ConfirmationNone, authorization: capability.AuthorizationOperator, readOnly: true},
		{category: "list", path: "workspace list", id: capability.WorkspaceList, kind: capability.KindQuery, risk: capability.RiskNone, confirmation: capability.ConfirmationNone, authorization: capability.AuthorizationOperator, readOnly: true},
		{category: "detail", path: "workspace show", id: capability.WorkspaceShow, kind: capability.KindQuery, risk: capability.RiskNone, confirmation: capability.ConfirmationNone, authorization: capability.AuthorizationOperator, readOnly: true},
		{category: "mutation", path: "workspace register", id: capability.WorkspaceRegister, kind: capability.KindMutation, risk: capability.RiskState, confirmation: capability.ConfirmationNone, authorization: capability.AuthorizationOperator},
		{category: "destructive", path: "workspace purge", id: capability.WorkspacePurge, kind: capability.KindMutation, risk: capability.RiskDestructive, confirmation: capability.ConfirmationRequired, authorization: capability.AuthorizationOperator, destructive: true},
		{category: "prompt", path: "workspace relocate", id: capability.WorkspaceRelocate, kind: capability.KindMutation, risk: capability.RiskState, confirmation: capability.ConfirmationNone, authorization: capability.AuthorizationOperator},
		{category: "review", path: "request approve", id: capability.RequestApprove, kind: capability.KindMutation, risk: capability.RiskSensitive, confirmation: capability.ConfirmationReview, authorization: capability.AuthorizationReviewer},
		{category: "machine", path: "config reveal", id: capability.ConfigSet, kind: capability.KindMutation, risk: capability.RiskState, confirmation: capability.ConfirmationNone, authorization: capability.AuthorizationOperator, machine: true},
	}

	root := newRootCommand()
	for _, test := range tests {
		t.Run(test.category, func(t *testing.T) {
			command := commandByRelativePath(root, test.path)
			if command == nil || !command.Runnable() {
				t.Fatalf("required CLI adapter %q is not runnable", test.path)
			}
			pathID, ok := capability.ForPath(test.path)
			if !ok || pathID != test.id {
				t.Fatalf("%s path operation=%q,%t want=%q,true", test.path, pathID, ok, test.id)
			}
			annotatedID, ok := canonicalCommandOperation(command)
			if !ok || annotatedID != test.id {
				t.Fatalf("%s command operation=%q,%t want=%q,true", test.path, annotatedID, ok, test.id)
			}
			spec, ok := capability.Lookup(annotatedID)
			if !ok {
				t.Fatalf("%s canonical operation %q is missing", test.path, annotatedID)
			}
			if spec.Kind != test.kind || spec.Risk != test.risk || spec.Confirmation.Mode != test.confirmation ||
				spec.Authorization != test.authorization || spec.Effects.ReadOnly != test.readOnly || spec.Effects.Destructive != test.destructive {
				t.Fatalf("%s security metadata drifted: kind=%q risk=%q confirmation=%q authorization=%q read_only=%t destructive=%t",
					test.path, spec.Kind, spec.Risk, spec.Confirmation.Mode, spec.Authorization, spec.Effects.ReadOnly, spec.Effects.Destructive)
			}
			if commandExplicitMachineOutput(command) != test.machine {
				t.Fatalf("%s machine-output=%t want=%t", test.path, commandExplicitMachineOutput(command), test.machine)
			}
			if test.machine {
				if reason := strings.TrimSpace(command.Annotations[presentationExemptAnnotation]); reason != "machine-output" {
					t.Fatalf("%s machine presentation exemption=%q", test.path, reason)
				}
			} else if commandPresentationExempt(command) {
				t.Fatalf("%s unexpectedly bypasses human presentation lifecycle", test.path)
			}
		})
	}
}

func TestRepresentativeCLIAdapterFailureUsesCanonicalWorkflow(t *testing.T) {
	rootPath := t.TempDir()
	command := commandByRelativePath(newRootCommand(), "workspace show")
	if command == nil {
		t.Fatal("workspace show command missing")
	}
	title := commandPresentationTitle(command)
	text, err := executeInteractiveLifecycleCommand(rootPath, "workspace", "show")
	if err == nil {
		t.Fatal("workspace show without an ID unexpectedly succeeded")
	}
	assertSingleHumanWorkflow(t, text, title, "Failed")
	if !strings.Contains(text, "×  ") {
		t.Fatalf("failure did not render inside the workflow: %q", text)
	}
}

func TestCLICommandsDoNotDuplicateCanonicalSecurityMetadata(t *testing.T) {
	root := newRootCommand()
	securityTerms := []string{"risk", "confirmation", "authorization", "audience", "destructive", "read_only", "open_world"}
	var traverse func(*cobra.Command)
	traverse = func(command *cobra.Command) {
		if command.Runnable() {
			if id, ok := canonicalCommandOperation(command); ok {
				if _, exists := capability.Lookup(id); !exists {
					t.Errorf("%s references unknown operation %q", relativeCommandPath(command), id)
				}
			}
			for key := range command.Annotations {
				if key == canonicalOperationAnnotation {
					continue
				}
				lower := strings.ToLower(key)
				for _, term := range securityTerms {
					if strings.Contains(lower, term) {
						t.Errorf("%s duplicates canonical security metadata in annotation %q", relativeCommandPath(command), key)
					}
				}
			}
		}
		for _, child := range command.Commands() {
			traverse(child)
		}
	}
	traverse(root)
}

func TestCLIAdaptersDoNotBypassCanonicalMutationOwners(t *testing.T) {
	root := cliAdapterRepositoryRoot(t)
	checks := []struct {
		file    string
		pattern *regexp.Regexp
		reason  string
	}{
		{
			file:    "internal/cli/workspace.go",
			pattern: regexp.MustCompile(`(?:workspaceManagerForCommand\([^)]*\)|\bmanager)\.(?:Register|Unregister|Relocate|Purge|CreateContainer|RenameContainer|DeleteContainer|AddAllowDir|RemoveAllowDir|AddWorkspacesToContainer|RemoveWorkspacesFromContainer|AddWorkspaceToContainers|RemoveWorkspaceFromContainers)\s*\(`),
			reason:  "workspace mutation must use application.WorkspaceService",
		},
		{
			file:    "internal/cli/upstream.go",
			pattern: regexp.MustCompile(`\.service\.Manager\(\)\.(?:Add|Remove|CreateBatch|SetEnabled)\s*\(`),
			reason:  "upstream mutation must use application.UpstreamService",
		},
		{
			file:    "internal/cli/config.go",
			pattern: regexp.MustCompile(`\bconfig\.Save\s*\(`),
			reason:  "configuration mutation must use the application setting/config authority",
		},
		{
			file:    "internal/cli/scoped_settings.go",
			pattern: regexp.MustCompile(`\bconfig\.Save\s*\(`),
			reason:  "scoped setting mutation must use application.SettingService",
		},
		{
			file:    "internal/cli/tunnel.go",
			pattern: regexp.MustCompile(`\bconfig\.Save\s*\(`),
			reason:  "tunnel setting mutation must use the application setting authority",
		},
	}
	for _, check := range checks {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(check.file)))
		if err != nil {
			t.Fatal(err)
		}
		if match := check.pattern.Find(data); match != nil {
			t.Errorf("%s bypasses canonical mutation ownership via %q: %s", check.file, string(match), check.reason)
		}
	}
}

func TestCLIProductionSourcesDoNotWriteHumanOutputDirectly(t *testing.T) {
	root := filepath.Join(cliAdapterRepositoryRoot(t), "internal", "cli")
	allowedWriterPlumbing := map[string]string{
		"completion_command.go":     "explicit machine-output shell completion stream",
		"interactive_interrupt.go":  "terminal raw-mode writer wrapping",
		"logging.go":                "canonical diagnostic writer plumbing",
		"managed_elevation_unix.go": "elevated child-process stdio transport",
		"mcp.go":                    "MCP stdio protocol transport",
		"output_mode.go":            "canonical result-writer selection",
		"terminal.go":               "terminal capability detection and writer wrapping",
		"tui.go":                    "alternate full-screen UI transport",
	}
	directPrint := regexp.MustCompile(`\bcmd\.(?:Print|Printf|Println)\s*\(`)
	directWriter := regexp.MustCompile(`cmd\.(?:OutOrStdout|ErrOrStderr)\s*\(\s*\)`)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if match := directPrint.Find(data); match != nil {
			t.Errorf("%s writes human output through Cobra directly via %q; use the presenter/result gateway", name, string(match))
		}
		if match := directWriter.Find(data); match != nil {
			if reason := strings.TrimSpace(allowedWriterPlumbing[name]); reason == "" {
				t.Errorf("%s accesses a Cobra output writer directly via %q without an explicit transport/plumbing exemption", name, string(match))
			}
		}
	}
	for name, reason := range allowedWriterPlumbing {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("writer plumbing exemption %s has no reason", name)
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("stale writer plumbing exemption %s: %v", name, err)
		}
		if !directWriter.Match(data) {
			t.Errorf("stale writer plumbing exemption %s: direct Cobra writer access is gone", name)
		}
	}
}

func TestMakefileFacadeMatchesPublicTopLevelCommands(t *testing.T) {
	rootPath := cliAdapterRepositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(rootPath, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	commands := makefileWords(t, source, "CM_COMMANDS")
	frontend := makefileWords(t, source, "CM_FRONTEND_COMMANDS")
	got := append(append([]string(nil), commands...), frontend...)
	sort.Strings(got)

	want := make([]string, 0)
	for _, command := range newRootCommand().Commands() {
		if command.Hidden || command.Name() == "help" {
			continue
		}
		want = append(want, command.Name())
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("thin Makefile facade drifted from public top-level Cobra commands\nmakefile:\n%s\ncobra:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantFrontend := []string{"restart", "serve", "up"}
	sort.Strings(frontend)
	if strings.Join(frontend, "\n") != strings.Join(wantFrontend, "\n") {
		t.Fatalf("commands requiring a frontend build drifted: got=%v want=%v", frontend, wantFrontend)
	}
	for _, name := range commands {
		if strings.ContainsAny(name, " /") {
			t.Fatalf("Makefile duplicates nested command path %q instead of forwarding it positionally", name)
		}
	}
	for _, contract := range []string{
		"CM_POSITIONAL_GOALS = $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))",
		"CM_EFFECTIVE_ARGS = $(strip $(CM_POSITIONAL_ARGS) $(ARGS))",
		"$(CM) $@ $(CM_EFFECTIVE_ARGS)",
	} {
		if !strings.Contains(source, contract) {
			t.Fatalf("thin Makefile positional forwarding contract is missing %q", contract)
		}
	}
}

func makefileWords(t *testing.T, source, variable string) []string {
	t.Helper()
	prefix := variable + " ="
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, prefix) {
			values := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			if len(values) == 0 {
				t.Fatalf("%s is empty", variable)
			}
			return values
		}
	}
	t.Fatalf("%s is missing", variable)
	return nil
}

func cliAdapterRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve CLI adapter test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
