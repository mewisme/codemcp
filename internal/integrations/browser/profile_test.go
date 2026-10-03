package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveNativeProfileIsCodeMCPOwnedAndIsolated(t *testing.T) {
	root := t.TempDir()
	profile, err := ResolveProfile(ProfileOptions{
		StateRoot: root,
		Candidate: Candidate{HostPlatform: "linux", Transport: TransportNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "browser", "chatgpt")
	if profile.Path != want || profile.LocalPath != want || profile.LockPath != filepath.Join(root, "browser", "chatgpt.lock") {
		t.Fatalf("profile=%#v", profile)
	}
	for _, forbidden := range []string{".config/google-chrome", ".config/chromium", "Microsoft/Edge/User Data", "Google/Chrome/User Data"} {
		if strings.Contains(strings.ToLower(profile.Path), strings.ToLower(forbidden)) {
			t.Fatalf("profile aliases ordinary browser state: %#v", profile)
		}
	}
	if err := PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(profile.LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatalf("native profile permissions=%#o want=0700", info.Mode().Perm())
	}
}

func TestResolveWSLHostProfileLivesInWindowsLocalAppData(t *testing.T) {
	root := t.TempDir()
	profile, err := ResolveProfile(ProfileOptions{
		StateRoot: root,
		Candidate: Candidate{
			HostPlatform: "windows", Transport: TransportWSLHost,
			HostLocalAppData: `C:\Users\Mew\AppData\Local`,
			LocalAppData:     "/mnt/c/Users/Mew/AppData/Local",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Path != `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT` {
		t.Fatalf("host path=%q", profile.Path)
	}
	if profile.LocalPath != "/mnt/c/Users/Mew/AppData/Local/CodeMCP/Browser/ChatGPT" {
		t.Fatalf("local path=%q", profile.LocalPath)
	}
	if strings.HasPrefix(filepath.Clean(profile.LocalPath), filepath.Clean(root)) {
		t.Fatalf("Windows host profile is inside WSL config root: %#v", profile)
	}
}

func TestProfileLockAllowsOnlyOneProcessOwner(t *testing.T) {
	root := t.TempDir()
	profile := ProfileRef{LockPath: filepath.Join(root, "browser", "chatgpt.lock")}
	first, ok, err := TryAcquireProfile(profile)
	if err != nil || !ok {
		t.Fatalf("first lock ok=%t err=%v", ok, err)
	}
	defer first.Release()
	if second, ok, err := TryAcquireProfile(profile); err != nil {
		t.Fatal(err)
	} else if ok {
		_ = second.Release()
		t.Fatal("second profile owner acquired the same lock")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, ok, err := TryAcquireProfile(profile)
	if err != nil || !ok {
		t.Fatalf("profile lock was not reusable: ok=%t err=%v", ok, err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}
