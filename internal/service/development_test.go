package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareManagedRestartBinaryBuildsGoRunDevelopmentRuntime(t *testing.T) {
	sourceRoot := writeDevelopmentSourceFixture(t)
	t.Chdir(sourceRoot)
	t.Setenv("PWD", sourceRoot)

	configRoot := t.TempDir()
	source := filepath.Join(t.TempDir(), "go-build123", "b001", "exe", "cm")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("old-dev-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	calls := installDevelopmentBuildFakes(t, []byte("rebuilt-dev-binary"))
	prepared, err := PrepareManagedRestartBinaryContext(t.Context(), configRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	if !stagedGoRunBinary(configRoot, prepared) {
		t.Fatalf("prepared binary = %q", prepared)
	}
	data, err := os.ReadFile(prepared)
	if err != nil || string(data) != "rebuilt-dev-binary" {
		t.Fatalf("prepared data=%q err=%v", string(data), err)
	}
	storedRoot, err := loadGoRunSourceRoot(configRoot)
	if err != nil || storedRoot != sourceRoot {
		t.Fatalf("stored source root=%q err=%v want=%q", storedRoot, err, sourceRoot)
	}
	if got := strings.Join(*calls, "\n"); !strings.Contains(got, "pnpm --dir frontend build") || !strings.Contains(got, "go build ") {
		t.Fatalf("build calls:\n%s", got)
	}
}

func TestPrepareManagedRestartBinaryRebuildsStagedGoRunFromPersistedSource(t *testing.T) {
	sourceRoot := writeDevelopmentSourceFixture(t)
	t.Chdir(sourceRoot)
	t.Setenv("PWD", sourceRoot)

	configRoot := t.TempDir()
	source := filepath.Join(t.TempDir(), "go-build456", "b001", "exe", "cm")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("initial-dev-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	staged, err := PrepareManagedBinary(configRoot, source)
	if err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	t.Chdir(outside)
	t.Setenv("PWD", outside)
	calls := installDevelopmentBuildFakes(t, []byte("second-dev-binary"))

	prepared, err := PrepareManagedRestartBinaryContext(t.Context(), configRoot, staged)
	if err != nil {
		t.Fatal(err)
	}
	if prepared == staged {
		t.Fatalf("rebuilt development binary reused old path %q", prepared)
	}
	data, err := os.ReadFile(prepared)
	if err != nil || string(data) != "second-dev-binary" {
		t.Fatalf("prepared data=%q err=%v", string(data), err)
	}
	if len(*calls) != 2 {
		t.Fatalf("build calls=%v", *calls)
	}
}

func TestPrepareManagedRestartBinaryKeepsStableCMWithoutBuild(t *testing.T) {
	source := filepath.Join(t.TempDir(), "cm")
	if err := os.WriteFile(source, []byte("installed"), 0755); err != nil {
		t.Fatal(err)
	}
	previousCommand := developmentCommand
	previousLookPath := developmentLookPath
	developmentCommand = func(context.Context, string, string, ...string) (string, error) {
		t.Fatal("stable cm restart unexpectedly invoked development build")
		return "", nil
	}
	developmentLookPath = func(string) (string, error) {
		t.Fatal("stable cm restart unexpectedly resolved frontend tooling")
		return "", errors.New("unreachable")
	}
	t.Cleanup(func() {
		developmentCommand = previousCommand
		developmentLookPath = previousLookPath
	})

	prepared, err := PrepareManagedRestartBinaryContext(t.Context(), t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	if prepared != filepath.Clean(source) {
		t.Fatalf("prepared=%q want=%q", prepared, source)
	}
}

func TestStagedGoRunRestartRequiresKnownSourceRoot(t *testing.T) {
	configRoot := t.TempDir()
	staged := filepath.Join(configRoot, "runtime", "bin", "go-run", "deadbeef", "cm")
	if err := os.MkdirAll(filepath.Dir(staged), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("staged"), 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	t.Chdir(outside)
	t.Setenv("PWD", outside)

	_, err := PrepareManagedRestartBinaryContext(t.Context(), configRoot, staged)
	if err == nil || !strings.Contains(err.Error(), "source root is unavailable") {
		t.Fatalf("error=%v", err)
	}
}

func writeDevelopmentSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module go.mewis.me/codemcp\n\ngo 1.27\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "frontend"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "package.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func installDevelopmentBuildFakes(t *testing.T, binary []byte) *[]string {
	t.Helper()
	previousCommand := developmentCommand
	previousLookPath := developmentLookPath
	calls := []string{}
	developmentLookPath = func(name string) (string, error) {
		if name == "pnpm" {
			return "pnpm", nil
		}
		return "", errors.New("not found")
	}
	developmentCommand = func(_ context.Context, dir, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "go" {
			output := ""
			for index := 0; index+1 < len(args); index++ {
				if args[index] == "-o" {
					output = args[index+1]
					break
				}
			}
			if output == "" {
				t.Fatalf("go build missing -o: %v", args)
			}
			if err := os.WriteFile(output, binary, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if dir == "" {
			t.Fatal("development command missing source directory")
		}
		return "", nil
	}
	t.Cleanup(func() {
		developmentCommand = previousCommand
		developmentLookPath = previousLookPath
	})
	return &calls
}
