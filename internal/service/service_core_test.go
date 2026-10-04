package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
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

func TestMachineNamespaceUsesCM(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if got, want := DefaultConfigRoot(Account{HomeDir: home}), filepath.Join(home, ".cm"); got != want {
		t.Fatalf("default config root = %q, want %q", got, want)
	}
	for _, scope := range []Scope{ScopeUser, ScopeSystem} {
		id := ID(filepath.Join(home, ".cm"), scope)
		if !strings.HasPrefix(id, "cm-"+string(scope)+"-") {
			t.Fatalf("service id = %q", id)
		}
		if strings.Contains(id, "chatgpt-mcp") {
			t.Fatalf("service id contains legacy namespace: %q", id)
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

func TestWaitRuntimeReadyUsesStatusUpdateWaitWithoutPollDelay(t *testing.T) {
	spec := Spec{ID: "cm-user-test", Scope: ScopeUser}
	notReady := runtimecontrol.RuntimeStatus{PID: 42, RunID: "run_test", Lifecycle: "tunnel_connecting", Starting: true, Managed: true, ServiceID: spec.ID, ServiceScope: string(spec.Scope)}
	ready := notReady
	ready.Starting = false
	probeCalls := 0
	probe := func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
		probeCalls++
		if probeCalls == 1 {
			return notReady, true, nil
		}
		return ready, true, nil
	}
	waitCalls := 0
	waitUpdate := func(context.Context, runtimecontrol.RuntimeStatus) (runtimecontrol.RuntimeStatus, error) {
		waitCalls++
		return ready, nil
	}
	started := time.Now()
	status, err := waitRuntimeReady(t.Context(), spec, probe, nil, waitUpdate, "", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status.Starting || waitCalls != 1 || probeCalls != 2 {
		t.Fatalf("status=%#v waitCalls=%d probeCalls=%d", status, waitCalls, probeCalls)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("status update wait fell back to polling: %s", elapsed)
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

func TestPrepareManagedBinaryPrunesOldGoRunStages(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "go-build123", "b001", "exe", "cm")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}

	staged := make([]string, 0, goRunStagedBinaryRetention+2)
	for index := 0; index < goRunStagedBinaryRetention+2; index++ {
		if err := os.WriteFile(source, []byte(fmt.Sprintf("build-%d", index)), 0755); err != nil {
			t.Fatal(err)
		}
		prepared, err := PrepareManagedBinary(root, source)
		if err != nil {
			t.Fatal(err)
		}
		staged = append(staged, prepared)
		time.Sleep(time.Millisecond)
	}

	stageRoot := filepath.Join(root, "runtime", "bin", "go-run")
	entries, err := os.ReadDir(stageRoot)
	if err != nil {
		t.Fatal(err)
	}
	stageDirs := 0
	for _, entry := range entries {
		if entry.IsDir() && goRunStageDirectoryName(entry.Name()) {
			stageDirs++
		}
	}
	if stageDirs != goRunStagedBinaryRetention {
		t.Fatalf("staged directories=%d want=%d", stageDirs, goRunStagedBinaryRetention)
	}
	if _, err := os.Stat(staged[len(staged)-1]); err != nil {
		t.Fatalf("latest staged binary removed: %v", err)
	}
	if _, err := os.Stat(staged[0]); !os.IsNotExist(err) {
		t.Fatalf("oldest staged binary still exists: err=%v", err)
	}
}

func TestPruneGoRunStagedBinariesPreservesNonStageEntries(t *testing.T) {
	root := t.TempDir()
	stageRoot := filepath.Join(root, "runtime", "bin", "go-run")
	if err := os.MkdirAll(filepath.Join(stageRoot, ".build"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stageRoot, "custom-data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageRoot, "source-root"), []byte("/tmp/source\n"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(stageRoot, ".build"),
		filepath.Join(stageRoot, "custom-data"),
		filepath.Join(stageRoot, "source-root"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("fixture missing before prune %q: %v", path, err)
		}
	}

	pruneGoRunStagedBinaries(root, filepath.Join(stageRoot, "0123456789abcdef", "cm"))

	for _, path := range []string{
		filepath.Join(stageRoot, ".build"),
		filepath.Join(stageRoot, "custom-data"),
		filepath.Join(stageRoot, "source-root"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("non-stage entry removed %q: %v", path, err)
		}
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
