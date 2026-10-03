package browser

import (
	"context"
	"errors"
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
	defaultInfo, err := os.Stat(filepath.Join(profile.LocalPath, managedProfileDirectory))
	if err != nil {
		t.Fatalf("managed Default profile was not prepared: %v", err)
	}
	if !defaultInfo.IsDir() {
		t.Fatal("managed Default profile is not a directory")
	}
	if runtime.GOOS != "windows" && defaultInfo.Mode().Perm() != 0700 {
		t.Fatalf("managed Default profile permissions=%#o want=0700", defaultInfo.Mode().Perm())
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

func TestPrepareWSLHostProfileCreatesManagedDefaultProfile(t *testing.T) {
	root := t.TempDir()
	profile := ProfileRef{
		HostPlatform: "windows",
		Transport:    TransportWSLHost,
		Path:         `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`,
		LocalPath:    filepath.Join(root, "CodeMCP", "Browser", "ChatGPT"),
	}
	if err := PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(profile.LocalPath, managedProfileDirectory)); err != nil || !info.IsDir() {
		t.Fatalf("managed Default profile missing: info=%v err=%v", info, err)
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

func TestProfileInUseTracksCanonicalLock(t *testing.T) {
	root := t.TempDir()
	profile := ProfileRef{
		Transport: TransportNative,
		LocalPath: filepath.Join(root, "browser", "chatgpt"),
		LockPath:  filepath.Join(root, "browser", "chatgpt.lock"),
	}
	busy, err := ProfileInUse(profile)
	if err != nil || busy {
		t.Fatalf("initial busy=%t err=%v", busy, err)
	}
	lock, ok, err := TryAcquireProfile(profile)
	if err != nil || !ok {
		t.Fatalf("lock ok=%t err=%v", ok, err)
	}
	defer lock.Release()
	busy, err = ProfileInUse(profile)
	if err != nil || !busy {
		t.Fatalf("locked busy=%t err=%v", busy, err)
	}
}

func TestRemoveProfileOnlyDeletesCodeMCPOwnedChatGPTProfile(t *testing.T) {
	root := t.TempDir()
	profile := ProfileRef{
		Transport: TransportNative,
		LocalPath: filepath.Join(root, "browser", "chatgpt"),
	}
	if err := PrepareProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile.LocalPath, "marker"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile.LocalPath); !os.IsNotExist(err) {
		t.Fatalf("profile still exists: %v", err)
	}
	ordinary := filepath.Join(root, ".config", "google-chrome")
	if err := os.MkdirAll(ordinary, 0700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile(ProfileRef{Transport: TransportNative, LocalPath: ordinary}); err == nil {
		t.Fatal("ordinary browser profile deletion was not rejected")
	}
	if _, err := os.Stat(ordinary); err != nil {
		t.Fatalf("ordinary profile changed: %v", err)
	}
}

func TestResolveOwnedProfilesIncludesNativeAndWSLHostWithoutInstalledBrowser(t *testing.T) {
	root := t.TempDir()
	profiles, err := ResolveOwnedProfiles(context.Background(), OwnedProfileOptions{
		StateRoot: root,
		Runtime: Runtime{
			GOOS: "linux",
			Env: func(name string) string {
				if name == "WSL_DISTRO_NAME" {
					return "Ubuntu"
				}
				return ""
			},
			WindowsEnv: func(_ context.Context, name string) (string, error) {
				if name == "LOCALAPPDATA" {
					return `C:\Users\Mew\AppData\Local`, nil
				}
				return "", errors.New("not found")
			},
			WindowsToLocal: func(_ context.Context, path string) (string, error) {
				if strings.EqualFold(path, `C:\Users\Mew\AppData\Local`) {
					return "/mnt/c/Users/Mew/AppData/Local", nil
				}
				return "", errors.New("not found")
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles=%#v", profiles)
	}
	if profiles[0].Transport != TransportNative || profiles[1].Transport != TransportWSLHost {
		t.Fatalf("profiles=%#v", profiles)
	}
}
