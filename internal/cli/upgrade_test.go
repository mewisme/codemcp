package cli

import (
	"testing"

	"go.mewis.me/codemcp/internal/capability"
)

func TestUpgradeCanonicalCommandAndAcceptedAliases(t *testing.T) {
	root := newRootCommand()
	for _, test := range []struct {
		args      []string
		wantPath  string
		wantOp    capability.ID
		aliasPath string
	}{
		{args: []string{"upgrade"}, wantPath: "cm upgrade", wantOp: capability.UpdateApply},
		{args: []string{"update"}, wantPath: "cm upgrade", wantOp: capability.UpdateApply, aliasPath: "update"},
		{args: []string{"upg"}, wantPath: "cm upgrade", wantOp: capability.UpdateApply, aliasPath: "upg"},
		{args: []string{"upgrade", "check"}, wantPath: "cm upgrade check", wantOp: capability.UpdateCheck},
		{args: []string{"update", "check"}, wantPath: "cm upgrade check", wantOp: capability.UpdateCheck, aliasPath: "update check"},
		{args: []string{"upg", "check"}, wantPath: "cm upgrade check", wantOp: capability.UpdateCheck, aliasPath: "upg check"},
	} {
		command, remaining, err := root.Find(test.args)
		if err != nil || command == nil || len(remaining) != 0 {
			t.Fatalf("%v resolved command=%v remaining=%v err=%v", test.args, command, remaining, err)
		}
		if got := command.CommandPath(); got != test.wantPath {
			t.Fatalf("%v canonical command path=%q want=%q", test.args, got, test.wantPath)
		}
		if operation, ok := canonicalCommandOperation(command); !ok || operation != test.wantOp {
			t.Fatalf("%v operation=%q,%t want=%q,true", test.args, operation, ok, test.wantOp)
		}
		if test.aliasPath != "" {
			if operation, ok := capability.ForPath(test.aliasPath); ok {
				t.Fatalf("accepted spelling %q became separate capability path %q", test.aliasPath, operation)
			}
		}
	}
}
