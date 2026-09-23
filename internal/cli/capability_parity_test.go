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
		for _, path := range append([]string{spec.CanonicalPath}, spec.PublicPaths...) {
			path = capability.NormalizePath(path)
			if !actual[path] {
				stale = append(stale, fmt.Sprintf("%s (%s)", path, spec.ID))
			}
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("capability catalog paths missing from Cobra tree:\n  %s", strings.Join(stale, "\n  "))
	}
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
