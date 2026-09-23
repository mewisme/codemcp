package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStageCreatesImmutableVersion(t *testing.T) {
	layout := testLayout(t)
	source := testBinary(t, "first")
	staged, err := Stage(layout, "v1.2.3", source)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Reused {
		t.Fatal("first stage unexpectedly reused an existing version")
	}
	data, err := os.ReadFile(staged.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first" {
		t.Fatalf("staged binary = %q", data)
	}
	stagedAgain, err := Stage(layout, "v1.2.3", source)
	if err != nil {
		t.Fatal(err)
	}
	if !stagedAgain.Reused {
		t.Fatal("identical version was not reused")
	}
}

func TestStageRejectsVersionContentConflict(t *testing.T) {
	layout := testLayout(t)
	if _, err := Stage(layout, "v1.2.3", testBinary(t, "first")); err != nil {
		t.Fatal(err)
	}
	_, err := Stage(layout, "v1.2.3", testBinary(t, "second"))
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v", err)
	}
}

func TestStageRejectsInvalidSource(t *testing.T) {
	layout := testLayout(t)
	if _, err := Stage(layout, "v1.0.0", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing source to fail")
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(layout, "v1.0.0", empty); err == nil {
		t.Fatal("expected empty source to fail")
	}
}

func TestCleanupKeepsSelectedVersions(t *testing.T) {
	layout := testLayout(t)
	for _, version := range []string{"v1.0.0", "v1.1.0", "v1.2.0"} {
		if _, err := Stage(layout, version, testBinary(t, version)); err != nil {
			t.Fatal(err)
		}
	}
	staging := filepath.Join(layout.Versions, ".staging-stale")
	if err := os.MkdirAll(staging, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(layout.Versions, "keep-me.txt")
	if err := os.WriteFile(marker, []byte("marker"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Cleanup(layout, "v1.1.0", "v1.2.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout.Versions, "v1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("old version still exists: %v", err)
	}
	for _, version := range []string{"v1.1.0", "v1.2.0"} {
		if _, err := os.Stat(filepath.Join(layout.Versions, version)); err != nil {
			t.Fatalf("kept version %s: %v", version, err)
		}
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging directory still exists: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("non-version file should be preserved: %v", err)
	}
}

func TestRemoveAllWithRetryEventuallySucceeds(t *testing.T) {
	attempts, sleeps := 0, []time.Duration{}
	err := removeAllWithRetry("old-version", 4, 10*time.Millisecond, func(string) error {
		attempts++
		if attempts < 3 {
			return errors.New("locked")
		}
		return nil
	}, func(delay time.Duration) { sleeps = append(sleeps, delay) })
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d want=3", attempts)
	}
	if len(sleeps) != 2 || sleeps[0] != 10*time.Millisecond || sleeps[1] != 20*time.Millisecond {
		t.Fatalf("sleeps=%v", sleeps)
	}
}

func TestRemoveAllWithRetryReturnsLastError(t *testing.T) {
	want := errors.New("still locked")
	attempts := 0
	err := removeAllWithRetry("old-version", 3, 0, func(string) error { attempts++; return want }, func(time.Duration) {})
	if !errors.Is(err, want) || attempts != 3 {
		t.Fatalf("error=%v attempts=%d", err, attempts)
	}
}

func TestCurrentVersionAcceptsCanonicalizedInstallRoot(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real")
	aliasRoot := filepath.Join(t.TempDir(), "alias")
	if err := os.MkdirAll(realRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	layout, err := NewLayout(filepath.Join(aliasRoot, "install"), filepath.Join(aliasRoot, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Options{Layout: layout, Version: "v1.0.0", Source: testBinary(t, "current")}); err != nil {
		t.Fatal(err)
	}
	version, _, err := CurrentVersion(layout)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.0.0" {
		t.Fatalf("current version = %q", version)
	}
	second, err := Install(Options{Layout: layout, Version: "v1.1.0", Source: testBinary(t, "next")})
	if err != nil {
		t.Fatal(err)
	}
	wantPrevious, err := layout.VersionDir("v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if second.Activation.PreviousTarget != wantPrevious {
		t.Fatalf("previous target = %q, want %q", second.Activation.PreviousTarget, wantPrevious)
	}
	if err := Rollback(second.Activation); err != nil {
		t.Fatal(err)
	}
	version, _, err = CurrentVersion(layout)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.0.0" {
		t.Fatalf("rollback version = %q", version)
	}
}

func testLayout(t *testing.T) Layout {
	t.Helper()
	root := filepath.Join(t.TempDir(), "install")
	layout, err := NewLayout(root, filepath.Join(root, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func testBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chatgpt-mcp")
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
