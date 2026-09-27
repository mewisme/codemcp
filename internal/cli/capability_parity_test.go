package cli

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/capability"
)

var publicCapabilityExemptions = map[string]string{
	"tui":                  "TUI entrypoint; it is the surface being checked",
	"completion":           "local shell integration; it does not access runtime capabilities",
	"config":               "configuration namespace entrypoint only renders help and rejects positional fallbacks",
	"request create dummy": "public test-only helper for approval UI development",
}

func TestPublicCommandsHaveCanonicalCapabilities(t *testing.T) {
	root := newRootCommand()
	public := collectRunnablePublicPaths(root)
	missing := []string{}
	for _, path := range public {
		if _, exempt := publicCapabilityExemptions[path]; exempt {
			continue
		}
		if _, ok := capability.ForPath(path); !ok {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing capability mappings:\n  %s", strings.Join(missing, "\n  "))
	}

	actual := map[string]bool{}
	for _, path := range public {
		actual[path] = true
	}
	stale := []string{}
	for _, spec := range capability.All() {
		surface, ok := spec.Surface(capability.SurfaceCLI)
		if !ok || surface.State != capability.SurfaceRequired {
			continue
		}
		path := capability.NormalizePath(spec.CLI.CanonicalPath)
		if !actual[path] {
			stale = append(stale, fmt.Sprintf("%s (%s)", path, spec.ID))
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("capability catalog paths missing from Cobra tree:\n  %s", strings.Join(stale, "\n  "))
	}
}

func TestRunnableCLICommandsCarryCanonicalOperationAnnotations(t *testing.T) {
	root := newRootCommand()
	var walk func(*cobra.Command, []string, bool)
	walk = func(command *cobra.Command, prefix []string, hiddenAncestor bool) {
		hidden := hiddenAncestor || command.Hidden
		path := prefix
		if command != root {
			path = append(append([]string(nil), prefix...), command.Name())
		}
		if command.Runnable() && !hidden {
			key := capability.RootPath
			if command != root {
				key = capability.NormalizePath(strings.Join(path, " "))
			}
			want, mapped := capability.ForPath(key)
			got, annotated := canonicalCommandOperation(command)
			if mapped && (!annotated || got != want) {
				t.Errorf("%s annotation=%q,%t want=%q,true", key, got, annotated, want)
			}
			if !mapped && annotated {
				t.Errorf("%s unexpectedly annotated as %q", key, got)
			}
		}
		for _, child := range command.Commands() {
			walk(child, path, hidden)
		}
	}
	walk(root, nil, false)
}

func TestPublicCapabilityExemptionsAreExplicitAndCurrent(t *testing.T) {
	actual := map[string]bool{}
	for _, path := range collectRunnablePublicPaths(newRootCommand()) {
		actual[path] = true
	}
	for path, reason := range publicCapabilityExemptions {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("public capability exemption %q has no reason", path)
		}
		if !actual[path] {
			t.Fatalf("stale public capability exemption: %s", path)
		}
	}
}

func TestLegacyFeatureAndBuiltinsCommandGroupsAreAbsent(t *testing.T) {
	root := newRootCommand()
	for _, command := range root.Commands() {
		switch command.Name() {
		case "feature", "features", "builtin", "builtins":
			t.Fatalf("legacy command group is still public: %s", command.Name())
		}
	}
}

func TestRestoredCanonicalCLIReachabilityMatrix(t *testing.T) {
	expected := map[capability.ID]string{
		capability.InstructionSettingsRead:             "instructions get",
		capability.InstructionSettingsWrite:            "instructions set",
		capability.ProjectContextRead:                  "workspace context",
		capability.ToolInventoryRead:                   "tools list",
		capability.ExecutionList:                       "execution list",
		capability.ExecutionView:                       "execution view",
		capability.ProcessList:                         "process list",
		capability.ProcessView:                         "process view",
		capability.IntegrationRTKEnable:                "integration rtk enable",
		capability.IntegrationRTKDisable:               "integration rtk disable",
		capability.IntegrationCodeGraphWorkspaceStatus: "integration codegraph workspace status",
		capability.IntegrationCodeGraphWorkspaceInit:   "integration codegraph init",
		capability.IntegrationCodeGraphWorkspaceSync:   "integration codegraph sync",
		capability.IntegrationTypeSafeEnable:           "integration typesafe enable",
		capability.IntegrationTypeSafeDisable:          "integration typesafe disable",
		capability.DoctorRead:                          "doctor",
	}
	root := newRootCommand()
	for id, path := range expected {
		spec, ok := capability.Lookup(id)
		if !ok {
			t.Fatalf("canonical operation %q is missing", id)
		}
		if got := capability.NormalizePath(spec.CLI.CanonicalPath); got != path {
			t.Errorf("%s CLI path=%q want=%q", id, got, path)
			continue
		}
		command := commandByRelativePath(root, path)
		if command == nil || !command.Runnable() {
			t.Errorf("%s command path %q is not recursively reachable", id, path)
			continue
		}
		got, annotated := canonicalCommandOperation(command)
		if !annotated || got != id {
			t.Errorf("%s command annotation=%q,%t", path, got, annotated)
		}
	}
}

func collectRunnablePublicPaths(root *cobra.Command) []string {
	paths := []string{}
	var walk func(*cobra.Command, []string, bool)
	walk = func(cmd *cobra.Command, prefix []string, hiddenAncestor bool) {
		hidden := hiddenAncestor || cmd.Hidden
		if cmd == root {
			if cmd.Runnable() && !hidden {
				paths = append(paths, capability.RootPath)
			}
		} else {
			prefix = append(prefix, cmd.Name())
			if cmd.Runnable() && !hidden {
				paths = append(paths, capability.NormalizePath(strings.Join(prefix, " ")))
			}
		}
		for _, child := range cmd.Commands() {
			walk(child, prefix, hidden)
		}
	}
	walk(root, nil, false)
	sort.Strings(paths)
	return paths
}
