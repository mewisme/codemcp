//go:build darwin

package service

import (
	"strings"
	"testing"
)

func TestDarwinSystemPlistRunsAsInvokingUser(t *testing.T) {
	spec := Spec{ID: "cm-system-test", Scope: ScopeSystem, ConfigRoot: "/Users/mew/.cm", Binary: "/Users/mew/.local/bin/cm", Account: Account{Username: "mew", UID: "501", GID: "20", HomeDir: "/Users/mew"}}
	plist := DarwinPlist(spec)
	for _, expected := range []string{"<key>UserName</key>", "<string>mew</string>", "<key>RunAtLoad</key>", "<key>SuccessfulExit</key>", "--config-dir", "/Users/mew/.cm"} {
		if !strings.Contains(plist, expected) {
			t.Fatalf("plist missing %q:\n%s", expected, plist)
		}
	}
}

func TestDarwinUserPlistDoesNotOverrideIdentity(t *testing.T) {
	spec := Spec{ID: "cm-user-test", Scope: ScopeUser, ConfigRoot: "/Users/mew/.cm", Binary: "/Users/mew/.local/bin/cm", Account: Account{Username: "mew", UID: "501", HomeDir: "/Users/mew"}}
	plist := DarwinPlist(spec)
	if strings.Contains(plist, "<key>UserName</key>") {
		t.Fatalf("launch agent should inherit current user identity:\n%s", plist)
	}
}
