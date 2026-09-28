package commandalias

import (
	"reflect"
	"testing"
)

func TestCanonicalizePathAwareAliasesUntilBoundary(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "root and nested", args: []string{"tg", "token", "st"}, want: []string{"telegram", "token", "status"}},
		{name: "canonical intermediate", args: []string{"ups", "server", "info", "github"}, want: []string{"upstream", "server", "show", "github"}},
		{name: "deep reused remove", args: []string{"ws", "access", "rm", "ws_x", "/tmp"}, want: []string{"workspace", "access", "remove", "ws_x", "/tmp"}},
		{name: "stop at flag", args: []string{"ws", "--json", "ls"}, want: []string{"workspace", "--json", "ls"}},
		{name: "stop at positional argument", args: []string{"ups", "server", "info", "github", "st"}, want: []string{"upstream", "server", "show", "github", "st"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := append([]string(nil), test.args...)
			if got := Canonicalize(test.args); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Canonicalize(%v)=%v want=%v", test.args, got, test.want)
			}
			if !reflect.DeepEqual(test.args, original) {
				t.Fatalf("Canonicalize mutated input: got=%v want=%v", test.args, original)
			}
		})
	}
}

func TestRegistryReturnsDefensiveCopy(t *testing.T) {
	first := Registry()
	first[""][0].Aliases[0] = "mutated"
	first[""][0].Command = "mutated"
	second := Registry()
	if second[""][0].Command != "config" || second[""][0].Aliases[0] != "cfg" {
		t.Fatalf("registry mutation leaked: %#v", second[""][0])
	}
}
