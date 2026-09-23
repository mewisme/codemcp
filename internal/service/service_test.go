package service

import (
	"path/filepath"
	"testing"
)

func TestServiceIdentityIsStablePerRootAndScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	userID := ID(root, ScopeUser)
	if userID != ID(root, ScopeUser) {
		t.Fatal("service id is not stable")
	}
	if userID == ID(root, ScopeSystem) {
		t.Fatal("service scope did not affect identity")
	}
	if userID == ID(filepath.Join(root, "other"), ScopeUser) {
		t.Fatal("config root did not affect identity")
	}
}

func TestServiceArgsAlwaysPersistExplicitConfigRoot(t *testing.T) {
	spec := Spec{ID: "cm-user-test", Scope: ScopeUser, ConfigRoot: filepath.Join(t.TempDir(), "config"), EnvironmentHash: "env-hash"}
	args := Args(spec)
	if len(args) != 10 || args[0] != "--config-dir" || args[1] != spec.ConfigRoot || args[2] != "_service" || args[3] != "run" || args[8] != "--service-environment-hash" || args[9] != "env-hash" {
		t.Fatalf("args = %#v", args)
	}
}
