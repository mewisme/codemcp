package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSpecHelpers(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "config")
	spec, err := NewSpec(root, binary, ScopeUser, Account{Username: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.ID != ID(root, ScopeUser) || spec.Scope != ScopeUser || spec.Binary != filepath.Clean(binary) || spec.Account.Username != "tester" {
		t.Fatalf("spec = %#v", spec)
	}
	if _, err := NewSpec("", binary, ScopeUser, Account{}); err == nil {
		t.Fatal("empty config root accepted")
	}
	if _, err := StableBinaryPath("definitely-not-a-real-cm-binary"); err == nil {
		t.Fatal("missing binary accepted")
	}
	spec.EnvironmentHash = "env-hash"
	joined := strings.Join(Args(spec), " ")
	for _, value := range []string{spec.ConfigRoot, spec.ID, string(spec.Scope), spec.EnvironmentHash} {
		if !strings.Contains(joined, value) {
			t.Fatalf("args %q missing %q", joined, value)
		}
	}
}

func TestRunCommand(t *testing.T) {
	output, err := runCommand("go", "env", "GOOS")
	if err != nil || strings.TrimSpace(output) == "" {
		t.Fatalf("go env output=%q err=%v", output, err)
	}
	if output, err := runCommand("go", "env", "-definitely-invalid-flag"); err == nil || output == "" {
		t.Fatalf("invalid command output=%q err=%v", output, err)
	}
	if _, ok := commandSucceeded("go", "env", "GOARCH"); !ok {
		t.Fatal("expected successful command")
	}
	if _, ok := commandSucceeded("definitely-not-a-real-cm-command"); ok {
		t.Fatal("missing command succeeded")
	}
}

func TestPrepareManagedBinaryStagesTransientGoBuildBinaryByContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "go-build123", "b001", "exe", "cm")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("first-build"), 0755); err != nil {
		t.Fatal(err)
	}
	first, err := PrepareManagedBinary(root, source)
	if err != nil {
		t.Fatal(err)
	}
	if first == filepath.Clean(source) || !strings.Contains(filepath.ToSlash(first), "/runtime/bin/go-run/") {
		t.Fatalf("staged path = %q", first)
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "first-build" {
		t.Fatalf("staged binary data=%q err=%v", string(data), err)
	}
	if info, err := os.Stat(first); err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		t.Fatalf("staged binary permissions are invalid: info=%v err=%v", info, err)
	}
	reused, err := PrepareManagedBinary(root, source)
	if err != nil || reused != first {
		t.Fatalf("reused path=%q err=%v want=%q", reused, err, first)
	}
	if err := os.WriteFile(source, []byte("second-build"), 0755); err != nil {
		t.Fatal(err)
	}
	second, err := PrepareManagedBinary(root, source)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("changed binary reused staged path %q", second)
	}
}

func TestPrepareManagedBinaryKeepsNormalBinaryPath(t *testing.T) {
	source := filepath.Join(t.TempDir(), "cm")
	if err := os.WriteFile(source, []byte("installed-build"), 0755); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareManagedBinary(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	if prepared != filepath.Clean(source) {
		t.Fatalf("prepared path=%q want=%q", prepared, source)
	}
}
