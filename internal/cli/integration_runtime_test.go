package cli

import (
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestIntegrationLifecycleCommandsReachCanonicalOperations(t *testing.T) {
	cases := map[string]capability.ID{
		"integration rtk status":               capability.IntegrationRTKStatus,
		"integration rtk probe":                capability.IntegrationRTKProbe,
		"integration rtk install":              capability.IntegrationRTKInstall,
		"integration rtk install global":       capability.IntegrationRTKInstallGlobal,
		"integration codegraph status":         capability.IntegrationCodeGraphStatus,
		"integration codegraph probe":          capability.IntegrationCodeGraphProbe,
		"integration codegraph install":        capability.IntegrationCodeGraphInstall,
		"integration codegraph install global": capability.IntegrationCodeGraphInstallGlobal,
		"integration codegraph init":           capability.IntegrationCodeGraphWorkspaceInit,
		"integration codegraph sync":           capability.IntegrationCodeGraphWorkspaceSync,
	}
	root := newRootCommand()
	for path, want := range cases {
		command, remaining, err := root.Find(strings.Fields(path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if command == nil || !command.Runnable() || len(remaining) != 0 {
			t.Fatalf("%s is not directly runnable: command=%v remaining=%v", path, command, remaining)
		}
		got, ok := canonicalCommandOperation(command)
		if !ok || got != want {
			t.Fatalf("%s operation=%q,%t want=%q,true", path, got, ok, want)
		}
		if spec, ok := capability.Lookup(want); !ok || spec.CLI.CanonicalPath != path {
			t.Fatalf("%s capability binding=%#v ok=%t", path, spec.CLI, ok)
		}
	}
}

func TestCodeGraphWorkspaceLifecycleCommandsStayWorkspaceScoped(t *testing.T) {
	root := newRootCommand()
	for _, path := range []string{"integration codegraph init", "integration codegraph sync"} {
		command, _, err := root.Find(strings.Fields(path))
		if err != nil {
			t.Fatal(err)
		}
		if command.ValidArgsFunction == nil {
			t.Fatalf("%s has no workspace completion", path)
		}
		if err := command.Args(command, nil); err == nil {
			t.Fatalf("%s accepted missing workspace id", path)
		}
	}
}
